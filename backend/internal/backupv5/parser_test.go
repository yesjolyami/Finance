package backupv5

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
)

var fixtureInput = Input{
	HouseholdID: uuid.MustParse("11111111-1111-4111-8111-111111111111"),
	BudgetMonth: "2026-07-01",
}

const canonicalFixture = `{
  "version": 5,
  "exportedAt": "2026-07-15T12:00:00+03:00",
  "accounts": [
    {"id":"main","name":" Main account ","color":"#1a2b3c","sortOrder":0,"system":true,"kind":"regular","bank":"Test bank","owner":"Legacy label","createdAt":"2026-01-01T10:00:00Z"},
    {"id":"reserve","name":"Reserve","color":"#445566","sortOrder":1,"system":false,"kind":"savings","bank":"","owner":"","createdAt":"2026-01-02T10:00:00+03:00"}
  ],
  "categories": [
    {"id":"salary","type":"income","name":"Income","color":"#228833","budgetCents":0,"system":false,"createdAt":"2026-01-01T10:00:00Z"},
    {"id":"food","type":"expense","name":"Café","color":"#aa3344","budgetCents":300,"system":true,"createdAt":"2026-01-01T10:00:00Z"}
  ],
  "transactions": [
    {"id":"tx-income","type":"income","accountId":"main","toAccountId":null,"categoryId":"salary","amountCents":1000,"date":"2026-07-01","note":" Salary ","isBalanceAdjustment":false,"createdAt":"2026-07-01T08:00:00Z"},
    {"id":"tx-expense","type":"expense","accountId":"main","toAccountId":null,"categoryId":"food","amountCents":400,"date":"2026-07-02","note":"Groceries","isBalanceAdjustment":false,"createdAt":"2026-07-02T08:00:00Z"},
    {"id":"tx-transfer","type":"transfer","accountId":"main","toAccountId":"reserve","categoryId":null,"amountCents":200,"date":"2026-07-03","note":"Move","isBalanceAdjustment":false,"createdAt":"2026-07-03T08:00:00Z"},
    {"id":"tx-adjust","type":"income","accountId":"main","toAccountId":null,"categoryId":"salary","amountCents":50,"date":"2026-07-04","note":"Adjustment","isBalanceAdjustment":true,"createdAt":"2026-07-04T08:00:00Z"}
  ],
  "goals": [
    {"id":"goal-main","name":"Goal","targetCents":100,"savedCents":150,"targetDate":"","color":"#556677","archived":true,"createdAt":"2026-02-01T08:00:00Z"}
  ],
  "debts": [
    {"id":"debt-main","person":"Counterparty","direction":"i_owe","amountCents":1000,"paidCents":1200,"leftCents":0,"dueDate":"2026-12-31","note":"Debt note","archived":true,"createdAt":"2026-03-01T08:00:00Z"}
  ],
  "debtPayments": [
    {"id":"payment-1","debtId":"debt-main","amountCents":700,"date":"2026-07-05","note":"First","createdAt":"2026-07-05T08:00:00Z"},
    {"id":"payment-2","debtId":"debt-main","amountCents":500,"date":"2026-07-06","note":"Second","createdAt":"2026-07-06T08:00:00Z"}
  ]
}`

