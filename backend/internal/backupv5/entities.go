package backupv5

import (
	"crypto/sha256"
	"encoding/json"

	"github.com/google/uuid"
)

func normalizeTransaction(
	raw json.RawMessage,
	householdID uuid.UUID,
	accountIDs map[string]uuid.UUID,
	categoryIDs map[string]Category,
	index int,
) (Transaction, string, error) {
	path := itemPath("transactions", index)
	object, err := decodeObject(raw, []string{
		"id", "type", "accountId", "toAccountId", "categoryId", "amountCents",
		"date", "note", "isBalanceAdjustment", "createdAt",
	}, path)
	if err != nil {
		return Transaction{}, "", err
	}
	legacyID, err := requiredLegacyID(object, path)
	if err != nil {
		return Transaction{}, "", err
	}
	typeValue, err := decodeString(object["type"], path+".type")
	if err != nil {
		return Transaction{}, "", err
	}
	if typeValue != "income" && typeValue != "expense" && typeValue != "transfer" {
		return Transaction{}, "", validationError(ErrValue, "invalid_transaction_type", path+".type")
	}
	accountLegacyID, err := decodeString(object["accountId"], path+".accountId")
	if err != nil {
		return Transaction{}, "", err
	}
	if !validLegacyID(accountLegacyID) {
		return Transaction{}, "", validationError(ErrReference, "invalid_account_reference", path+".accountId")
	}
	accountID, present := accountIDs[accountLegacyID]
	if !present {
		return Transaction{}, "", validationError(ErrReference, "unknown_account_reference", path+".accountId")
	}
	toLegacyID, err := decodeNullableString(object["toAccountId"], path+".toAccountId")
	if err != nil {
		return Transaction{}, "", err
	}
	categoryLegacyID, err := decodeNullableString(object["categoryId"], path+".categoryId")
	if err != nil {
		return Transaction{}, "", err
	}
	var toAccountID *uuid.UUID
	var categoryID *uuid.UUID
	if typeValue == "transfer" {
		if categoryLegacyID != nil || toLegacyID == nil || !validLegacyID(*toLegacyID) || *toLegacyID == accountLegacyID {
			return Transaction{}, "", validationError(ErrValue, "invalid_transfer_shape", path)
		}
		mapped, exists := accountIDs[*toLegacyID]
		if !exists {
			return Transaction{}, "", validationError(ErrReference, "unknown_account_reference", path+".toAccountId")
		}
		toAccountID = &mapped
	} else {
		if toLegacyID != nil || categoryLegacyID == nil || !validLegacyID(*categoryLegacyID) {
			return Transaction{}, "", validationError(ErrValue, "invalid_transaction_shape", path)
		}
		category, exists := categoryIDs[*categoryLegacyID]
		if !exists {
			return Transaction{}, "", validationError(ErrReference, "unknown_category_reference", path+".categoryId")
		}
		if category.Type != typeValue {
			return Transaction{}, "", validationError(ErrReference, "category_type_mismatch", path+".categoryId")
		}
		mapped := category.ID
		categoryID = &mapped
	}
	amountToken, err := decodeIntegerToken(object["amountCents"], path+".amountCents")
	if err != nil {
		return Transaction{}, "", err
	}
	amount, err := parseBoundedInteger(amountToken, 1, MaximumMoneyCents, path+".amountCents")
	if err != nil {
		return Transaction{}, "", err
	}
	dateValue, err := decodeString(object["date"], path+".date")
	if err != nil {
		return Transaction{}, "", err
	}
	eventDate, err := parseLocalDate(dateValue, path+".date")
	if err != nil {
		return Transaction{}, "", err
	}
	noteValue, err := decodeString(object["note"], path+".note")
	if err != nil {
		return Transaction{}, "", err
	}
	note, err := normalizeOptionalText(noteValue, 1000, path+".note")
	if err != nil {
		return Transaction{}, "", err
	}
	balanceAdjustment, err := decodeBool(object["isBalanceAdjustment"], path+".isBalanceAdjustment")
	if err != nil {
		return Transaction{}, "", err
	}
	if typeValue == "transfer" && balanceAdjustment {
		return Transaction{}, "", validationError(ErrValue, "transfer_balance_adjustment_forbidden", path+".isBalanceAdjustment")
	}
	createdText, err := decodeString(object["createdAt"], path+".createdAt")
	if err != nil {
		return Transaction{}, "", err
	}
	createdAt, err := parseTimestamp(createdText, path+".createdAt")
	if err != nil {
		return Transaction{}, "", err
	}
	transaction := Transaction{
		ID: deterministicID(householdID, "transaction", legacyID), LegacyID: legacyID,
		Type: typeValue, AccountID: accountID, ToAccountID: toAccountID,
		CategoryID: categoryID, AmountCents: amount, EventDate: eventDate,
		Note: note, IsBalanceAdjustment: balanceAdjustment, CreatedAt: createdAt,
	}
	transaction.IdempotencyKey = "backup-v5:" + transaction.ID.String()
	transaction.PayloadHash = transactionFingerprint(transaction)
	return transaction, legacyID, nil
}

