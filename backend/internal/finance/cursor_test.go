package finance

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestCursorRoundTripAndFilterFingerprint(t *testing.T) {
	t.Parallel()

	filter := AccountListFilter{State: "active"}
	position := CursorPosition{
		CreatedAt: time.Date(2026, 7, 15, 12, 34, 56, 123456000, time.UTC),
		ID:        uuid.MustParse("123e4567-e89b-42d3-a456-426614174000"),
	}
	cursor, err := encodeCursor("accounts", filter, position)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := decodeCursor(cursor, "accounts", filter)
	if err != nil {
		t.Fatal(err)
	}
	if !decoded.CreatedAt.Equal(position.CreatedAt) || decoded.ID != position.ID {
		t.Fatalf("decoded cursor = %#v; want %#v", decoded, position)
	}
	if _, err := decodeCursor(cursor, "accounts", AccountListFilter{State: "all"}); !errors.Is(err, ErrInvalidQuery) {
		t.Fatalf("mismatched filter error = %v", err)
	}
	if _, err := decodeCursor(cursor, "categories", filter); !errors.Is(err, ErrInvalidQuery) {
		t.Fatalf("mismatched domain error = %v", err)
	}
}

func TestCursorRejectsMalformedTamperedAndNonCanonicalValues(t *testing.T) {
	t.Parallel()

	filter := CategoryListFilter{Type: "expense", State: "active"}
	position := CursorPosition{
		CreatedAt: time.Date(2026, 7, 15, 0, 0, 0, 0, time.UTC),
		ID:        uuid.MustParse("123e4567-e89b-42d3-a456-426614174000"),
	}
	cursor, err := encodeCursor("categories", filter, position)
	if err != nil {
		t.Fatal(err)
	}

	decoded, err := base64.RawURLEncoding.DecodeString(cursor)
	if err != nil {
		t.Fatal(err)
	}
	var envelope map[string]any
	if err := json.Unmarshal(decoded, &envelope); err != nil {
		t.Fatal(err)
	}
	envelope["fingerprint"] = strings.Repeat("0", 64)
	tamperedJSON, err := json.Marshal(envelope)
	if err != nil {
		t.Fatal(err)
	}
	tampered := base64.RawURLEncoding.EncodeToString(tamperedJSON)
	unknown := base64.RawURLEncoding.EncodeToString(append(decoded[:len(decoded)-1], []byte(`,"unknown":true}`)...))
	trailing := base64.RawURLEncoding.EncodeToString(append(decoded, []byte(` {}`)...))
	duplicate := base64.RawURLEncoding.EncodeToString(append(decoded[:len(decoded)-1], []byte(`,"id":"123e4567-e89b-42d3-a456-426614174000"}`)...))
	nonCanonicalTime := strings.Replace(string(decoded), "2026-07-15T00:00:00Z", "2026-07-15T00:00:00+00:00", 1)
	nonCanonicalUUID := strings.Replace(string(decoded), "123e4567-e89b-42d3-a456-426614174000", "123E4567-E89B-42D3-A456-426614174000", 1)

	for _, value := range []string{
		"", "%%%", cursor + "=", strings.Repeat("a", MaximumCursorBytes+1), tampered,
		unknown, trailing, duplicate,
		base64.RawURLEncoding.EncodeToString([]byte(nonCanonicalTime)),
		base64.RawURLEncoding.EncodeToString([]byte(nonCanonicalUUID)),
	} {
		if _, err := decodeCursor(value, "categories", filter); !errors.Is(err, ErrInvalidQuery) {
			t.Errorf("decodeCursor(%q) error = %v; want ErrInvalidQuery", value, err)
		}
	}
}

func TestFilterFingerprintsIncludeEveryNormalizedFilter(t *testing.T) {
	t.Parallel()

	from, _ := parseLocalDate("2026-07-01")
	to, _ := parseLocalDate("2026-07-31")
	accountID := uuid.MustParse("123e4567-e89b-42d3-a456-426614174000")
	categoryID := uuid.MustParse("123e4567-e89b-42d3-a456-426614174001")
	base := TransactionListFilter{From: &from, To: &to, State: "active"}
	baseFingerprint := filterFingerprint("transactions", base)

	variants := []TransactionListFilter{
		{From: &from, To: &to, State: "all"},
		{From: &from, To: &to, State: "active", Type: "expense"},
		{From: &from, To: &to, State: "active", AccountID: &accountID},
		{From: &from, To: &to, State: "active", CategoryID: &categoryID},
		{From: &from, State: "active"},
		{To: &to, State: "active"},
	}
	for index, variant := range variants {
		if filterFingerprint("transactions", variant) == baseFingerprint {
			t.Errorf("variant %d did not change filter fingerprint", index)
		}
	}
	if filterFingerprint("accounts", AccountListFilter{State: "active"}) ==
		filterFingerprint("categories", CategoryListFilter{Type: "expense", State: "active"}) {
		t.Fatal("different cursor domains shared a fingerprint")
	}
	if filterFingerprint("accounts", AccountListFilter{State: "active"}) ==
		filterFingerprint("accounts", AccountListFilter{State: "all"}) {
		t.Fatal("account state did not change fingerprint")
	}
	if filterFingerprint("categories", CategoryListFilter{Type: "expense", State: "active"}) ==
		filterFingerprint("categories", CategoryListFilter{Type: "income", State: "active"}) ||
		filterFingerprint("categories", CategoryListFilter{Type: "expense", State: "active"}) ==
			filterFingerprint("categories", CategoryListFilter{Type: "expense", State: "archived"}) {
		t.Fatal("category type/state did not change fingerprint")
	}
	if filterFingerprint("account-balances", AccountBalanceFilter{To: to}) ==
		filterFingerprint("account-balances", AccountBalanceFilter{To: from}) {
		t.Fatal("account balance date did not change fingerprint")
	}
	if filterFingerprint("category-expenses", CategoryExpenseFilter{From: from, To: to}) ==
		filterFingerprint("category-expenses", CategoryExpenseFilter{From: to, To: to}) ||
		filterFingerprint("category-expenses", CategoryExpenseFilter{From: from, To: to}) ==
			filterFingerprint("category-expenses", CategoryExpenseFilter{From: from, To: from}) {
		t.Fatal("category expense range did not change fingerprint")
	}
}