func TestParseCanonicalBackup(t *testing.T) {
	model, err := ParseBytes(context.Background(), []byte(canonicalFixture), fixtureInput)
	if err != nil {
		t.Fatalf("ParseBytes() error = %v", err)
	}
	if model.ExportedAt.Format("2006-01-02T15:04:05Z07:00") != "2026-07-15T09:00:00Z" {
		t.Fatalf("ExportedAt = %s", model.ExportedAt)
	}
	if model.BackupDigest != sha256.Sum256([]byte(canonicalFixture)) {
		t.Fatalf("BackupDigest does not cover exact raw bytes")
	}
	if model.Accounts[0].ID.String() != "b48f4451-9067-5540-b3ec-fd8eeeb9e19c" {
		t.Fatalf("account fixture UUID = %s", model.Accounts[0].ID)
	}
	if model.Budgets[0].ID.String() != "caff427e-f755-56cf-897b-353379622370" {
		t.Fatalf("budget fixture UUID = %s", model.Budgets[0].ID)
	}
	if model.Accounts[0].Name != "Main account" || model.Accounts[0].Color != "#1A2B3C" {
		t.Fatalf("account normalization = %#v", model.Accounts[0])
	}
	if model.Categories[1].Name != "Café" || model.Categories[1].Color != "#AA3344" {
		t.Fatalf("category normalization = %#v", model.Categories[1])
	}
	if got, want := model.Counts, (Counts{Accounts: 2, Categories: 2, Transactions: 4, Budgets: 1, Goals: 1, Debts: 1, DebtPayments: 2}); got != want {
		t.Fatalf("Counts = %#v, want %#v", got, want)
	}
	if got, want := model.Totals, (Totals{
		IncomeCents: 1050, ExpenseCents: 400, TransferCents: 200,
		HouseholdBalanceCents: 650, CashFlowIncomeCents: 1000,
		CashFlowExpenseCents: 400, BudgetCents: 300,
	}); got != want {
		t.Fatalf("Totals = %#v, want %#v", got, want)
	}
	if got, want := model.Warnings, (WarningCounts{
		LegacyOwnerNotLinked: 1, ArchiveTimeApproximated: 2, GoalExceedsTarget: 1,
		DebtOverpaid: 1, SystemResourcePreserved: 2, BudgetMonthExplicitChoice: 1,
	}); got != want {
		t.Fatalf("Warnings = %#v, want %#v", got, want)
	}
	mainID := model.Accounts[0].ID
	reserveID := model.Accounts[1].ID
	if model.Reconciliation.AccountBalances[mainID] != 450 || model.Reconciliation.AccountBalances[reserveID] != 200 {
		t.Fatalf("AccountBalances = %#v", model.Reconciliation.AccountBalances)
	}
	month := model.Reconciliation.Monthly["2026-07"]
	if month.IncomeCount != 2 || month.ExpenseCount != 1 || month.TransferCount != 1 ||
		month.CashFlowIncomeCents != 1000 || month.CashFlowExpenseCents != 400 {
		t.Fatalf("Monthly totals = %#v", month)
	}
	if len(model.Reconciliation.Debts) != 1 || model.Reconciliation.Debts[0].PaidCents != 1200 || model.Reconciliation.Debts[0].LeftCents != 0 {
		t.Fatalf("Debt reconciliation = %#v", model.Reconciliation.Debts)
	}
	if model.Transactions[0].IdempotencyKey != "backup-v5:"+model.Transactions[0].ID.String() || model.Transactions[0].PayloadHash == ([32]byte{}) {
		t.Fatalf("transaction idempotency metadata is incomplete")
	}
	if model.Reconciliation.AccountBalances == nil || model.Reconciliation.Monthly == nil || model.Accounts == nil || model.Goals == nil {
		t.Fatalf("normalized collections must be non-nil")
	}
}

func TestDeterministicRepeatAndUUIDFraming(t *testing.T) {
	first, err := ParseBytes(context.Background(), []byte(canonicalFixture), fixtureInput)
	if err != nil {
		t.Fatal(err)
	}
	second, err := ParseReader(context.Background(), strings.NewReader(canonicalFixture), fixtureInput)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(first, second) {
		t.Fatalf("repeat parse diverged")
	}
	withWhitespace, err := ParseBytes(context.Background(), []byte(canonicalFixture+"\n"), fixtureInput)
	if err != nil {
		t.Fatal(err)
	}
	if withWhitespace.BackupDigest == first.BackupDigest {
		t.Fatalf("raw-byte digest ignored trailing JSON whitespace")
	}
	otherHousehold := uuid.MustParse("22222222-2222-4222-8222-222222222222")
	if deterministicID(otherHousehold, "account", "main") == first.Accounts[0].ID {
		t.Fatalf("household must participate in UUID framing")
	}
	if deterministicID(fixtureInput.HouseholdID, "category", "main") == first.Accounts[0].ID {
		t.Fatalf("entity type must participate in UUID framing")
	}
	if deterministicID(fixtureInput.HouseholdID, "account", "é") == deterministicID(fixtureInput.HouseholdID, "account", "é") {
		t.Fatalf("legacy IDs must not be Unicode-normalized")
	}
}

