package finance

import (
	"errors"
	"math"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestParsePositiveCents(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		name  string
		value string
		want  int64
		valid bool
	}{
		{name: "one", value: "1", want: 1, valid: true},
		{name: "maximum", value: "9000000000000000", want: MaximumMoneyCents, valid: true},
		{name: "empty", value: ""},
		{name: "zero", value: "0"},
		{name: "leading zero", value: "01"},
		{name: "negative", value: "-1"},
		{name: "positive sign", value: "+1"},
		{name: "decimal", value: "1.0"},
		{name: "exponent", value: "1e3"},
		{name: "over domain maximum", value: "9000000000000001"},
		{name: "over int64", value: "9223372036854775808"},
	} {
		t.Run(test.name, func(t *testing.T) {
			got, err := parsePositiveCents(test.value)
			if test.valid {
				if err != nil || got != test.want {
					t.Fatalf("parsePositiveCents(%q) = %d, %v; want %d", test.value, got, err, test.want)
				}
				return
			}
			if !errors.Is(err, ErrValidation) {
				t.Fatalf("parsePositiveCents(%q) error = %v; want ErrValidation", test.value, err)
			}
		})
	}
}

func TestParseSignedCents(t *testing.T) {
	t.Parallel()

	for _, value := range []string{"0", "1", "-1", "9223372036854775807", "-9223372036854775808"} {
		if _, err := parseSignedCents(value); err != nil {
			t.Errorf("parseSignedCents(%q): %v", value, err)
		}
	}
	for _, value := range []string{"", "-0", "+1", "01", "-01", "1.5", "1e3", "9223372036854775808"} {
		if _, err := parseSignedCents(value); !errors.Is(err, ErrAggregateOverflow) {
			t.Errorf("parseSignedCents(%q) error = %v; want ErrAggregateOverflow", value, err)
		}
	}
}

func TestParseVersion(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		value string
		want  int64
	}{
		{value: "1", want: 1},
		{value: "9223372036854775807", want: math.MaxInt64},
	} {
		got, err := parseVersion(test.value)
		if err != nil || got != test.want {
			t.Fatalf("parseVersion(%q) = %d, %v", test.value, got, err)
		}
	}
	for _, value := range []string{"", "0", "01", "-1", "+1", "1.0", "1e3", "9223372036854775808"} {
		if _, err := parseVersion(value); !errors.Is(err, ErrValidation) {
			t.Errorf("parseVersion(%q) error = %v; want ErrValidation", value, err)
		}
	}
}

func TestCanonicalUUIDAndLocalDate(t *testing.T) {
	t.Parallel()

	validUUID := "123e4567-e89b-42d3-a456-426614174000"
	if parsed, err := parseCanonicalUUID(validUUID); err != nil || parsed.String() != validUUID {
		t.Fatalf("parseCanonicalUUID valid = %v, %v", parsed, err)
	}
	for _, value := range []string{
		"", uuid.Nil.String(), "123E4567-E89B-42D3-A456-426614174000", "123e4567e89b42d3a456426614174000",
	} {
		if _, err := parseCanonicalUUID(value); !errors.Is(err, ErrValidation) {
			t.Errorf("parseCanonicalUUID(%q) error = %v", value, err)
		}
	}

	if date, err := parseLocalDate("2024-02-29"); err != nil || date.String() != "2024-02-29" {
		t.Fatalf("parseLocalDate leap date = %v, %v", date, err)
	}
	for _, value := range []string{"2023-02-29", "2024-2-29", "2024-02-29T00:00:00Z", " 2024-02-29"} {
		if _, err := parseLocalDate(value); !errors.Is(err, ErrValidation) {
			t.Errorf("parseLocalDate(%q) error = %v", value, err)
		}
	}
}

