package backupv5

import (
	"context"
	"encoding/json"
	"math"
	"math/big"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
	"golang.org/x/text/cases"
	"golang.org/x/text/unicode/norm"
)

var (
	colorPattern     = regexp.MustCompile(`^#[0-9A-Fa-f]{6}$`)
	timestampPattern = regexp.MustCompile(`^[0-9]{4}-[0-9]{2}-[0-9]{2}T[0-9]{2}:[0-9]{2}:[0-9]{2}(?:\.[0-9]{1,9})?(?:Z|[+-][0-9]{2}:[0-9]{2})$`)
	unicodeFold      = cases.Fold()
)

var rootFields = []string{
	"version", "exportedAt", "accounts", "categories", "transactions",
	"goals", "debts", "debtPayments",
}

func decodeAndNormalize(ctx context.Context, raw []byte, householdID uuid.UUID, budgetMonth LocalDate) (*Model, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	root, err := decodeObject(raw, rootFields, "$")
	if err != nil {
		return nil, err
	}
	version, err := decodeIntegerToken(root["version"], "version")
	if err != nil {
		return nil, err
	}
	if version != "5" {
		return nil, validationError(ErrSchema, "unsupported_version", "version")
	}
	exportedAtText, err := decodeString(root["exportedAt"], "exportedAt")
	if err != nil {
		return nil, err
	}
	exportedAt, err := parseTimestamp(exportedAtText, "exportedAt")
	if err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	accountRows, err := decodeArray(root["accounts"], MaxAccounts, "accounts")
	if err != nil {
		return nil, err
	}
	categoryRows, err := decodeArray(root["categories"], MaxCategories, "categories")
	if err != nil {
		return nil, err
	}
	transactionRows, err := decodeArray(root["transactions"], MaxTransactions, "transactions")
	if err != nil {
		return nil, err
	}
	goalRows, err := decodeArray(root["goals"], MaxGoals, "goals")
	if err != nil {
		return nil, err
	}
	debtRows, err := decodeArray(root["debts"], MaxDebts, "debts")
	if err != nil {
		return nil, err
	}
	paymentRows, err := decodeArray(root["debtPayments"], MaxDebtPayments, "debtPayments")
	if err != nil {
		return nil, err
	}
	if err := validateEntityCounts(len(accountRows), len(categoryRows), len(transactionRows), len(goalRows), len(debtRows), len(paymentRows)); err != nil {
		return nil, err
	}

	model := &Model{
		HouseholdID:  householdID,
		ExportedAt:   exportedAt,
		BudgetMonth:  budgetMonth,
		Accounts:     make([]Account, 0, len(accountRows)),
		Categories:   make([]Category, 0, len(categoryRows)),
		Budgets:      make([]Budget, 0),
		Transactions: make([]Transaction, 0, len(transactionRows)),
		Goals:        make([]Goal, 0, len(goalRows)),
		Debts:        make([]Debt, 0, len(debtRows)),
		DebtPayments: make([]DebtPayment, 0, len(paymentRows)),
	}

	accountIDs := make(map[string]uuid.UUID, len(accountRows))
	for index, row := range accountRows {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		account, legacyID, normalizeErr := normalizeAccount(row, householdID, index)
		if normalizeErr != nil {
			return nil, normalizeErr
		}
		if _, duplicate := accountIDs[legacyID]; duplicate {
			return nil, validationError(ErrValue, "duplicate_legacy_id", itemPath("accounts", index)+".id")
		}
		accountIDs[legacyID] = account.ID
		model.Accounts = append(model.Accounts, account)
		if account.LegacyOwnerLabel != "" {
			model.Warnings.LegacyOwnerNotLinked++
		}
		if account.IsSystem {
			model.Warnings.SystemResourcePreserved++
		}
	}

	categoryIDs := make(map[string]Category, len(categoryRows))
	categoryNames := make(map[string]struct{}, len(categoryRows))
	hasIncome := false
	hasExpense := false
	for index, row := range categoryRows {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		category, legacyID, normalizeErr := normalizeCategory(row, householdID, budgetMonth, index)
		if normalizeErr != nil {
			return nil, normalizeErr
		}
		if _, duplicate := categoryIDs[legacyID]; duplicate {
			return nil, validationError(ErrValue, "duplicate_legacy_id", itemPath("categories", index)+".id")
		}
		nameKey := category.Type + "\x00" + unicodeFold.String(category.Name)
		if _, duplicate := categoryNames[nameKey]; duplicate {
			return nil, validationError(ErrValue, "duplicate_category_name", itemPath("categories", index)+".name")
		}
		categoryNames[nameKey] = struct{}{}
		categoryIDs[legacyID] = category
		model.Categories = append(model.Categories, category)
		if category.Type == "income" {
			hasIncome = true
		} else {
			hasExpense = true
		}
		if category.IsSystem {
			model.Warnings.SystemResourcePreserved++
		}
		if category.BudgetCents > 0 {
			model.Budgets = append(model.Budgets, Budget{
				ID:          budgetID(householdID, legacyID, budgetMonth),
				CategoryID:  category.ID,
				Month:       budgetMonth,
				AmountCents: category.BudgetCents,
			})
		}
	}
	if !hasIncome || !hasExpense {
		return nil, validationError(ErrValue, "income_and_expense_categories_required", "categories")
	}
	if len(model.Budgets) > 0 {
		model.Warnings.BudgetMonthExplicitChoice = 1
	}

	transactionIDs := make(map[string]struct{}, len(transactionRows))
	for index, row := range transactionRows {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		transaction, legacyID, normalizeErr := normalizeTransaction(row, householdID, accountIDs, categoryIDs, index)
		if normalizeErr != nil {
			return nil, normalizeErr
		}
		if _, duplicate := transactionIDs[legacyID]; duplicate {
			return nil, validationError(ErrValue, "duplicate_legacy_id", itemPath("transactions", index)+".id")
		}
		transactionIDs[legacyID] = struct{}{}
		model.Transactions = append(model.Transactions, transaction)
	}

	goalIDs := make(map[string]struct{}, len(goalRows))
	for index, row := range goalRows {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		goal, legacyID, normalizeErr := normalizeGoal(row, householdID, index)
		if normalizeErr != nil {
			return nil, normalizeErr
		}
		if _, duplicate := goalIDs[legacyID]; duplicate {
			return nil, validationError(ErrValue, "duplicate_legacy_id", itemPath("goals", index)+".id")
		}
		goalIDs[legacyID] = struct{}{}
		model.Goals = append(model.Goals, goal)
		if goal.InitialSavedCents > goal.TargetCents {
			model.Warnings.GoalExceedsTarget++
		}
		if goal.Archived {
			model.Warnings.ArchiveTimeApproximated++
		}
	}

	debtIDs := make(map[string]uuid.UUID, len(debtRows))
	for index, row := range debtRows {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		debt, legacyID, normalizeErr := normalizeDebt(row, householdID, index)
		if normalizeErr != nil {
			return nil, normalizeErr
		}
		if _, duplicate := debtIDs[legacyID]; duplicate {
			return nil, validationError(ErrValue, "duplicate_legacy_id", itemPath("debts", index)+".id")
		}
		debtIDs[legacyID] = debt.ID
		model.Debts = append(model.Debts, debt)
		if debt.Archived {
			model.Warnings.ArchiveTimeApproximated++
		}
	}

	paymentIDs := make(map[string]struct{}, len(paymentRows))
	for index, row := range paymentRows {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		payment, legacyID, normalizeErr := normalizeDebtPayment(row, householdID, debtIDs, index)
		if normalizeErr != nil {
			return nil, normalizeErr
		}
		if _, duplicate := paymentIDs[legacyID]; duplicate {
			return nil, validationError(ErrValue, "duplicate_legacy_id", itemPath("debtPayments", index)+".id")
		}
		paymentIDs[legacyID] = struct{}{}
		model.DebtPayments = append(model.DebtPayments, payment)
	}

	model.Counts = Counts{
		Accounts:          len(model.Accounts),
		Categories:        len(model.Categories),
		Transactions:      len(model.Transactions),
		Budgets:           len(model.Budgets),
		Goals:             len(model.Goals),
		GoalContributions: 0,
		Debts:             len(model.Debts),
		DebtPayments:      len(model.DebtPayments),
	}
	if err := calculateReconciliation(ctx, model); err != nil {
		return nil, err
	}
	return model, nil
}

