package backupv5

import (
	"context"
	"errors"
	"strings"
	"testing"
)

func TestEntityCapsAreEnforcedDuringTokenScan(t *testing.T) {
	tests := []struct {
		field string
		limit int
	}{
		{field: "accounts", limit: MaxAccounts},
		{field: "categories", limit: MaxCategories},
		{field: "transactions", limit: MaxTransactions},
		{field: "goals", limit: MaxGoals},
		{field: "debts", limit: MaxDebts},
		{field: "debtPayments", limit: MaxDebtPayments},
	}
	for _, test := range tests {
		t.Run(test.field+" boundary", func(t *testing.T) {
			atLimit := compactArrays(map[string]int{test.field: test.limit})
			if err := validateRawJSON(context.Background(), atLimit); err != nil {
				t.Fatalf("token scan rejected count at limit: %v", err)
			}
			overLimit := compactArrays(map[string]int{test.field: test.limit + 1})
			_, err := ParseBytes(context.Background(), overLimit, fixtureInput)
			assertValidationError(t, err, ErrLimit, "entity_count_exceeded")
		})
	}

	exactTotal := compactArrays(map[string]int{
		"transactions": MaxTransactions,
		"debtPayments": MaxRawEntities - MaxTransactions,
	})
	if err := validateRawJSON(context.Background(), exactTotal); err != nil {
		t.Fatalf("token scan rejected raw total at limit: %v", err)
	}
	overTotal := compactArrays(map[string]int{
		"transactions": MaxTransactions,
		"debtPayments": MaxRawEntities - MaxTransactions + 1,
	})
	_, err := ParseBytes(context.Background(), overTotal, fixtureInput)
	assertValidationError(t, err, ErrLimit, "total_entity_count_exceeded")
}

func TestEntityCountHelperBoundaries(t *testing.T) {
	if err := validateEntityCounts(MaxAccounts, MaxCategories, MaxTransactions, MaxGoals, MaxDebts, 30_000); err != nil {
		t.Fatalf("valid total boundary rejected: %v", err)
	}
	tests := []struct {
		name   string
		values [6]int
		code   string
	}{
		{name: "accounts", values: [6]int{MaxAccounts + 1}, code: "entity_count_exceeded"},
		{name: "categories", values: [6]int{0, MaxCategories + 1}, code: "entity_count_exceeded"},
		{name: "transactions", values: [6]int{0, 0, MaxTransactions + 1}, code: "entity_count_exceeded"},
		{name: "goals", values: [6]int{0, 0, 0, MaxGoals + 1}, code: "entity_count_exceeded"},
		{name: "debts", values: [6]int{0, 0, 0, 0, MaxDebts + 1}, code: "entity_count_exceeded"},
		{name: "payments", values: [6]int{0, 0, 0, 0, 0, MaxDebtPayments + 1}, code: "entity_count_exceeded"},
		{name: "negative", values: [6]int{-1}, code: "entity_count_exceeded"},
		{name: "total", values: [6]int{MaxAccounts, MaxCategories, MaxTransactions, MaxGoals, MaxDebts, 30_001}, code: "total_entity_count_exceeded"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := validateEntityCounts(test.values[0], test.values[1], test.values[2], test.values[3], test.values[4], test.values[5])
			assertValidationError(t, err, ErrLimit, test.code)
		})
	}
}

func TestWrongJSONTypesStaySchemaErrors(t *testing.T) {
	tests := []struct {
		name string
		from string
		to   string
		path string
	}{
		{name: "account name", from: `"name":" Main account "`, to: `"name":7`, path: "accounts[0].name"},
		{name: "account sort order", from: `"sortOrder":0`, to: `"sortOrder":"0"`, path: "accounts[0].sortOrder"},
		{name: "account system", from: `"system":true`, to: `"system":0`, path: "accounts[0].system"},
		{name: "account kind", from: `"kind":"regular"`, to: `"kind":7`, path: "accounts[0].kind"},
		{name: "category type", from: `"type":"income"`, to: `"type":7`, path: "categories[0].type"},
		{name: "category budget", from: `"budgetCents":0`, to: `"budgetCents":"0"`, path: "categories[0].budgetCents"},
		{name: "transaction type", from: `"type":"income","accountId"`, to: `"type":7,"accountId"`, path: "transactions[0].type"},
		{name: "transaction account", from: `"accountId":"main"`, to: `"accountId":7`, path: "transactions[0].accountId"},
		{name: "transaction destination", from: `"toAccountId":"reserve"`, to: `"toAccountId":true`, path: "transactions[2].toAccountId"},
		{name: "transaction category", from: `"categoryId":"salary"`, to: `"categoryId":true`, path: "transactions[0].categoryId"},
		{name: "transaction amount", from: `"amountCents":1000`, to: `"amountCents":"1000"`, path: "transactions[0].amountCents"},
		{name: "goal archived", from: `"archived":true,"createdAt":"2026-02`, to: `"archived":0,"createdAt":"2026-02`, path: "goals[0].archived"},
		{name: "debt direction", from: `"direction":"i_owe"`, to: `"direction":7`, path: "debts[0].direction"},
		{name: "payment debt reference", from: `"debtId":"debt-main"`, to: `"debtId":7`, path: "debtPayments[0].debtId"},
		{name: "payment date", from: `"date":"2026-07-05"`, to: `"date":false`, path: "debtPayments[0].date"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			raw := strings.Replace(canonicalFixture, test.from, test.to, 1)
			if raw == canonicalFixture {
				t.Fatalf("test replacement did not match")
			}
			_, err := ParseBytes(context.Background(), []byte(raw), fixtureInput)
			if !errors.Is(err, ErrSchema) {
				t.Fatalf("error = %v, want ErrSchema", err)
			}
			var validation *ValidationError
			if !errors.As(err, &validation) || validation.Path != test.path {
				t.Fatalf("error = %#v, want safe path %s", err, test.path)
			}
		})
	}
}

func compactArrays(counts map[string]int) []byte {
	fields := []string{"accounts", "categories", "transactions", "goals", "debts", "debtPayments"}
	var output strings.Builder
	output.WriteByte('{')
	for fieldIndex, field := range fields {
		if fieldIndex > 0 {
			output.WriteByte(',')
		}
		output.WriteByte('"')
		output.WriteString(field)
		output.WriteString(`":[`)
		for index := 0; index < counts[field]; index++ {
			if index > 0 {
				output.WriteByte(',')
			}
			output.WriteString(`{}`)
		}
		output.WriteByte(']')
	}
	output.WriteByte('}')
	return []byte(output.String())
}
