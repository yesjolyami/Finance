package finance

import (
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"time"

	"github.com/google/uuid"
)

const cursorSchemaVersion = 1

type cursorEnvelope struct {
	Version     int    `json:"v"`
	Domain      string `json:"domain"`
	CreatedAt   string `json:"createdAt"`
	ID          string `json:"id"`
	Fingerprint string `json:"fingerprint"`
}

type filterFingerprintEnvelope struct {
	Schema int    `json:"schema"`
	Domain string `json:"domain"`
	Filter any    `json:"filter"`
}

func encodeCursor(domain string, filter any, position CursorPosition) (string, error) {
	if domain == "" || position.ID == uuid.Nil || position.CreatedAt.IsZero() {
		return "", ErrInvalidQuery
	}
	envelope := cursorEnvelope{
		Version:     cursorSchemaVersion,
		Domain:      domain,
		CreatedAt:   position.CreatedAt.UTC().Format(time.RFC3339Nano),
		ID:          position.ID.String(),
		Fingerprint: filterFingerprint(domain, filter),
	}
	encoded, err := json.Marshal(envelope)
	if err != nil {
		return "", ErrInvalidQuery
	}
	cursor := base64.RawURLEncoding.EncodeToString(encoded)
	if len(cursor) > MaximumCursorBytes {
		return "", ErrInvalidQuery
	}
	return cursor, nil
}

func decodeCursor(cursor, domain string, filter any) (CursorPosition, error) {
	if cursor == "" || len(cursor) > MaximumCursorBytes || strings.Contains(cursor, "=") {
		return CursorPosition{}, ErrInvalidQuery
	}
	decoded, err := base64.RawURLEncoding.Strict().DecodeString(cursor)
	if err != nil || len(decoded) == 0 || len(decoded) > MaximumCursorBytes {
		return CursorPosition{}, ErrInvalidQuery
	}
	if err := rejectDuplicateCursorFields(decoded); err != nil {
		return CursorPosition{}, ErrInvalidQuery
	}
	var envelope cursorEnvelope
	decoder := json.NewDecoder(strings.NewReader(string(decoded)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&envelope); err != nil {
		return CursorPosition{}, ErrInvalidQuery
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return CursorPosition{}, ErrInvalidQuery
	}
	if envelope.Version != cursorSchemaVersion || envelope.Domain != domain ||
		envelope.ID == "" || envelope.CreatedAt == "" || envelope.Fingerprint == "" {
		return CursorPosition{}, ErrInvalidQuery
	}
	expectedFingerprint := filterFingerprint(domain, filter)
	if subtle.ConstantTimeCompare([]byte(envelope.Fingerprint), []byte(expectedFingerprint)) != 1 {
		return CursorPosition{}, ErrInvalidQuery
	}
	id, err := parseCanonicalUUID(envelope.ID)
	if err != nil {
		return CursorPosition{}, ErrInvalidQuery
	}
	createdAt, err := time.Parse(time.RFC3339Nano, envelope.CreatedAt)
	if err != nil || createdAt.IsZero() || createdAt.Location() != time.UTC ||
		createdAt.Format(time.RFC3339Nano) != envelope.CreatedAt {
		return CursorPosition{}, ErrInvalidQuery
	}
	canonical, err := json.Marshal(envelope)
	if err != nil || base64.RawURLEncoding.EncodeToString(canonical) != cursor {
		return CursorPosition{}, ErrInvalidQuery
	}
	return CursorPosition{CreatedAt: createdAt, ID: id}, nil
}

func rejectDuplicateCursorFields(encoded []byte) error {
	decoder := json.NewDecoder(strings.NewReader(string(encoded)))
	token, err := decoder.Token()
	if err != nil || token != json.Delim('{') {
		return ErrInvalidQuery
	}
	seen := make(map[string]struct{}, 5)
	for decoder.More() {
		keyToken, tokenErr := decoder.Token()
		key, ok := keyToken.(string)
		if tokenErr != nil || !ok {
			return ErrInvalidQuery
		}
		if _, duplicate := seen[key]; duplicate {
			return ErrInvalidQuery
		}
		seen[key] = struct{}{}
		var value json.RawMessage
		if err := decoder.Decode(&value); err != nil {
			return ErrInvalidQuery
		}
	}
	if token, err := decoder.Token(); err != nil || token != json.Delim('}') {
		return ErrInvalidQuery
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return ErrInvalidQuery
	}
	return nil
}

func filterFingerprint(domain string, filter any) string {
	encoded, err := json.Marshal(filterFingerprintEnvelope{
		Schema: cursorSchemaVersion,
		Domain: domain,
		Filter: filter,
	})
	if err != nil {
		panic("finance cursor filter contains an unsupported value: " + err.Error())
	}
	hash := sha256.Sum256(encoded)
	return hex.EncodeToString(hash[:])
}