func TestStrictRawJSON(t *testing.T) {
	deep := strings.Repeat("[", MaxJSONDepth+1) + "0" + strings.Repeat("]", MaxJSONDepth+1)
	cases := []struct {
		name string
		raw  []byte
		kind error
		code string
	}{
		{name: "empty", raw: nil, kind: ErrInvalidJSON, code: "empty_body"},
		{name: "invalid utf8", raw: []byte{'{', '"', 'x', '"', ':', '"', 0xff, '"', '}'}, kind: ErrInvalidJSON, code: "invalid_utf8"},
		{name: "bom", raw: append([]byte{0xef, 0xbb, 0xbf}, []byte(canonicalFixture)...), kind: ErrInvalidJSON, code: "utf8_bom_forbidden"},
		{name: "malformed", raw: []byte(`{"version":`), kind: ErrInvalidJSON, code: "malformed_json"},
		{name: "duplicate root", raw: []byte(`{"version":5,"version":5}`), kind: ErrInvalidJSON, code: "duplicate_key"},
		{name: "duplicate nested", raw: []byte(`{"x":{"id":"a","id":"b"}}`), kind: ErrInvalidJSON, code: "duplicate_key"},
		{name: "second root", raw: []byte(`{} {}`), kind: ErrInvalidJSON, code: "trailing_value"},
		{name: "trailing bytes", raw: []byte(`{} nope`), kind: ErrInvalidJSON, code: "trailing_bytes"},
		{name: "fraction", raw: []byte(`{"version":5.0}`), kind: ErrValue, code: "non_integer_number"},
		{name: "exponent", raw: []byte(`{"version":5e0}`), kind: ErrValue, code: "non_integer_number"},
		{name: "negative zero", raw: []byte(`{"version":-0}`), kind: ErrValue, code: "non_integer_number"},
		{name: "depth", raw: []byte(deep), kind: ErrLimit, code: "json_depth_exceeded"},
		{name: "string bytes", raw: []byte(`{"x":"` + strings.Repeat("x", MaxRawStringBytes+1) + `"}`), kind: ErrLimit, code: "json_string_too_long"},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			_, err := ParseBytes(context.Background(), test.raw, fixtureInput)
			assertValidationError(t, err, test.kind, test.code)
		})
	}

	boundaryDepth := strings.Repeat("[", MaxJSONDepth) + "0" + strings.Repeat("]", MaxJSONDepth)
	if err := validateRawJSON(context.Background(), []byte(boundaryDepth)); err != nil {
		t.Fatalf("depth at boundary rejected: %v", err)
	}
	if err := validateRawJSON(context.Background(), []byte(`{"x":"`+strings.Repeat("x", MaxRawStringBytes)+`"}`)); err != nil {
		t.Fatalf("string at boundary rejected: %v", err)
	}
}

func TestStrictSchema(t *testing.T) {
	tests := []struct {
		name string
		raw  string
		kind error
		code string
	}{
		{name: "root object", raw: `[]`, kind: ErrSchema, code: "object_required"},
		{name: "version string", raw: strings.Replace(canonicalFixture, `"version": 5`, `"version": "5"`, 1), kind: ErrSchema, code: "integer_required"},
		{name: "version four", raw: strings.Replace(canonicalFixture, `"version": 5`, `"version": 4`, 1), kind: ErrSchema, code: "unsupported_version"},
		{name: "version six", raw: strings.Replace(canonicalFixture, `"version": 5`, `"version": 6`, 1), kind: ErrSchema, code: "unsupported_version"},
		{name: "missing array", raw: strings.Replace(canonicalFixture, `  "goals": [
    {"id":"goal-main","name":"Goal","targetCents":100,"savedCents":150,"targetDate":"","color":"#556677","archived":true,"createdAt":"2026-02-01T08:00:00Z"}
  ],
`, "", 1), kind: ErrSchema, code: "required_field_missing"},
		{name: "array null", raw: strings.Replace(canonicalFixture, `"goals": [
    {"id":"goal-main","name":"Goal","targetCents":100,"savedCents":150,"targetDate":"","color":"#556677","archived":true,"createdAt":"2026-02-01T08:00:00Z"}
  ]`, `"goals": null`, 1), kind: ErrSchema, code: "array_required"},
		{name: "unknown root", raw: strings.Replace(canonicalFixture, `"version": 5,`, `"version": 5,"extra":true,`, 1), kind: ErrSchema, code: "unknown_field"},
		{name: "unknown entity", raw: strings.Replace(canonicalFixture, `"id":"main","name"`, `"id":"main","extra":true,"name"`, 1), kind: ErrSchema, code: "unknown_field"},
		{name: "wrong boolean type", raw: strings.Replace(canonicalFixture, `"system":true`, `"system":"true"`, 1), kind: ErrSchema, code: "boolean_required"},
		{name: "empty categories domain invalid", raw: minimalBackup(`[]`), kind: ErrValue, code: "income_and_expense_categories_required"},
		{name: "single category domain invalid", raw: minimalBackup(`[{
          "id":"only","type":"income","name":"Only","color":"#112233",
          "budgetCents":0,"system":false,"createdAt":"2026-01-01T00:00:00Z"
		}]`), kind: ErrValue, code: "income_and_expense_categories_required"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := ParseBytes(context.Background(), []byte(test.raw), fixtureInput)
			assertValidationError(t, err, test.kind, test.code)
		})
	}
}