func TestTextColorAndUntrimmedKeys(t *testing.T) {
	t.Parallel()

	if got, err := normalizeRequiredText("  Семейный счёт  ", 120); err != nil || got != "Семейный счёт" {
		t.Fatalf("normalizeRequiredText = %q, %v", got, err)
	}
	if got, err := normalizeRequiredText("e\u0301", 120); err != nil || got != "é" {
		t.Fatalf("NFD text normalization = %q, %v", got, err)
	}
	invalidUTF8 := string([]byte{0xff, 'a'})
	if _, err := normalizeRequiredText(invalidUTF8, 120); !errors.Is(err, ErrValidation) {
		t.Fatalf("invalid UTF-8 required text error = %v", err)
	}
	if _, err := normalizeOptionalText(invalidUTF8, 120); !errors.Is(err, ErrValidation) {
		t.Fatalf("invalid UTF-8 optional text error = %v", err)
	}
	if _, err := normalizeRequiredText(strings.Repeat("я", 121), 120); !errors.Is(err, ErrValidation) {
		t.Fatalf("long Unicode text error = %v", err)
	}
	if got, err := normalizeColor(" #a1b2c3 "); err != nil || got != "#A1B2C3" {
		t.Fatalf("normalizeColor = %q, %v", got, err)
	}
	for _, value := range []string{"#FFFFF", "#GGGGGG", "112233"} {
		if _, err := normalizeColor(value); !errors.Is(err, ErrValidation) {
			t.Errorf("normalizeColor(%q) error = %v", value, err)
		}
	}
	for _, value := range []string{"", " key", "key ", strings.Repeat("к", 256)} {
		if err := validateIdempotencyKey(value); !errors.Is(err, ErrValidation) {
			t.Errorf("validateIdempotencyKey(%q) error = %v", value, err)
		}
		if err := validateRequestID(value); !errors.Is(err, ErrValidation) {
			t.Errorf("validateRequestID(%q) error = %v", value, err)
		}
	}
	if err := validateIdempotencyKey("logical-create-1"); err != nil {
		t.Fatalf("valid idempotency key: %v", err)
	}
}

func TestNormalizeAccountAndCategoryInputs(t *testing.T) {
	t.Parallel()

	owner := "123e4567-e89b-42d3-a456-426614174000"
	account, err := normalizeAccountCreate(CreateAccountInput{
		Name: " Main ", Color: "#abcdef", OwnerUserID: &owner,
	})
	if err != nil {
		t.Fatalf("normalizeAccountCreate: %v", err)
	}
	if account.Name != "Main" || account.Color != "#ABCDEF" || account.AccountType != "regular" ||
		account.OwnerUserID == nil || account.OwnerUserID.String() != owner {
		t.Fatalf("unexpected normalized account: %#v", account)
	}
	if _, err := normalizeAccountCreate(CreateAccountInput{Name: "A", Color: "#112233", SortOrder: 1_000_001}); !errors.Is(err, ErrValidation) {
		t.Fatalf("account sort boundary error = %v", err)
	}
	category, err := normalizeCategoryCreate(CreateCategoryInput{
		Type: "expense", Name: " Food ", Color: "#abcdef", SortOrder: 1_000_000,
	})
	if err != nil || category.Name != "Food" || category.Color != "#ABCDEF" {
		t.Fatalf("normalizeCategoryCreate = %#v, %v", category, err)
	}
}

func TestPatchPresenceAndMergedTransactionShape(t *testing.T) {
	t.Parallel()

	if _, err := normalizeAccountPatch(AccountPatchInput{}); !errors.Is(err, ErrValidation) {
		t.Fatalf("empty account patch error = %v", err)
	}
	if _, err := normalizeCategoryPatch(CategoryPatchInput{}); !errors.Is(err, ErrValidation) {
		t.Fatalf("empty category patch error = %v", err)
	}
	if _, err := normalizeTransactionPatch(TransactionPatchInput{}); !errors.Is(err, ErrValidation) {
		t.Fatalf("empty transaction patch error = %v", err)
	}

	accountID := uuid.MustParse("123e4567-e89b-42d3-a456-426614174000")
	categoryID := uuid.MustParse("123e4567-e89b-42d3-a456-426614174001")
	current := TransactionValues{
		Type: "expense", AccountID: accountID, CategoryID: &categoryID, AmountCents: 100,
		EventDate: LocalDate{Time: time.Date(2026, 7, 15, 0, 0, 0, 0, time.UTC)},
	}
	patch, err := normalizeTransactionPatch(TransactionPatchInput{
		Type: Field[string]{Present: true, Value: "transfer"},
	})
	if err != nil {
		t.Fatalf("normalize partial transaction patch: %v", err)
	}
	if _, err := mergeTransactionPatch(current, patch); !errors.Is(err, ErrValidation) {
		t.Fatalf("invalid merged transfer error = %v", err)
	}

	nullOwner := NullableField[string]{Present: true, Value: nil}
	accountPatch, err := normalizeAccountPatch(AccountPatchInput{OwnerUserID: nullOwner})
	if err != nil || !accountPatch.OwnerUserID.Present || accountPatch.OwnerUserID.Value != nil {
		t.Fatalf("explicit null owner patch = %#v, %v", accountPatch, err)
	}
}

