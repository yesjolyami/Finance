package backupv5

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
)

type fakeConfirmRepository struct {
	command      ConfirmCommand
	result       ConfirmResult
	err          error
	referenced   []string
	auditErr     error
	deadlineLeft time.Duration
	confirmCalls int
}

func (repository *fakeConfirmRepository) Confirm(ctx context.Context, command ConfirmCommand) (ConfirmResult, error) {
	repository.command = command
	repository.confirmCalls++
	if deadline, exists := ctx.Deadline(); exists {
		repository.deadlineLeft = time.Until(deadline)
	}
	return repository.result, repository.err
}

func (repository *fakeConfirmRepository) ReferencedHMACKeyIDs(context.Context) ([]string, error) {
	return append([]string(nil), repository.referenced...), repository.auditErr
}

func testHMACKeyring(t *testing.T) *HMACKeyring {
	t.Helper()
	keyring, err := NewHMACKeyring("active", map[string][]byte{
		"active": bytes.Repeat([]byte{0x11}, 32),
		"old":    bytes.Repeat([]byte{0x22}, 48),
	})
	if err != nil {
		t.Fatal(err)
	}
	return keyring
}

func validConfirmInput() ConfirmInput {
	token := base64RawURL(bytes.Repeat([]byte{0x33}, previewTokenBytes))
	return ConfirmInput{
		Subject: previewActorSubject, HouseholdID: fixtureInput.HouseholdID,
		BudgetMonth: fixtureInput.BudgetMonth, RawJSON: strings.NewReader(canonicalFixture),
		PreviewToken: token, IdempotencyKey: "confirm-key-1", ServerRequestID: "request-1",
	}
}

func TestHMACKeyringValidationCopiesAndCanonicalFixtures(t *testing.T) {
	key := make([]byte, 32)
	for index := range key {
		key[index] = byte(index)
	}
	source := map[string][]byte{"fixture": key}
	keyring, err := NewHMACKeyring("fixture", source)
	if err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256([]byte("fixture"))
	candidate := keyring.candidates("confirm-key-1", digest, "2026-07-01")[0]
	if got := hex.EncodeToString(candidate.Lookup[:]); got != "40db9ad49aa2c07a4de795e507584217273cb7a79f028610673d099e0e60c0dd" {
		t.Fatalf("lookup HMAC fixture = %s", got)
	}
	if got := hex.EncodeToString(candidate.Fingerprint[:]); got != "f8074c7c33810cf3ddaedd8cf79fde71ed6122062198bf803419e52f02101b99" {
		t.Fatalf("fingerprint fixture = %s", got)
	}
	key[0] ^= 0xff
	source["fixture"][1] ^= 0xff
	again := keyring.candidates("confirm-key-1", digest, "2026-07-01")[0]
	if again != candidate {
		t.Fatal("keyring retained caller-owned key bytes")
	}
	if requestFingerprint(bytes.Repeat([]byte{1}, 32), digest, "2026-07-01") ==
		requestFingerprint(bytes.Repeat([]byte{1}, 32), digest, "2026-07-010") {
		t.Fatal("length-framed fingerprint is ambiguous")
	}
}

func TestHMACKeyringRejectsInvalidConfiguration(t *testing.T) {
	valid := bytes.Repeat([]byte{1}, 32)
	tooMany := make(map[string][]byte)
	for index := 0; index < maxHMACKeys+1; index++ {
		tooMany[fmt.Sprintf("key-%02d", index)] = valid
	}
	for _, test := range []struct {
		name   string
		active string
		keys   map[string][]byte
	}{
		{name: "empty", active: "active", keys: nil},
		{name: "invalid active id", active: "bad id", keys: map[string][]byte{"bad id": valid}},
		{name: "invalid retained id", active: "active", keys: map[string][]byte{"active": valid, "bad/id": valid}},
		{name: "active absent", active: "active", keys: map[string][]byte{"old": valid}},
		{name: "short key", active: "active", keys: map[string][]byte{"active": bytes.Repeat([]byte{1}, 31)}},
		{name: "too many", active: "key-00", keys: tooMany},
	} {
		t.Run(test.name, func(t *testing.T) {
			if _, err := NewHMACKeyring(test.active, test.keys); !errors.Is(err, ErrInvalidKeyring) {
				t.Fatalf("error = %v", err)
			}
		})
	}
}

func TestHMACKeyringNeverFormatsOrSerializesSecrets(t *testing.T) {
	keyID := "sensitive-key-id"
	secret := []byte("synthetic-secret-material-32-bytes!")
	keyring, err := NewHMACKeyring(keyID, map[string][]byte{keyID: secret})
	if err != nil {
		t.Fatal(err)
	}
	outputs := []string{
		fmt.Sprintf("%v", keyring), fmt.Sprintf("%+v", keyring), fmt.Sprintf("%#v", keyring),
		fmt.Sprintf("%v", *keyring), fmt.Sprintf("%+v", *keyring), fmt.Sprintf("%#v", *keyring),
	}
	var logOutput bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&logOutput, nil))
	logger.Info("keyring", "value", keyring)
	outputs = append(outputs, logOutput.String())
	for _, output := range outputs {
		if strings.Contains(output, keyID) || strings.Contains(output, string(secret)) || strings.Contains(output, fmt.Sprint(secret)) {
			t.Fatalf("keyring formatting leaked secret material: %s", output)
		}
	}
	if _, err := json.Marshal(keyring); !errors.Is(err, ErrInvalidKeyring) {
		t.Fatalf("JSON error = %v", err)
	}
}

