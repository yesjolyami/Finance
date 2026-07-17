package backupv5

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"io"
	"strconv"
	"unicode/utf8"

	"github.com/google/uuid"
)

func ParseBytes(ctx context.Context, raw []byte, input Input) (*Model, error) {
	if int64(len(raw)) > MaxRawBytes {
		return nil, validationError(ErrLimit, "raw_body_too_large", "$")
	}
	return parse(ctx, raw, input)
}

func ParseReader(ctx context.Context, reader io.Reader, input Input) (*Model, error) {
	raw, err := readBounded(ctx, reader)
	if err != nil {
		return nil, err
	}
	return parse(ctx, raw, input)
}

func readBounded(ctx context.Context, reader io.Reader) ([]byte, error) {
	if reader == nil {
		return nil, validationError(ErrInvalidJSON, "empty_body", "$")
	}
	var output bytes.Buffer
	buffer := make([]byte, 32*1024)
	for {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		read, err := reader.Read(buffer)
		if read < 0 || read > len(buffer) {
			return nil, validationError(ErrInvalidJSON, "read_failed", "$")
		}
		if read > 0 {
			if int64(output.Len()+read) > MaxRawBytes {
				return nil, validationError(ErrLimit, "raw_body_too_large", "$")
			}
			_, _ = output.Write(buffer[:read])
		}
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, validationError(ErrInvalidJSON, "read_failed", "$")
		}
		if read == 0 {
			return nil, validationError(ErrInvalidJSON, "read_failed", "$")
		}
	}
	return output.Bytes(), nil
}

func parse(ctx context.Context, raw []byte, input Input) (*Model, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if len(raw) == 0 {
		return nil, validationError(ErrInvalidJSON, "empty_body", "$")
	}
	if !utf8.Valid(raw) {
		return nil, validationError(ErrInvalidJSON, "invalid_utf8", "$")
	}
	if bytes.HasPrefix(raw, []byte{0xef, 0xbb, 0xbf}) {
		return nil, validationError(ErrInvalidJSON, "utf8_bom_forbidden", "$")
	}
	if err := validateRawJSON(ctx, raw); err != nil {
		return nil, err
	}
	if input.HouseholdID == uuid.Nil {
		return nil, validationError(ErrValue, "invalid_household_id", "input.householdId")
	}
	budgetMonth, err := parseLocalDate(input.BudgetMonth, "input.budgetMonth")
	if err != nil {
		return nil, err
	}
	if budgetMonth.Day() != 1 {
		return nil, validationError(ErrValue, "budget_month_not_first_day", "input.budgetMonth")
	}
	model, err := decodeAndNormalize(ctx, raw, input.HouseholdID, budgetMonth)
	if err != nil {
		return nil, err
	}
	model.BackupDigest = sha256.Sum256(raw)
	return model, nil
}

func validateRawJSON(ctx context.Context, raw []byte) error {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	state := rawScanState{}
	if err := scanJSONValue(ctx, decoder, 0, true, -1, &state); err != nil {
		return err
	}
	token, err := decoder.Token()
	if err == nil {
		_ = token
		return validationError(ErrInvalidJSON, "trailing_value", "$")
	}
	if !errors.Is(err, io.EOF) {
		return validationError(ErrInvalidJSON, "trailing_bytes", "$")
	}
	return nil
}

type rawScanState struct {
	entityTotal int
}