func TestTransactionShapes(t *testing.T) {
	t.Parallel()

	accountID := "123e4567-e89b-42d3-a456-426614174000"
	categoryID := "123e4567-e89b-42d3-a456-426614174001"
	toAccountID := "123e4567-e89b-42d3-a456-426614174002"
	base := CreateTransactionInput{
		Type: "expense", AccountID: accountID, CategoryID: &categoryID,
		AmountCents: "100", EventDate: "2026-07-15",
	}
	if _, err := normalizeTransactionCreate(base); err != nil {
		t.Fatalf("valid expense: %v", err)
	}
	transfer := base
	transfer.Type = "transfer"
	transfer.CategoryID = nil
	transfer.ToAccountID = &toAccountID
	if _, err := normalizeTransactionCreate(transfer); err != nil {
		t.Fatalf("valid transfer: %v", err)
	}
	transfer.IsBalanceAdjustment = true
	if _, err := normalizeTransactionCreate(transfer); !errors.Is(err, ErrValidation) {
		t.Fatalf("transfer adjustment error = %v", err)
	}
	transfer.IsBalanceAdjustment = false
	transfer.ToAccountID = &accountID
	if _, err := normalizeTransactionCreate(transfer); !errors.Is(err, ErrValidation) {
		t.Fatalf("same-account transfer error = %v", err)
	}
}

func TestListAndDateRangeBoundaries(t *testing.T) {
	t.Parallel()

	filter, limit, err := normalizeAccountList(AccountListInput{})
	if err != nil || filter.State != "active" || limit != DefaultListLimit {
		t.Fatalf("default account list = %#v, %d, %v", filter, limit, err)
	}
	if _, _, err := normalizeAccountList(AccountListInput{State: "deleted"}); !errors.Is(err, ErrInvalidQuery) {
		t.Fatalf("account deleted state error = %v", err)
	}
	if _, _, err := normalizeTransactionList(TransactionListInput{Limit: 201}); !errors.Is(err, ErrInvalidQuery) {
		t.Fatalf("transaction limit error = %v", err)
	}
	if _, err := normalizeSummaryRange(SummaryRangeInput{From: "2024-01-01", To: "2024-12-31"}); err != nil {
		t.Fatalf("366-day leap range: %v", err)
	}
	if _, err := normalizeSummaryRange(SummaryRangeInput{From: "2024-01-01", To: "2025-01-01"}); !errors.Is(err, ErrInvalidQuery) {
		t.Fatalf("367-day range error = %v", err)
	}
	if _, err := normalizeSummaryRange(SummaryRangeInput{From: "2025-01-02", To: "2025-01-01"}); !errors.Is(err, ErrInvalidQuery) {
		t.Fatalf("reverse range error = %v", err)
	}
	if _, err := normalizeSummaryRange(SummaryRangeInput{From: "bad", To: "2025-01-01"}); !errors.Is(err, ErrInvalidQuery) {
		t.Fatalf("malformed date error = %v", err)
	}
}

func TestQueryDateRangeErrorsAreInvalidQuery(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		name string
		from string
		to   string
	}{
		{name: "reverse", from: "2026-07-16", to: "2026-07-15"},
		{name: "over 366 days", from: "2024-01-01", to: "2025-01-01"},
	} {
		t.Run(test.name, func(t *testing.T) {
			if _, _, err := normalizeTransactionList(TransactionListInput{From: test.from, To: test.to}); !errors.Is(err, ErrInvalidQuery) {
				t.Errorf("transaction range error = %v", err)
			}
			if _, err := normalizeSummaryRange(SummaryRangeInput{From: test.from, To: test.to}); !errors.Is(err, ErrInvalidQuery) {
				t.Errorf("summary range error = %v", err)
			}
			if _, _, err := normalizeCategoryExpenseList(CategoryExpenseListInput{From: test.from, To: test.to}); !errors.Is(err, ErrInvalidQuery) {
				t.Errorf("category expense range error = %v", err)
			}
		})
	}
}