func validateEntityCounts(accounts, categories, transactions, goals, debts, payments int) error {
	limits := []struct {
		value int
		limit int
		path  string
	}{
		{accounts, MaxAccounts, "accounts"},
		{categories, MaxCategories, "categories"},
		{transactions, MaxTransactions, "transactions"},
		{goals, MaxGoals, "goals"},
		{debts, MaxDebts, "debts"},
		{payments, MaxDebtPayments, "debtPayments"},
	}
	for _, item := range limits {
		if item.value < 0 || item.value > item.limit {
			return validationError(ErrLimit, "entity_count_exceeded", item.path)
		}
	}
	if int64(accounts)+int64(categories)+int64(transactions)+int64(goals)+int64(debts)+int64(payments) > MaxRawEntities {
		return validationError(ErrLimit, "total_entity_count_exceeded", "$")
	}
	return nil
}

func parseLocalDate(value, path string) (LocalDate, error) {
	if len(value) != 10 {
		return LocalDate{}, validationError(ErrValue, "invalid_date", path)
	}
	parsed, err := time.Parse("2006-01-02", value)
	if err != nil || parsed.Year() < 1 || parsed.Format("2006-01-02") != value {
		return LocalDate{}, validationError(ErrValue, "invalid_date", path)
	}
	return LocalDate{Time: parsed}, nil
}