func scanJSONValue(
	ctx context.Context,
	decoder *json.Decoder,
	depth int,
	root bool,
	arrayLimit int,
	state *rawScanState,
) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	token, err := decoder.Token()
	if err != nil {
		return validationError(ErrInvalidJSON, "malformed_json", "$")
	}
	switch value := token.(type) {
	case json.Delim:
		containerDepth := depth + 1
		if containerDepth > MaxJSONDepth {
			return validationError(ErrLimit, "json_depth_exceeded", "$")
		}
		switch value {
		case '{':
			seen := make(map[string]struct{})
			for decoder.More() {
				if err := ctx.Err(); err != nil {
					return err
				}
				keyToken, keyErr := decoder.Token()
				if keyErr != nil {
					return validationError(ErrInvalidJSON, "malformed_json", "$")
				}
				key, ok := keyToken.(string)
				if !ok {
					return validationError(ErrInvalidJSON, "malformed_object", "$")
				}
				if len(key) > MaxRawStringBytes {
					return validationError(ErrLimit, "json_string_too_long", "$")
				}
				if _, duplicate := seen[key]; duplicate {
					return validationError(ErrInvalidJSON, "duplicate_key", "$")
				}
				seen[key] = struct{}{}
				childLimit := -1
				if root {
					childLimit = rootEntityLimit(key)
				}
				if err := scanJSONValue(ctx, decoder, containerDepth, false, childLimit, state); err != nil {
					return err
				}
			}
			closing, closeErr := decoder.Token()
			if closeErr != nil || closing != json.Delim('}') {
				return validationError(ErrInvalidJSON, "malformed_object", "$")
			}
		case '[':
			count := 0
			for decoder.More() {
				if arrayLimit >= 0 {
					count++
					state.entityTotal++
					if count > arrayLimit {
						return validationError(ErrLimit, "entity_count_exceeded", "$")
					}
					if state.entityTotal > MaxRawEntities {
						return validationError(ErrLimit, "total_entity_count_exceeded", "$")
					}
				}
				if err := scanJSONValue(ctx, decoder, containerDepth, false, -1, state); err != nil {
					return err
				}
			}
			closing, closeErr := decoder.Token()
			if closeErr != nil || closing != json.Delim(']') {
				return validationError(ErrInvalidJSON, "malformed_array", "$")
			}
		default:
			return validationError(ErrInvalidJSON, "unexpected_delimiter", "$")
		}
	case string:
		if len(value) > MaxRawStringBytes {
			return validationError(ErrLimit, "json_string_too_long", "$")
		}
	case json.Number:
		if !isLexicalInteger(value.String()) {
			return validationError(ErrValue, "non_integer_number", "$")
		}
	case bool, nil:
	default:
		return validationError(ErrInvalidJSON, "unsupported_token", "$")
	}
	return nil
}

func rootEntityLimit(key string) int {
	switch key {
	case "accounts":
		return MaxAccounts
	case "categories":
		return MaxCategories
	case "transactions":
		return MaxTransactions
	case "goals":
		return MaxGoals
	case "debts":
		return MaxDebts
	case "debtPayments":
		return MaxDebtPayments
	default:
		return -1
	}
}

func isLexicalInteger(value string) bool {
	if value == "" {
		return false
	}
	start := 0
	if value[0] == '-' {
		if len(value) == 1 {
			return false
		}
		start = 1
		if value[start] == '0' {
			return false
		}
	}
	if value[start] == '0' {
		return len(value) == start+1
	}
	if value[start] < '1' || value[start] > '9' {
		return false
	}
	for index := start + 1; index < len(value); index++ {
		if value[index] < '0' || value[index] > '9' {
			return false
		}
	}
	return true
}

func decodeObject(raw json.RawMessage, required []string, path string) (map[string]json.RawMessage, error) {
	var object map[string]json.RawMessage
	if err := json.Unmarshal(raw, &object); err != nil || object == nil {
		return nil, validationError(ErrSchema, "object_required", path)
	}
	allowed := make(map[string]struct{}, len(required))
	for _, field := range required {
		allowed[field] = struct{}{}
		if _, present := object[field]; !present {
			return nil, validationError(ErrSchema, "required_field_missing", path+"."+field)
		}
	}
	for field := range object {
		if _, present := allowed[field]; !present {
			return nil, validationError(ErrSchema, "unknown_field", path)
		}
	}
	return object, nil
}

func decodeArray(raw json.RawMessage, limit int, path string) ([]json.RawMessage, error) {
	var values []json.RawMessage
	if err := json.Unmarshal(raw, &values); err != nil || values == nil {
		return nil, validationError(ErrSchema, "array_required", path)
	}
	if len(values) > limit {
		return nil, validationError(ErrLimit, "entity_count_exceeded", path)
	}
	return values, nil
}

func decodeString(raw json.RawMessage, path string) (string, error) {
	var value string
	if err := json.Unmarshal(raw, &value); err != nil {
		return "", validationError(ErrSchema, "string_required", path)
	}
	return value, nil
}

func decodeNullableString(raw json.RawMessage, path string) (*string, error) {
	if bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		return nil, nil
	}
	value, err := decodeString(raw, path)
	if err != nil {
		return nil, err
	}
	return &value, nil
}

func decodeBool(raw json.RawMessage, path string) (bool, error) {
	var value bool
	if err := json.Unmarshal(raw, &value); err != nil {
		return false, validationError(ErrSchema, "boolean_required", path)
	}
	return value, nil
}

func decodeIntegerToken(raw json.RawMessage, path string) (string, error) {
	value := string(bytes.TrimSpace(raw))
	if !isLexicalInteger(value) {
		return "", validationError(ErrSchema, "integer_required", path)
	}
	return value, nil
}

func itemPath(collection string, index int) string {
	return collection + "[" + strconv.Itoa(index) + "]"
}
