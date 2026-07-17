package finance

import (
	"testing"
)

func TestTypedCreateFingerprintsNormalizeDefaultsAndNulls(t *testing.T) {
	t.Parallel()

	first, err := normalizeAccountCreate(CreateAccountInput{Name: " Main ", Color: "#aabbcc"})
	if err != nil {
		t.Fatal(err)
	}
	second, err := normalizeAccountCreate(CreateAccountInput{
		Name: "Main", Color: "#AABBCC", AccountType: "regular",
		BankLabel: "", LegacyOwnerLabel: "", OwnerUserID: nil,
	})
	if err != nil {
		t.Fatal(err)
	}
	if accountPayloadHash(first) != accountPayloadHash(second) {
		t.Fatal("equivalent explicit defaults produced different account fingerprints")
	}
	changed := second
	changed.BankLabel = "Bank"
	if accountPayloadHash(first) == accountPayloadHash(changed) {
		t.Fatal("changed account payload produced the same fingerprint")
	}

	categoryA, err := normalizeCategoryCreate(CreateCategoryInput{
		Type: "expense", Name: " Food ", Color: "#abcdef",
	})
	if err != nil {
		t.Fatal(err)
	}
	categoryB, err := normalizeCategoryCreate(CreateCategoryInput{
		Type: "expense", Name: "Food", Color: "#ABCDEF", SortOrder: 0,
	})
	if err != nil {
		t.Fatal(err)
	}
	if categoryPayloadHash(categoryA) != categoryPayloadHash(categoryB) {
		t.Fatal("equivalent category payloads produced different fingerprints")
	}

	accountID := "123e4567-e89b-42d3-a456-426614174000"
	categoryID := "123e4567-e89b-42d3-a456-426614174001"
	transactionA, err := normalizeTransactionCreate(CreateTransactionInput{
		Type: "expense", AccountID: accountID, CategoryID: &categoryID,
		AmountCents: "100", EventDate: "2026-07-15", Note: "  ",
	})
	if err != nil {
		t.Fatal(err)
	}
	transactionB, err := normalizeTransactionCreate(CreateTransactionInput{
		Type: "expense", AccountID: accountID, CategoryID: &categoryID,
		AmountCents: "100", EventDate: "2026-07-15", Note: "",
		ToAccountID: nil, IsBalanceAdjustment: false,
	})
	if err != nil {
		t.Fatal(err)
	}
	if transactionPayloadHash(transactionA) != transactionPayloadHash(transactionB) {
		t.Fatal("equivalent transaction defaults/nulls produced different fingerprints")
	}
	transactionB.AmountCents++
	if transactionPayloadHash(transactionA) == transactionPayloadHash(transactionB) {
		t.Fatal("changed transaction payload produced the same fingerprint")
	}
}

func TestUnicodeEquivalentTextHasOneRepresentationAndFingerprint(t *testing.T) {
	t.Parallel()

	accountNFC, err := normalizeAccountCreate(CreateAccountInput{
		Name: "Café", Color: "#112233", BankLabel: "Crédit", LegacyOwnerLabel: "José",
	})
	if err != nil {
		t.Fatal(err)
	}
	accountNFD, err := normalizeAccountCreate(CreateAccountInput{
		Name: "Cafe\u0301", Color: "#112233", BankLabel: "Cre\u0301dit", LegacyOwnerLabel: "Jose\u0301",
	})
	if err != nil {
		t.Fatal(err)
	}
	if accountNFC != accountNFD || accountPayloadHash(accountNFC) != accountPayloadHash(accountNFD) {
		t.Fatalf("Unicode-equivalent account payload diverged: %#v != %#v", accountNFC, accountNFD)
	}

	categoryNFC, err := normalizeCategoryCreate(CreateCategoryInput{
		Type: "expense", Name: "Café", Color: "#445566",
	})
	if err != nil {
		t.Fatal(err)
	}
	categoryNFD, err := normalizeCategoryCreate(CreateCategoryInput{
		Type: "expense", Name: "Cafe\u0301", Color: "#445566",
	})
	if err != nil {
		t.Fatal(err)
	}
	if categoryNFC != categoryNFD || categoryPayloadHash(categoryNFC) != categoryPayloadHash(categoryNFD) {
		t.Fatal("Unicode-equivalent category replay payload would conflict")
	}

	accountID := "123e4567-e89b-42d3-a456-426614174000"
	categoryID := "123e4567-e89b-42d3-a456-426614174001"
	transactionNFC, err := normalizeTransactionCreate(CreateTransactionInput{
		Type: "expense", AccountID: accountID, CategoryID: &categoryID,
		AmountCents: "1", EventDate: "2026-07-15", Note: "Café",
	})
	if err != nil {
		t.Fatal(err)
	}
	transactionNFD, err := normalizeTransactionCreate(CreateTransactionInput{
		Type: "expense", AccountID: accountID, CategoryID: &categoryID,
		AmountCents: "1", EventDate: "2026-07-15", Note: "Cafe\u0301",
	})
	if err != nil {
		t.Fatal(err)
	}
	if transactionNFC.Note != transactionNFD.Note || transactionPayloadHash(transactionNFC) != transactionPayloadHash(transactionNFD) {
		t.Fatal("Unicode-equivalent note produced a different normalized fingerprint")
	}
}