func parseOptionalLocalDate(value, path string) (*LocalDate, error) {
	if value == "" {
		return nil, nil
	}
	parsed, err := parseLocalDate(value, path)
	if err != nil {
		return nil, err
	}
	return &parsed, nil
}

func parseTimestamp(value, path string) (time.Time, error) {
	if !timestampPattern.MatchString(value) {
		return time.Time{}, validationError(ErrValue, "invalid_timestamp", path)
	}
	parsed, err := time.Parse(time.RFC3339Nano, value)
	if err != nil || parsed.Year() < 1 {
		return time.Time{}, validationError(ErrValue, "invalid_timestamp", path)
	}
	return parsed.UTC(), nil
}

func normalizeRequiredText(value string, maximum int, path string) (string, error) {
	trimmed := strings.TrimSpace(value)
	normalized := norm.NFC.String(trimmed)
	if normalized == "" || strings.ContainsRune(normalized, '\x00') ||
		!utf8.ValidString(normalized) || utf8.RuneCountInString(normalized) > maximum {
		return "", validationError(ErrValue, "invalid_text", path)
	}
	return normalized, nil
}

func normalizeOptionalText(value string, maximum int, path string) (string, error) {
	normalized := norm.NFC.String(strings.TrimSpace(value))
	if strings.ContainsRune(normalized, '\x00') ||
		!utf8.ValidString(normalized) || utf8.RuneCountInString(normalized) > maximum {
		return "", validationError(ErrValue, "invalid_text", path)
	}
	return normalized, nil
}

func normalizePreservedText(value string, maximum int, path string) (string, error) {
	normalized := norm.NFC.String(value)
	if strings.ContainsRune(normalized, '\x00') ||
		!utf8.ValidString(normalized) || utf8.RuneCountInString(normalized) > maximum {
		return "", validationError(ErrValue, "invalid_text", path)
	}
	return normalized, nil
}

func normalizeColor(value, path string) (string, error) {
	if !colorPattern.MatchString(value) {
		return "", validationError(ErrValue, "invalid_color", path)
	}
	return strings.ToUpper(value), nil
}

func parseBoundedInteger(token string, minimum, maximum int64, path string) (int64, error) {
	value, ok := new(big.Int).SetString(token, 10)
	if !ok || value.Cmp(big.NewInt(minimum)) < 0 || value.Cmp(big.NewInt(maximum)) > 0 {
		return 0, validationError(ErrValue, "integer_out_of_range", path)
	}
	return value.Int64(), nil
}

func parseSortOrder(token, path string) (int32, error) {
	value, err := parseBoundedInteger(token, 0, math.MaxInt32, path)
	if err != nil {
		return 0, err
	}
	return int32(value), nil
}

func requiredLegacyID(object map[string]json.RawMessage, path string) (string, error) {
	value, err := decodeString(object["id"], path+".id")
	if err != nil {
		return "", err
	}
	if !validLegacyID(value) {
		return "", validationError(ErrValue, "invalid_legacy_id", path+".id")
	}
	return value, nil
}