func normalizeGoal(raw json.RawMessage, householdID uuid.UUID, index int) (Goal, string, error) {
	path := itemPath("goals", index)
	object, err := decodeObject(raw, []string{"id", "name", "targetCents", "savedCents", "targetDate", "color", "archived", "createdAt"}, path)
	if err != nil {
		return Goal{}, "", err
	}
	legacyID, err := requiredLegacyID(object, path)
	if err != nil {
		return Goal{}, "", err
	}
	nameValue, err := decodeString(object["name"], path+".name")
	if err != nil {
		return Goal{}, "", err
	}
	name, err := normalizeRequiredText(nameValue, 120, path+".name")
	if err != nil {
		return Goal{}, "", err
	}
	targetToken, err := decodeIntegerToken(object["targetCents"], path+".targetCents")
	if err != nil {
		return Goal{}, "", err
	}
	target, err := parseBoundedInteger(targetToken, 1, MaximumMoneyCents, path+".targetCents")
	if err != nil {
		return Goal{}, "", err
	}
	savedToken, err := decodeIntegerToken(object["savedCents"], path+".savedCents")
	if err != nil {
		return Goal{}, "", err
	}
	saved, err := parseBoundedInteger(savedToken, 0, MaximumMoneyCents, path+".savedCents")
	if err != nil {
		return Goal{}, "", err
	}
	targetDateValue, err := decodeString(object["targetDate"], path+".targetDate")
	if err != nil {
		return Goal{}, "", err
	}
	targetDate, err := parseOptionalLocalDate(targetDateValue, path+".targetDate")
	if err != nil {
		return Goal{}, "", err
	}
	colorValue, err := decodeString(object["color"], path+".color")
	if err != nil {
		return Goal{}, "", err
	}
	color, err := normalizeColor(colorValue, path+".color")
	if err != nil {
		return Goal{}, "", err
	}
	archived, err := decodeBool(object["archived"], path+".archived")
	if err != nil {
		return Goal{}, "", err
	}
	createdText, err := decodeString(object["createdAt"], path+".createdAt")
	if err != nil {
		return Goal{}, "", err
	}
	createdAt, err := parseTimestamp(createdText, path+".createdAt")
	if err != nil {
		return Goal{}, "", err
	}
	return Goal{
		ID: deterministicID(householdID, "goal", legacyID), LegacyID: legacyID,
		Name: name, TargetCents: target, InitialSavedCents: saved,
		TargetDate: targetDate, Color: color, Archived: archived, CreatedAt: createdAt,
	}, legacyID, nil
}

func normalizeDebt(raw json.RawMessage, householdID uuid.UUID, index int) (Debt, string, error) {
	path := itemPath("debts", index)
	object, err := decodeObject(raw, []string{
		"id", "person", "direction", "amountCents", "paidCents", "leftCents",
		"dueDate", "note", "archived", "createdAt",
	}, path)
	if err != nil {
		return Debt{}, "", err
	}
	legacyID, err := requiredLegacyID(object, path)
	if err != nil {
		return Debt{}, "", err
	}
	personValue, err := decodeString(object["person"], path+".person")
	if err != nil {
		return Debt{}, "", err
	}
	person, err := normalizeRequiredText(personValue, 120, path+".person")
	if err != nil {
		return Debt{}, "", err
	}
	direction, err := decodeString(object["direction"], path+".direction")
	if err != nil {
		return Debt{}, "", err
	}
	if direction != "owe_me" && direction != "i_owe" {
		return Debt{}, "", validationError(ErrValue, "invalid_debt_direction", path+".direction")
	}
	amountToken, err := decodeIntegerToken(object["amountCents"], path+".amountCents")
	if err != nil {
		return Debt{}, "", err
	}
	amount, err := parseBoundedInteger(amountToken, 1, MaximumMoneyCents, path+".amountCents")
	if err != nil {
		return Debt{}, "", err
	}
	paidToken, err := decodeIntegerToken(object["paidCents"], path+".paidCents")
	if err != nil {
		return Debt{}, "", err
	}
	paid, err := parseBoundedInteger(paidToken, 0, MaximumMoneyCents, path+".paidCents")
	if err != nil {
		return Debt{}, "", err
	}
	leftToken, err := decodeIntegerToken(object["leftCents"], path+".leftCents")
	if err != nil {
		return Debt{}, "", err
	}
	left, err := parseBoundedInteger(leftToken, 0, MaximumMoneyCents, path+".leftCents")
	if err != nil {
		return Debt{}, "", err
	}
	dueDateValue, err := decodeString(object["dueDate"], path+".dueDate")
	if err != nil {
		return Debt{}, "", err
	}
	dueDate, err := parseOptionalLocalDate(dueDateValue, path+".dueDate")
	if err != nil {
		return Debt{}, "", err
	}
	noteValue, err := decodeString(object["note"], path+".note")
	if err != nil {
		return Debt{}, "", err
	}
	note, err := normalizeOptionalText(noteValue, 1000, path+".note")
	if err != nil {
		return Debt{}, "", err
	}
	archived, err := decodeBool(object["archived"], path+".archived")
	if err != nil {
		return Debt{}, "", err
	}
	createdText, err := decodeString(object["createdAt"], path+".createdAt")
	if err != nil {
		return Debt{}, "", err
	}
	createdAt, err := parseTimestamp(createdText, path+".createdAt")
	if err != nil {
		return Debt{}, "", err
	}
	return Debt{
		ID: deterministicID(householdID, "debt", legacyID), LegacyID: legacyID,
		PersonLabel: person, Direction: direction, OriginalAmountCents: amount,
		LegacyPaidCents: paid, LegacyLeftCents: left, DueDate: dueDate,
		Note: note, Archived: archived, CreatedAt: createdAt,
	}, legacyID, nil
}