func TestDomainValidationAndReferences(t *testing.T) {
	longID := strings.Repeat("i", MaxLegacyIDBytes+1)
	longName := strings.Repeat("я", 121)
	longNote := strings.Repeat("🙂", 1001)
	duplicateCategory := `,{"id":"food-2","type":"expense","name":"CAFÉ","color":"#123456","budgetCents":0,"system":false,"createdAt":"2026-01-01T10:00:00Z"}`
	tests := []struct {
		name string
		raw  string
		kind error
		code string
	}{
		{name: "legacy id outer whitespace", raw: strings.Replace(canonicalFixture, `"id":"main"`, `"id":" main"`, 1), kind: ErrValue, code: "invalid_legacy_id"},
		{name: "legacy id control", raw: strings.Replace(canonicalFixture, `"id":"main"`, `"id":"main\u0001"`, 1), kind: ErrValue, code: "invalid_legacy_id"},
		{name: "legacy id bytes", raw: strings.Replace(canonicalFixture, `"id":"main"`, `"id":"`+longID+`"`, 1), kind: ErrValue, code: "invalid_legacy_id"},
		{name: "duplicate account id", raw: strings.Replace(canonicalFixture, `"id":"reserve"`, `"id":"main"`, 1), kind: ErrValue, code: "duplicate_legacy_id"},
		{name: "name runes", raw: strings.Replace(canonicalFixture, `"name":" Main account "`, `"name":"`+longName+`"`, 1), kind: ErrValue, code: "invalid_text"},
		{name: "database forbidden nul text", raw: strings.Replace(canonicalFixture, `"name":" Main account "`, `"name":"Main\u0000account"`, 1), kind: ErrValue, code: "invalid_text"},
		{name: "note runes", raw: strings.Replace(canonicalFixture, `"note":" Salary "`, `"note":"`+longNote+`"`, 1), kind: ErrValue, code: "invalid_text"},
		{name: "color", raw: strings.Replace(canonicalFixture, `"color":"#1a2b3c"`, `"color":"red"`, 1), kind: ErrValue, code: "invalid_color"},
		{name: "sort negative", raw: strings.Replace(canonicalFixture, `"sortOrder":0`, `"sortOrder":-1`, 1), kind: ErrValue, code: "integer_out_of_range"},
		{name: "sort overflow", raw: strings.Replace(canonicalFixture, `"sortOrder":0`, `"sortOrder":2147483648`, 1), kind: ErrValue, code: "integer_out_of_range"},
		{name: "account kind", raw: strings.Replace(canonicalFixture, `"kind":"regular"`, `"kind":"cash"`, 1), kind: ErrValue, code: "invalid_account_kind"},
		{name: "income budget", raw: strings.Replace(canonicalFixture, `"budgetCents":0`, `"budgetCents":1`, 1), kind: ErrValue, code: "income_budget_must_be_zero"},
		{name: "money zero", raw: strings.Replace(canonicalFixture, `"amountCents":1000`, `"amountCents":0`, 1), kind: ErrValue, code: "integer_out_of_range"},
		{name: "money maximum plus one", raw: strings.Replace(canonicalFixture, `"amountCents":1000`, `"amountCents":9000000000000001`, 1), kind: ErrValue, code: "integer_out_of_range"},
		{name: "invalid local date", raw: strings.Replace(canonicalFixture, `"date":"2026-07-01"`, `"date":"2026-02-30"`, 1), kind: ErrValue, code: "invalid_date"},
		{name: "year zero local date", raw: strings.Replace(canonicalFixture, `"date":"2026-07-01"`, `"date":"0000-01-01"`, 1), kind: ErrValue, code: "invalid_date"},
		{name: "naive timestamp", raw: strings.Replace(canonicalFixture, `"createdAt":"2026-01-01T10:00:00Z"`, `"createdAt":"2026-01-01T10:00:00"`, 1), kind: ErrValue, code: "invalid_timestamp"},
		{name: "year zero timestamp", raw: strings.Replace(canonicalFixture, `"createdAt":"2026-01-01T10:00:00Z"`, `"createdAt":"0000-01-01T10:00:00Z"`, 1), kind: ErrValue, code: "invalid_timestamp"},
		{name: "unknown source account", raw: strings.Replace(canonicalFixture, `"accountId":"main"`, `"accountId":"missing"`, 1), kind: ErrReference, code: "unknown_account_reference"},
		{name: "unknown destination account", raw: strings.Replace(canonicalFixture, `"toAccountId":"reserve"`, `"toAccountId":"missing"`, 1), kind: ErrReference, code: "unknown_account_reference"},
		{name: "unknown category", raw: strings.Replace(canonicalFixture, `"categoryId":"salary"`, `"categoryId":"missing"`, 1), kind: ErrReference, code: "unknown_category_reference"},
		{name: "category type mismatch", raw: strings.Replace(canonicalFixture, `"categoryId":"food","amountCents":400`, `"categoryId":"salary","amountCents":400`, 1), kind: ErrReference, code: "category_type_mismatch"},
		{name: "transfer destination required", raw: strings.Replace(canonicalFixture, `"toAccountId":"reserve","categoryId":null`, `"toAccountId":null,"categoryId":null`, 1), kind: ErrValue, code: "invalid_transfer_shape"},
		{name: "transfer category forbidden", raw: strings.Replace(canonicalFixture, `"toAccountId":"reserve","categoryId":null`, `"toAccountId":"reserve","categoryId":"food"`, 1), kind: ErrValue, code: "invalid_transfer_shape"},
		{name: "transfer same account", raw: strings.Replace(canonicalFixture, `"toAccountId":"reserve","categoryId":null`, `"toAccountId":"main","categoryId":null`, 1), kind: ErrValue, code: "invalid_transfer_shape"},
		{name: "transfer adjustment", raw: strings.Replace(canonicalFixture, `"note":"Move","isBalanceAdjustment":false`, `"note":"Move","isBalanceAdjustment":true`, 1), kind: ErrValue, code: "transfer_balance_adjustment_forbidden"},
		{name: "unknown debt", raw: strings.Replace(canonicalFixture, `"debtId":"debt-main"`, `"debtId":"missing"`, 1), kind: ErrReference, code: "unknown_debt_reference"},
		{name: "unicode category collision", raw: strings.Replace(canonicalFixture, `
  ],
  "transactions": [`, duplicateCategory+`
  ],
  "transactions": [`, 1), kind: ErrValue, code: "duplicate_category_name"},
		{name: "invalid household", raw: canonicalFixture, kind: ErrValue, code: "invalid_household_id"},
		{name: "invalid budget month", raw: canonicalFixture, kind: ErrValue, code: "budget_month_not_first_day"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			input := fixtureInput
			if test.name == "invalid household" {
				input.HouseholdID = uuid.Nil
			}
			if test.name == "invalid budget month" {
				input.BudgetMonth = "2026-07-02"
			}
			_, err := ParseBytes(context.Background(), []byte(test.raw), input)
			assertValidationError(t, err, test.kind, test.code)
			if strings.Contains(fmt.Sprint(err), "missing") && test.name != "invalid household" {
				t.Fatalf("error leaked a field value: %v", err)
			}
		})
	}
}