func normalizeAccount(raw json.RawMessage, householdID uuid.UUID, index int) (Account, string, error) {
	path := itemPath("accounts", index)
	object, err := decodeObject(raw, []string{"id", "name", "color", "sortOrder", "system", "kind", "bank", "owner", "createdAt"}, path)
	if err != nil {
		return Account{}, "", err
	}
	legacyID, err := requiredLegacyID(object, path)
	if err != nil {
		return Account{}, "", err
	}
	nameValue, err := decodeString(object["name"], path+".name")
	if err != nil {
		return Account{}, "", err
	}
	name, err := normalizeRequiredText(nameValue, 120, path+".name")
	if err != nil {
		return Account{}, "", err
	}
	colorValue, err := decodeString(object["color"], path+".color")
	if err != nil {
		return Account{}, "", err
	}
	color, err := normalizeColor(colorValue, path+".color")
	if err != nil {
		return Account{}, "", err
	}
	sortToken, err := decodeIntegerToken(object["sortOrder"], path+".sortOrder")
	if err != nil {
		return Account{}, "", err
	}
	sortOrder, err := parseSortOrder(sortToken, path+".sortOrder")
	if err != nil {
		return Account{}, "", err
	}
	isSystem, err := decodeBool(object["system"], path+".system")
	if err != nil {
		return Account{}, "", err
	}
	kind, err := decodeString(object["kind"], path+".kind")
	if err != nil {
		return Account{}, "", err
	}
	if kind != "regular" && kind != "savings" {
		return Account{}, "", validationError(ErrValue, "invalid_account_kind", path+".kind")
	}
	bankValue, err := decodeString(object["bank"], path+".bank")
	if err != nil {
		return Account{}, "", err
	}
	bank, err := normalizePreservedText(bankValue, 120, path+".bank")
	if err != nil {
		return Account{}, "", err
	}
	ownerValue, err := decodeString(object["owner"], path+".owner")
	if err != nil {
		return Account{}, "", err
	}
	owner, err := normalizePreservedText(ownerValue, 120, path+".owner")
	if err != nil {
		return Account{}, "", err
	}
	createdText, err := decodeString(object["createdAt"], path+".createdAt")
	if err != nil {
		return Account{}, "", err
	}
	createdAt, err := parseTimestamp(createdText, path+".createdAt")
	if err != nil {
		return Account{}, "", err
	}
	return Account{
		ID: deterministicID(householdID, "account", legacyID), LegacyID: legacyID,
		Name: name, Color: color, SortOrder: sortOrder, IsSystem: isSystem,
		Kind: kind, BankLabel: bank, LegacyOwnerLabel: owner, CreatedAt: createdAt,
	}, legacyID, nil
}

func normalizeCategory(raw json.RawMessage, householdID uuid.UUID, budgetMonth LocalDate, index int) (Category, string, error) {
	_ = budgetMonth
	path := itemPath("categories", index)
	object, err := decodeObject(raw, []string{"id", "type", "name", "color", "budgetCents", "system", "createdAt"}, path)
	if err != nil {
		return Category{}, "", err
	}
	legacyID, err := requiredLegacyID(object, path)
	if err != nil {
		return Category{}, "", err
	}
	typeValue, err := decodeString(object["type"], path+".type")
	if err != nil {
		return Category{}, "", err
	}
	if typeValue != "income" && typeValue != "expense" {
		return Category{}, "", validationError(ErrValue, "invalid_category_type", path+".type")
	}
	nameValue, err := decodeString(object["name"], path+".name")
	if err != nil {
		return Category{}, "", err
	}
	name, err := normalizeRequiredText(nameValue, 120, path+".name")
	if err != nil {
		return Category{}, "", err
	}
	colorValue, err := decodeString(object["color"], path+".color")
	if err != nil {
		return Category{}, "", err
	}
	color, err := normalizeColor(colorValue, path+".color")
	if err != nil {
		return Category{}, "", err
	}
	budgetToken, err := decodeIntegerToken(object["budgetCents"], path+".budgetCents")
	if err != nil {
		return Category{}, "", err
	}
	budget, err := parseBoundedInteger(budgetToken, 0, MaximumMoneyCents, path+".budgetCents")
	if err != nil {
		return Category{}, "", err
	}
	if typeValue == "income" && budget != 0 {
		return Category{}, "", validationError(ErrValue, "income_budget_must_be_zero", path+".budgetCents")
	}
	isSystem, err := decodeBool(object["system"], path+".system")
	if err != nil {
		return Category{}, "", err
	}
	createdText, err := decodeString(object["createdAt"], path+".createdAt")
	if err != nil {
		return Category{}, "", err
	}
	createdAt, err := parseTimestamp(createdText, path+".createdAt")
	if err != nil {
		return Category{}, "", err
	}
	return Category{
		ID: deterministicID(householdID, "category", legacyID), LegacyID: legacyID,
		Type: typeValue, Name: name, Color: color, SortOrder: int32(index),
		IsSystem: isSystem, BudgetCents: budget, CreatedAt: createdAt,
	}, legacyID, nil
}