func TestConfirmInputsNeverFormatOrSerializeSecrets(t *testing.T) {
	input := validConfirmInput()
	command := ConfirmCommand{ActorSubject: input.Subject, ServerRequestID: input.ServerRequestID}
	for _, value := range []any{input, command} {
		for _, output := range []string{fmt.Sprintf("%v", value), fmt.Sprintf("%+v", value), fmt.Sprintf("%#v", value)} {
			if strings.Contains(output, input.PreviewToken) || strings.Contains(output, input.IdempotencyKey) || strings.Contains(output, input.Subject) {
				t.Fatalf("confirm boundary formatting leaked input: %s", output)
			}
		}
		if _, err := json.Marshal(value); !errors.Is(err, ErrInvalidService) {
			t.Fatalf("serialization error = %v", err)
		}
	}
}

func TestConfirmServiceBuildsHashOnlyCommand(t *testing.T) {
	repository := &fakeConfirmRepository{result: ConfirmResult{Response: ConfirmResponse{Status: "completed"}}}
	service, err := NewConfirmService(repository, testHMACKeyring(t))
	if err != nil {
		t.Fatal(err)
	}
	input := validConfirmInput()
	result, err := service.Confirm(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	if result.Response.Status != "completed" || repository.confirmCalls != 1 {
		t.Fatalf("result/calls = %#v/%d", result, repository.confirmCalls)
	}
	decoded, _ := base64DecodeRawURL(input.PreviewToken)
	if repository.command.TokenHash != sha256.Sum256(decoded) || !repository.command.TokenValid {
		t.Fatal("token was not reduced to its SHA-256 hash")
	}
	if repository.command.Model == nil || repository.command.Model.BackupDigest != sha256.Sum256([]byte(canonicalFixture)) {
		t.Fatal("confirm did not perform exact bounded reparse")
	}
	if repository.command.ActiveKeyID != "active" || len(repository.command.Candidates) != 2 {
		t.Fatalf("rotation candidates = %#v", repository.command.Candidates)
	}
	commandType := reflect.TypeOf(repository.command)
	for index := 0; index < commandType.NumField(); index++ {
		field := commandType.Field(index).Name
		if field == "PreviewToken" || field == "IdempotencyKey" || field == "RawJSON" {
			t.Fatalf("repository command exposes raw secret field %s", field)
		}
	}
}

func TestConfirmTokenAndOpaqueHeaders(t *testing.T) {
	validToken := base64RawURL(bytes.Repeat([]byte{7}, 32))
	if _, valid := decodePreviewToken(validToken); !valid {
		t.Fatal("valid token rejected")
	}
	invalidTokens := []string{
		"", validToken + "=", "not+base64", base64RawURL(bytes.Repeat([]byte{7}, 31)),
		base64RawURL(bytes.Repeat([]byte{7}, 33)), " " + validToken,
	}
	for _, token := range invalidTokens {
		if _, valid := decodePreviewToken(token); valid {
			t.Fatalf("invalid token accepted: %q", token)
		}
	}
	for _, test := range []struct {
		value string
		kind  error
	}{
		{"", ErrIdempotencyKey}, {" key", ErrIdempotencyKey}, {"key ", ErrIdempotencyKey},
		{"key\x00", ErrIdempotencyKey}, {"key\n", ErrIdempotencyKey},
		{strings.Repeat("x", 256), ErrIdempotencyKey}, {string([]byte{0xff}), ErrIdempotencyKey},
		{" request", ErrRequestID}, {"request\x1f", ErrRequestID}, {strings.Repeat("x", 256), ErrRequestID},
	} {
		if err := validateOpaqueHeader(test.value, 255, test.kind); !errors.Is(err, test.kind) {
			t.Fatalf("value %q error = %v", test.value, err)
		}
	}
	if err := validateOpaqueHeader(strings.Repeat("x", 255), 255, ErrIdempotencyKey); err != nil {
		t.Fatalf("boundary key rejected: %v", err)
	}
}

func TestPreviewExpiryIsHalfOpen(t *testing.T) {
	expiresAt := time.Date(2026, 7, 15, 12, 0, 0, 0, time.UTC)
	if !previewTimeActive(expiresAt.Add(-time.Nanosecond), expiresAt) {
		t.Fatal("token immediately before expiry is inactive")
	}
	if previewTimeActive(expiresAt, expiresAt) {
		t.Fatal("token at exact expiry is active")
	}
	if previewTimeActive(expiresAt.Add(time.Nanosecond), expiresAt) {
		t.Fatal("token after expiry is active")
	}
}

func TestConfirmReplayConflictPrecedesMalformedTokenAtRepositoryBoundary(t *testing.T) {
	repository := &fakeConfirmRepository{err: ErrIdempotencyConflict}
	service, _ := NewConfirmService(repository, testHMACKeyring(t))
	input := validConfirmInput()
	input.PreviewToken = "malformed="
	_, err := service.Confirm(context.Background(), input)
	if !errors.Is(err, ErrIdempotencyConflict) || repository.command.TokenValid {
		t.Fatalf("error/tokenValid = %v/%v", err, repository.command.TokenValid)
	}
}

func TestConfirmTimeoutDoesNotExtendCallerDeadline(t *testing.T) {
	for _, test := range []struct {
		name       string
		caller     time.Duration
		configured time.Duration
		maximum    time.Duration
	}{
		{name: "service deadline", configured: 50 * time.Millisecond, maximum: 50 * time.Millisecond},
		{name: "short caller", caller: 20 * time.Millisecond, configured: time.Second, maximum: 20 * time.Millisecond},
	} {
		t.Run(test.name, func(t *testing.T) {
			repository := &fakeConfirmRepository{result: ConfirmResult{Response: ConfirmResponse{Status: "completed"}}}
			service, err := NewConfirmService(repository, testHMACKeyring(t), WithConfirmTimeout(test.configured))
			if err != nil {
				t.Fatal(err)
			}
			ctx := context.Background()
			if test.caller > 0 {
				var cancel context.CancelFunc
				ctx, cancel = context.WithTimeout(ctx, test.caller)
				defer cancel()
			}
			if _, err := service.Confirm(ctx, validConfirmInput()); err != nil {
				t.Fatal(err)
			}
			if repository.deadlineLeft <= 0 || repository.deadlineLeft > test.maximum+10*time.Millisecond {
				t.Fatalf("deadline left = %s", repository.deadlineLeft)
			}
		})
	}
	for _, timeout := range []time.Duration{0, -1, MaximumConfirmTimeout + time.Nanosecond} {
		if _, err := NewConfirmService(&fakeConfirmRepository{}, testHMACKeyring(t), WithConfirmTimeout(timeout)); !errors.Is(err, ErrInvalidService) {
			t.Fatalf("timeout %s error = %v", timeout, err)
		}
	}
}

func TestConfirmKeyringAuditSupportsRotationAndFailsClosed(t *testing.T) {
	repository := &fakeConfirmRepository{referenced: []string{"active", "old"}}
	service, _ := NewConfirmService(repository, testHMACKeyring(t))
	if err := service.AuditKeyring(context.Background()); err != nil {
		t.Fatal(err)
	}
	repository.referenced = append(repository.referenced, "retired-missing")
	if err := service.AuditKeyring(context.Background()); !errors.Is(err, ErrReferencedKeyMissing) {
		t.Fatalf("missing key error = %v", err)
	}
	repository.auditErr = fmt.Errorf("secret SQL: %w", ErrRepository)
	if err := service.AuditKeyring(context.Background()); !errors.Is(err, ErrRepository) || strings.Contains(err.Error(), "secret") {
		t.Fatalf("unsafe audit error = %v", err)
	}
}

func TestConfirmResponseJSONExactAllowlist(t *testing.T) {
	model, err := ParseReader(context.Background(), strings.NewReader(canonicalFixture), fixtureInput)
	if err != nil {
		t.Fatal(err)
	}
	response := confirmResponse(uuid.MustParse("00000000-0000-4000-8000-000000000001"), time.Date(2026, 7, 15, 9, 0, 0, 0, time.UTC), model)
	encoded, err := json.Marshal(response)
	if err != nil {
		t.Fatal(err)
	}
	var object map[string]json.RawMessage
	if err := json.Unmarshal(encoded, &object); err != nil {
		t.Fatal(err)
	}
	assertJSONKeys(t, object, []string{"completedAt", "counts", "importRunId", "policyVersion", "status", "warningCounts"})
	var warnings map[string]json.RawMessage
	if err := json.Unmarshal(object["warningCounts"], &warnings); err != nil {
		t.Fatal(err)
	}
	assertJSONKeys(t, warnings, []string{
		"archiveTimeApproximated", "budgetMonthExplicitChoice", "debtOverpaid",
		"goalExceedsTarget", "legacyOwnerNotLinked", "systemResourcePreserved",
	})
	for _, forbidden := range []string{`"backupDigest":`, `"budgetMonth":`, `"token":`, `"idempotencyKey":`, `"incomeCents":`, "Main account", "Debt note"} {
		if strings.Contains(string(encoded), forbidden) {
			t.Fatalf("response exposed %q: %s", forbidden, encoded)
		}
	}
}

func base64RawURL(value []byte) string {
	return base64.RawURLEncoding.EncodeToString(value)
}

func base64DecodeRawURL(value string) ([]byte, error) {
	return base64.RawURLEncoding.DecodeString(value)
}