func TestMoneyDateAndUnicodeBoundaries(t *testing.T) {
	maximum := strings.Replace(canonicalFixture, `"amountCents":1000`, `"amountCents":9000000000000000`, 1)
	maximum = strings.Replace(maximum, `"note":" Salary "`, `"note":"`+strings.Repeat("🙂", 1000)+`"`, 1)
	model, err := ParseBytes(context.Background(), []byte(maximum), fixtureInput)
	if err != nil {
		t.Fatalf("maximum boundaries rejected: %v", err)
	}
	if model.Transactions[0].AmountCents != MaximumMoneyCents || len([]rune(model.Transactions[0].Note)) != 1000 {
		t.Fatalf("maximum boundaries normalized incorrectly")
	}
	if model.Transactions[0].CreatedAt.Location().String() != "UTC" {
		t.Fatalf("timestamp must normalize to UTC")
	}
}

func TestDebtReconciliationMismatch(t *testing.T) {
	raw := strings.Replace(canonicalFixture, `"paidCents":1200`, `"paidCents":1199`, 1)
	_, err := ParseBytes(context.Background(), []byte(raw), fixtureInput)
	assertValidationError(t, err, ErrReconciliation, "debt_derived_values_mismatch")
}

func TestAggregateOverflow(t *testing.T) {
	raw := aggregateBackup(1025, MaximumMoneyCents)
	_, err := ParseBytes(context.Background(), raw, fixtureInput)
	assertValidationError(t, err, ErrReconciliation, "aggregate_overflow")
}