func normalizeDebtPayment(raw json.RawMessage, householdID uuid.UUID, debtIDs map[string]uuid.UUID, index int) (DebtPayment, string, error) {
	path := itemPath("debtPayments", index)
	object, err := decodeObject(raw, []string{"id", "debtId", "amountCents", "date", "note", "createdAt"}, path)
	if err != nil {
		return DebtPayment{}, "", err
	}
	legacyID, err := requiredLegacyID(object, path)
	if err != nil {
		return DebtPayment{}, "", err
	}
	debtLegacyID, err := decodeString(object["debtId"], path+".debtId")
	if err != nil {
		return DebtPayment{}, "", err
	}
	if !validLegacyID(debtLegacyID) {
		return DebtPayment{}, "", validationError(ErrReference, "invalid_debt_reference", path+".debtId")
	}
	debtID, exists := debtIDs[debtLegacyID]
	if !exists {
		return DebtPayment{}, "", validationError(ErrReference, "unknown_debt_reference", path+".debtId")
	}
	amountToken, err := decodeIntegerToken(object["amountCents"], path+".amountCents")
	if err != nil {
		return DebtPayment{}, "", err
	}
	amount, err := parseBoundedInteger(amountToken, 1, MaximumMoneyCents, path+".amountCents")
	if err != nil {
		return DebtPayment{}, "", err
	}
	dateValue, err := decodeString(object["date"], path+".date")
	if err != nil {
		return DebtPayment{}, "", err
	}
	eventDate, err := parseLocalDate(dateValue, path+".date")
	if err != nil {
		return DebtPayment{}, "", err
	}
	noteValue, err := decodeString(object["note"], path+".note")
	if err != nil {
		return DebtPayment{}, "", err
	}
	note, err := normalizeOptionalText(noteValue, 1000, path+".note")
	if err != nil {
		return DebtPayment{}, "", err
	}
	createdText, err := decodeString(object["createdAt"], path+".createdAt")
	if err != nil {
		return DebtPayment{}, "", err
	}
	createdAt, err := parseTimestamp(createdText, path+".createdAt")
	if err != nil {
		return DebtPayment{}, "", err
	}
	return DebtPayment{
		ID: deterministicID(householdID, "debt-payment", legacyID), LegacyID: legacyID,
		DebtID: debtID, AmountCents: amount, EventDate: eventDate, Note: note, CreatedAt: createdAt,
	}, legacyID, nil
}

func transactionFingerprint(transaction Transaction) [32]byte {
	payload := struct {
		ID                  string  `json:"id"`
		Type                string  `json:"type"`
		AccountID           string  `json:"accountId"`
		ToAccountID         *string `json:"toAccountId"`
		CategoryID          *string `json:"categoryId"`
		AmountCents         int64   `json:"amountCents"`
		EventDate           string  `json:"eventDate"`
		Note                string  `json:"note"`
		IsBalanceAdjustment bool    `json:"isBalanceAdjustment"`
		CreatedAt           string  `json:"createdAt"`
	}{
		ID: transaction.ID.String(), Type: transaction.Type, AccountID: transaction.AccountID.String(),
		AmountCents: transaction.AmountCents, EventDate: transaction.EventDate.String(),
		Note: transaction.Note, IsBalanceAdjustment: transaction.IsBalanceAdjustment,
		CreatedAt: transaction.CreatedAt.Format("2006-01-02T15:04:05.999999999Z07:00"),
	}
	if transaction.ToAccountID != nil {
		value := transaction.ToAccountID.String()
		payload.ToAccountID = &value
	}
	if transaction.CategoryID != nil {
		value := transaction.CategoryID.String()
		payload.CategoryID = &value
	}
	encoded, _ := json.Marshal(payload)
	return sha256.Sum256(encoded)
}