func TestHouseholdTotalDeterministicWithOpposingBalances(t *testing.T) {
	raw := opposingBalancesBackup()
	for iteration := 0; iteration < 12; iteration++ {
		model, err := ParseBytes(context.Background(), raw, fixtureInput)
		if err != nil {
			t.Fatalf("iteration %d: %v", iteration, err)
		}
		if model.Totals.HouseholdBalanceCents != 0 {
			t.Fatalf("iteration %d household balance = %d", iteration, model.Totals.HouseholdBalanceCents)
		}
	}
}

func TestCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := ParseBytes(ctx, []byte(canonicalFixture), fixtureInput)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("pre-cancel error = %v", err)
	}

	ctx, cancel = context.WithCancel(context.Background())
	reader := &cancelingReader{cancel: cancel, data: []byte(canonicalFixture)}
	_, err = ParseReader(ctx, reader, fixtureInput)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("reader cancellation error = %v", err)
	}

	loopContext := newCancelAfterContext(50)
	_, err = ParseBytes(loopContext, []byte(canonicalFixture), fixtureInput)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("bounded-loop cancellation error = %v", err)
	}

	_, err = ParseReader(context.Background(), invalidCountReader{}, fixtureInput)
	assertValidationError(t, err, ErrInvalidJSON, "read_failed")
}

func TestRawBodyLimit(t *testing.T) {
	if testing.Short() {
		t.Skip("32 MiB boundary test")
	}
	exact, err := readBounded(context.Background(), io.LimitReader(zeroReader{}, MaxRawBytes))
	if err != nil || int64(len(exact)) != MaxRawBytes {
		t.Fatalf("exact body limit = %d, %v", len(exact), err)
	}
	_, err = readBounded(context.Background(), io.LimitReader(zeroReader{}, MaxRawBytes+1))
	assertValidationError(t, err, ErrLimit, "raw_body_too_large")
}

func minimalBackup(categories string) string {
	return `{"version":5,"exportedAt":"2026-01-01T00:00:00Z","accounts":[],"categories":` + categories + `,"transactions":[],"goals":[],"debts":[],"debtPayments":[]}`
}

func aggregateBackup(count int, amount int64) []byte {
	var output strings.Builder
	output.WriteString(`{"version":5,"exportedAt":"2026-01-01T00:00:00Z","accounts":[{"id":"a","name":"A","color":"#112233","sortOrder":0,"system":false,"kind":"regular","bank":"","owner":"","createdAt":"2026-01-01T00:00:00Z"}],"categories":[{"id":"in","type":"income","name":"In","color":"#112233","budgetCents":0,"system":false,"createdAt":"2026-01-01T00:00:00Z"},{"id":"out","type":"expense","name":"Out","color":"#332211","budgetCents":0,"system":false,"createdAt":"2026-01-01T00:00:00Z"}],"transactions":[`)
	for index := 0; index < count; index++ {
		if index > 0 {
			output.WriteByte(',')
		}
		fmt.Fprintf(&output, `{"id":"t-%d","type":"income","accountId":"a","toAccountId":null,"categoryId":"in","amountCents":%d,"date":"2026-01-01","note":"","isBalanceAdjustment":false,"createdAt":"2026-01-01T00:00:00Z"}`, index, amount)
	}
	output.WriteString(`],"goals":[],"debts":[],"debtPayments":[]}`)
	return []byte(output.String())
}

func opposingBalancesBackup() []byte {
	var output strings.Builder
	output.WriteString(`{"version":5,"exportedAt":"2026-01-01T00:00:00Z","accounts":[`)
	for index := 0; index < 4; index++ {
		if index > 0 {
			output.WriteByte(',')
		}
		fmt.Fprintf(&output, `{"id":"a%d","name":"A%d","color":"#112233","sortOrder":%d,"system":false,"kind":"regular","bank":"","owner":"","createdAt":"2026-01-01T00:00:00Z"}`, index, index, index)
	}
	output.WriteString(`],"categories":[{"id":"in","type":"income","name":"In","color":"#112233","budgetCents":0,"system":false,"createdAt":"2026-01-01T00:00:00Z"},{"id":"out","type":"expense","name":"Out","color":"#332211","budgetCents":0,"system":false,"createdAt":"2026-01-01T00:00:00Z"}],"transactions":[`)
	transactionIndex := 0
	writeTransaction := func(transactionType, accountID, toID, categoryID string) {
		if transactionIndex > 0 {
			output.WriteByte(',')
		}
		toValue := "null"
		categoryValue := "null"
		if toID != "" {
			toValue = `"` + toID + `"`
		}
		if categoryID != "" {
			categoryValue = `"` + categoryID + `"`
		}
		fmt.Fprintf(&output, `{"id":"t-%d","type":"%s","accountId":"%s","toAccountId":%s,"categoryId":%s,"amountCents":9000000000000000,"date":"2026-01-01","note":"","isBalanceAdjustment":false,"createdAt":"2026-01-01T00:00:00Z"}`, transactionIndex, transactionType, accountID, toValue, categoryValue)
		transactionIndex++
	}
	for index := 0; index < 1000; index++ {
		writeTransaction("income", "a0", "", "in")
		writeTransaction("transfer", "a1", "a2", "")
		writeTransaction("expense", "a3", "", "out")
	}
	output.WriteString(`],"goals":[],"debts":[],"debtPayments":[]}`)
	return []byte(output.String())
}

func assertValidationError(t *testing.T, err, kind error, code string) {
	t.Helper()
	if err == nil {
		t.Fatalf("expected %s, got nil", code)
	}
	if !errors.Is(err, kind) {
		t.Fatalf("error %v does not match %v", err, kind)
	}
	var validation *ValidationError
	if !errors.As(err, &validation) || validation.Code != code {
		t.Fatalf("error = %#v, want code %s", err, code)
	}
}

type cancelingReader struct {
	cancel context.CancelFunc
	data   []byte
	done   bool
}

func (reader *cancelingReader) Read(destination []byte) (int, error) {
	if reader.done {
		return 0, io.EOF
	}
	reader.done = true
	read := copy(destination, reader.data)
	reader.cancel()
	return read, nil
}

type zeroReader struct{}

func (zeroReader) Read(destination []byte) (int, error) {
	for index := range destination {
		destination[index] = ' '
	}
	return len(destination), nil
}

type invalidCountReader struct{}

func (invalidCountReader) Read(destination []byte) (int, error) {
	return len(destination) + 1, nil
}

type cancelAfterContext struct {
	calls    int
	limit    int
	done     chan struct{}
	canceled bool
}

func newCancelAfterContext(limit int) *cancelAfterContext {
	return &cancelAfterContext{limit: limit, done: make(chan struct{})}
}

func (ctx *cancelAfterContext) Deadline() (time.Time, bool) { return time.Time{}, false }
func (ctx *cancelAfterContext) Done() <-chan struct{}       { return ctx.done }
func (ctx *cancelAfterContext) Value(any) any               { return nil }

func (ctx *cancelAfterContext) Err() error {
	ctx.calls++
	if ctx.calls < ctx.limit {
		return nil
	}
	if !ctx.canceled {
		ctx.canceled = true
		close(ctx.done)
	}
	return context.Canceled
}

func TestParseBytesDoesNotRetainRawInput(t *testing.T) {
	raw := []byte(canonicalFixture)
	model, err := ParseBytes(context.Background(), raw, fixtureInput)
	if err != nil {
		t.Fatal(err)
	}
	for index := range raw {
		raw[index] = ' '
	}
	if model.Accounts[0].Name != "Main account" || model.Transactions[0].Note != "Salary" {
		t.Fatalf("model retained mutable raw input")
	}
}
