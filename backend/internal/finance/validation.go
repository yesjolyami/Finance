package finance

import (
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
	"golang.org/x/text/unicode/norm"
)

var colorPattern = regexp.MustCompile(`^#[0-9A-F]{6}$`)

func parseCanonicalUUID(value string) (uuid.UUID, error) {
	parsed, err := uuid.Parse(value)
	if err != nil || parsed == uuid.Nil || parsed.String() != value {
		return uuid.Nil, ErrValidation
	}
	return parsed, nil
}

func parseLocalDate(value string) (LocalDate, error) {
	if len(value) != len("2006-01-02") {
		return LocalDate{}, ErrValidation
	}
	parsed, err := time.Parse("2006-01-02", value)
	if err != nil || parsed.Format("2006-01-02") != value {
		return LocalDate{}, ErrValidation
	}
	return LocalDate{Time: parsed}, nil
}

func parsePositiveCents(value string) (int64, error) {
	if value == "" || value[0] == '0' || strings.HasPrefix(value, "+") || strings.HasPrefix(value, "-") {
		return 0, ErrValidation
	}
	for _, character := range value {
		if character < '0' || character > '9' {
			return 0, ErrValidation
		}
	}
	parsed, err := strconv.ParseInt(value, 10, 64)
	if err != nil || parsed < 1 || parsed > MaximumMoneyCents {
		return 0, ErrValidation
	}
	return parsed, nil
}

func parseSignedCents(value string) (int64, error) {
	if value == "" || value == "-0" || strings.HasPrefix(value, "+") {
		return 0, ErrAggregateOverflow
	}
	digits := value
	if strings.HasPrefix(digits, "-") {
		digits = digits[1:]
	}
	if digits == "" || (len(digits) > 1 && digits[0] == '0') {
		return 0, ErrAggregateOverflow
	}
	for _, character := range digits {
		if character < '0' || character > '9' {
			return 0, ErrAggregateOverflow
		}
	}
	parsed, err := strconv.ParseInt(value, 10, 64)
	if err != nil {
		return 0, ErrAggregateOverflow
	}
	return parsed, nil
}

func parseVersion(value string) (int64, error) {
	if value == "" || value[0] == '0' || strings.HasPrefix(value, "+") || strings.HasPrefix(value, "-") {
		return 0, ErrValidation
	}
	for _, character := range value {
		if character < '0' || character > '9' {
			return 0, ErrValidation
		}
	}
	version, err := strconv.ParseInt(value, 10, 64)
	if err != nil || version < 1 {
		return 0, ErrValidation
	}
	return version, nil
}

func validateRequestID(value string) error {
	return validateUntrimmedKey(value)
}

func validateIdempotencyKey(value string) error {
	return validateUntrimmedKey(value)
}

func validateUntrimmedKey(value string) error {
	if !utf8.ValidString(value) || value == "" || value != strings.TrimSpace(value) ||
		utf8.RuneCountInString(value) > 255 {
		return ErrValidation
	}
	return nil
}

func normalizeRequiredText(value string, maximum int) (string, error) {
	trimmed := strings.TrimSpace(value)
	if !utf8.ValidString(trimmed) {
		return "", ErrValidation
	}
	normalized := norm.NFC.String(trimmed)
	if normalized == "" || utf8.RuneCountInString(normalized) > maximum {
		return "", ErrValidation
	}
	return normalized, nil
}

func normalizeOptionalText(value string, maximum int) (string, error) {
	trimmed := strings.TrimSpace(value)
	if !utf8.ValidString(trimmed) {
		return "", ErrValidation
	}
	normalized := norm.NFC.String(trimmed)
	if utf8.RuneCountInString(normalized) > maximum {
		return "", ErrValidation
	}
	return normalized, nil
}

func normalizeColor(value string) (string, error) {
	normalized := strings.ToUpper(strings.TrimSpace(value))
	if !colorPattern.MatchString(normalized) {
		return "", ErrValidation
	}
	return normalized, nil
}

func normalizeAccountCreate(input CreateAccountInput) (AccountCreate, error) {
	name, err := normalizeRequiredText(input.Name, 120)
	if err != nil {
		return AccountCreate{}, err
	}
	color, err := normalizeColor(input.Color)
	if err != nil {
		return AccountCreate{}, err
	}
	if input.SortOrder < 0 || input.SortOrder > 1_000_000 {
		return AccountCreate{}, ErrValidation
	}
	accountType := strings.TrimSpace(input.AccountType)
	if accountType == "" {
		accountType = "regular"
	}
	if accountType != "regular" && accountType != "savings" {
		return AccountCreate{}, ErrValidation
	}
	bankLabel, err := normalizeOptionalText(input.BankLabel, 120)
	if err != nil {
		return AccountCreate{}, err
	}
	legacyOwnerLabel, err := normalizeOptionalText(input.LegacyOwnerLabel, 120)
	if err != nil {
		return AccountCreate{}, err
	}
	ownerUserID, err := normalizeOptionalUUID(input.OwnerUserID)
	if err != nil {
		return AccountCreate{}, err
	}
	return AccountCreate{
		Name: name, Color: color, SortOrder: input.SortOrder, AccountType: accountType,
		BankLabel: bankLabel, LegacyOwnerLabel: legacyOwnerLabel, OwnerUserID: ownerUserID,
	}, nil
}

func normalizeAccountPatch(input AccountPatchInput) (AccountPatch, error) {
	if !input.Name.Present && !input.Color.Present && !input.SortOrder.Present &&
		!input.AccountType.Present && !input.BankLabel.Present &&
		!input.LegacyOwnerLabel.Present && !input.OwnerUserID.Present {
		return AccountPatch{}, ErrValidation
	}
	result := AccountPatch{}
	var err error
	if input.Name.Present {
		result.Name = Field[string]{Present: true}
		result.Name.Value, err = normalizeRequiredText(input.Name.Value, 120)
		if err != nil {
			return AccountPatch{}, err
		}
	}
	if input.Color.Present {
		result.Color = Field[string]{Present: true}
		result.Color.Value, err = normalizeColor(input.Color.Value)
		if err != nil {
			return AccountPatch{}, err
		}
	}
	if input.SortOrder.Present {
		if input.SortOrder.Value < 0 || input.SortOrder.Value > 1_000_000 {
			return AccountPatch{}, ErrValidation
		}
		result.SortOrder = input.SortOrder
	}
	if input.AccountType.Present {
		accountType := strings.TrimSpace(input.AccountType.Value)
		if accountType != "regular" && accountType != "savings" {
			return AccountPatch{}, ErrValidation
		}
		result.AccountType = Field[string]{Present: true, Value: accountType}
	}
	if input.BankLabel.Present {
		result.BankLabel = Field[string]{Present: true}
		result.BankLabel.Value, err = normalizeOptionalText(input.BankLabel.Value, 120)
		if err != nil {
			return AccountPatch{}, err
		}
	}
	if input.LegacyOwnerLabel.Present {
		result.LegacyOwnerLabel = Field[string]{Present: true}
		result.LegacyOwnerLabel.Value, err = normalizeOptionalText(input.LegacyOwnerLabel.Value, 120)
		if err != nil {
			return AccountPatch{}, err
		}
	}
	if input.OwnerUserID.Present {
		result.OwnerUserID.Present = true
		if input.OwnerUserID.Value != nil {
			ownerID, parseErr := parseCanonicalUUID(*input.OwnerUserID.Value)
			if parseErr != nil {
				return AccountPatch{}, parseErr
			}
			result.OwnerUserID.Value = &ownerID
		}
	}
	return result, nil
}

func normalizeCategoryCreate(input CreateCategoryInput) (CategoryCreate, error) {
	categoryType := strings.TrimSpace(input.Type)
	if categoryType != "income" && categoryType != "expense" {
		return CategoryCreate{}, ErrValidation
	}
	name, err := normalizeRequiredText(input.Name, 120)
	if err != nil {
		return CategoryCreate{}, err
	}
	color, err := normalizeColor(input.Color)
	if err != nil {
		return CategoryCreate{}, err
	}
	if input.SortOrder < 0 || input.SortOrder > 1_000_000 {
		return CategoryCreate{}, ErrValidation
	}
	return CategoryCreate{Type: categoryType, Name: name, Color: color, SortOrder: input.SortOrder}, nil
}

func normalizeCategoryPatch(input CategoryPatchInput) (CategoryPatch, error) {
	if !input.Name.Present && !input.Color.Present && !input.SortOrder.Present {
		return CategoryPatch{}, ErrValidation
	}
	result := CategoryPatch{}
	var err error
	if input.Name.Present {
		result.Name = Field[string]{Present: true}
		result.Name.Value, err = normalizeRequiredText(input.Name.Value, 120)
		if err != nil {
			return CategoryPatch{}, err
		}
	}
	if input.Color.Present {
		result.Color = Field[string]{Present: true}
		result.Color.Value, err = normalizeColor(input.Color.Value)
		if err != nil {
			return CategoryPatch{}, err
		}
	}
	if input.SortOrder.Present {
		if input.SortOrder.Value < 0 || input.SortOrder.Value > 1_000_000 {
			return CategoryPatch{}, ErrValidation
		}
		result.SortOrder = input.SortOrder
	}
	return result, nil
}

func normalizeTransactionCreate(input CreateTransactionInput) (TransactionValues, error) {
	accountID, err := parseCanonicalUUID(input.AccountID)
	if err != nil {
		return TransactionValues{}, err
	}
	toAccountID, err := normalizeOptionalUUID(input.ToAccountID)
	if err != nil {
		return TransactionValues{}, err
	}
	categoryID, err := normalizeOptionalUUID(input.CategoryID)
	if err != nil {
		return TransactionValues{}, err
	}
	amount, err := parsePositiveCents(input.AmountCents)
	if err != nil {
		return TransactionValues{}, err
	}
	eventDate, err := parseLocalDate(input.EventDate)
	if err != nil {
		return TransactionValues{}, err
	}
	note, err := normalizeOptionalText(input.Note, 1000)
	if err != nil {
		return TransactionValues{}, err
	}
	values := TransactionValues{
		Type: strings.TrimSpace(input.Type), AccountID: accountID, ToAccountID: toAccountID,
		CategoryID: categoryID, AmountCents: amount, EventDate: eventDate, Note: note,
		IsBalanceAdjustment: input.IsBalanceAdjustment,
	}
	if err := validateTransactionShape(values); err != nil {
		return TransactionValues{}, err
	}
	return values, nil
}

func normalizeTransactionPatch(input TransactionPatchInput) (TransactionPatch, error) {
	if !input.Type.Present && !input.AccountID.Present && !input.ToAccountID.Present &&
		!input.CategoryID.Present && !input.AmountCents.Present && !input.EventDate.Present &&
		!input.Note.Present && !input.IsBalanceAdjustment.Present {
		return TransactionPatch{}, ErrValidation
	}
	result := TransactionPatch{}
	if input.Type.Present {
		transactionType := strings.TrimSpace(input.Type.Value)
		if transactionType != "income" && transactionType != "expense" && transactionType != "transfer" {
			return TransactionPatch{}, ErrValidation
		}
		result.Type = Field[string]{Present: true, Value: transactionType}
	}
	if input.AccountID.Present {
		accountID, err := parseCanonicalUUID(input.AccountID.Value)
		if err != nil {
			return TransactionPatch{}, err
		}
		result.AccountID = Field[uuid.UUID]{Present: true, Value: accountID}
	}
	if input.ToAccountID.Present {
		result.ToAccountID.Present = true
		if input.ToAccountID.Value != nil {
			toAccountID, err := parseCanonicalUUID(*input.ToAccountID.Value)
			if err != nil {
				return TransactionPatch{}, err
			}
			result.ToAccountID.Value = &toAccountID
		}
	}
	if input.CategoryID.Present {
		result.CategoryID.Present = true
		if input.CategoryID.Value != nil {
			categoryID, err := parseCanonicalUUID(*input.CategoryID.Value)
			if err != nil {
				return TransactionPatch{}, err
			}
			result.CategoryID.Value = &categoryID
		}
	}
	if input.AmountCents.Present {
		amount, err := parsePositiveCents(input.AmountCents.Value)
		if err != nil {
			return TransactionPatch{}, err
		}
		result.AmountCents = Field[int64]{Present: true, Value: amount}
	}
	if input.EventDate.Present {
		eventDate, err := parseLocalDate(input.EventDate.Value)
		if err != nil {
			return TransactionPatch{}, err
		}
		result.EventDate = Field[LocalDate]{Present: true, Value: eventDate}
	}
	if input.Note.Present {
		note, err := normalizeOptionalText(input.Note.Value, 1000)
		if err != nil {
			return TransactionPatch{}, err
		}
		result.Note = Field[string]{Present: true, Value: note}
	}
	if input.IsBalanceAdjustment.Present {
		result.IsBalanceAdjustment = input.IsBalanceAdjustment
	}
	return result, nil
}

func validateTransactionShape(values TransactionValues) error {
	switch values.Type {
	case "income", "expense":
		if values.CategoryID == nil || values.ToAccountID != nil {
			return ErrValidation
		}
	case "transfer":
		if values.CategoryID != nil || values.ToAccountID == nil ||
			*values.ToAccountID == values.AccountID || values.IsBalanceAdjustment {
			return ErrValidation
		}
	default:
		return ErrValidation
	}
	return nil
}

func mergeTransactionPatch(current TransactionValues, patch TransactionPatch) (TransactionValues, error) {
	merged := current
	if patch.Type.Present {
		merged.Type = patch.Type.Value
	}
	if patch.AccountID.Present {
		merged.AccountID = patch.AccountID.Value
	}
	if patch.ToAccountID.Present {
		merged.ToAccountID = patch.ToAccountID.Value
	}
	if patch.CategoryID.Present {
		merged.CategoryID = patch.CategoryID.Value
	}
	if patch.AmountCents.Present {
		merged.AmountCents = patch.AmountCents.Value
	}
	if patch.EventDate.Present {
		merged.EventDate = patch.EventDate.Value
	}
	if patch.Note.Present {
		merged.Note = patch.Note.Value
	}
	if patch.IsBalanceAdjustment.Present {
		merged.IsBalanceAdjustment = patch.IsBalanceAdjustment.Value
	}
	if err := validateTransactionShape(merged); err != nil {
		return TransactionValues{}, err
	}
	return merged, nil
}

func normalizeOptionalUUID(value *string) (*uuid.UUID, error) {
	if value == nil {
		return nil, nil
	}
	parsed, err := parseCanonicalUUID(*value)
	if err != nil {
		return nil, err
	}
	return &parsed, nil
}

func normalizeState(value, defaultValue string) (string, error) {
	if value == "" {
		return defaultValue, nil
	}
	if value != "active" && value != "archived" && value != "deleted" && value != "all" {
		return "", ErrInvalidQuery
	}
	return value, nil
}

func normalizeLimit(value, defaultValue int) (int, error) {
	if value == 0 {
		return defaultValue, nil
	}
	if value < 1 || value > MaximumListLimit {
		return 0, ErrInvalidQuery
	}
	return value, nil
}

func normalizeAccountList(input AccountListInput) (AccountListFilter, int, error) {
	state, err := normalizeState(input.State, "active")
	if err != nil || state == "deleted" {
		return AccountListFilter{}, 0, ErrInvalidQuery
	}
	limit, err := normalizeLimit(input.Limit, DefaultListLimit)
	if err != nil {
		return AccountListFilter{}, 0, err
	}
	return AccountListFilter{State: state}, limit, nil
}

func normalizeCategoryList(input CategoryListInput) (CategoryListFilter, int, error) {
	if input.Type != "income" && input.Type != "expense" {
		return CategoryListFilter{}, 0, ErrInvalidQuery
	}
	state, err := normalizeState(input.State, "active")
	if err != nil || state == "deleted" {
		return CategoryListFilter{}, 0, ErrInvalidQuery
	}
	limit, err := normalizeLimit(input.Limit, DefaultListLimit)
	if err != nil {
		return CategoryListFilter{}, 0, err
	}
	return CategoryListFilter{Type: input.Type, State: state}, limit, nil
}

func normalizeTransactionList(input TransactionListInput) (TransactionListFilter, int, error) {
	filter := TransactionListFilter{Type: input.Type}
	state, err := normalizeState(input.State, "active")
	if err != nil || state == "archived" {
		return TransactionListFilter{}, 0, ErrInvalidQuery
	}
	filter.State = state
	if input.Type != "" && input.Type != "income" && input.Type != "expense" && input.Type != "transfer" {
		return TransactionListFilter{}, 0, ErrInvalidQuery
	}
	if input.From != "" {
		parsed, parseErr := parseLocalDate(input.From)
		if parseErr != nil {
			return TransactionListFilter{}, 0, ErrInvalidQuery
		}
		filter.From = &parsed
	}
	if input.To != "" {
		parsed, parseErr := parseLocalDate(input.To)
		if parseErr != nil {
			return TransactionListFilter{}, 0, ErrInvalidQuery
		}
		filter.To = &parsed
	}
	if filter.From != nil && filter.To != nil {
		if err := validateDateRange(*filter.From, *filter.To); err != nil {
			return TransactionListFilter{}, 0, ErrInvalidQuery
		}
	}
	if input.AccountID != "" {
		parsed, parseErr := parseCanonicalUUID(input.AccountID)
		if parseErr != nil {
			return TransactionListFilter{}, 0, ErrInvalidQuery
		}
		filter.AccountID = &parsed
	}
	if input.CategoryID != "" {
		parsed, parseErr := parseCanonicalUUID(input.CategoryID)
		if parseErr != nil {
			return TransactionListFilter{}, 0, ErrInvalidQuery
		}
		filter.CategoryID = &parsed
	}
	limit, err := normalizeLimit(input.Limit, DefaultTransactionLimit)
	if err != nil {
		return TransactionListFilter{}, 0, err
	}
	return filter, limit, nil
}

func normalizeSummaryRange(input SummaryRangeInput) (SummaryRange, error) {
	from, err := parseLocalDate(input.From)
	if err != nil {
		return SummaryRange{}, ErrInvalidQuery
	}
	to, err := parseLocalDate(input.To)
	if err != nil {
		return SummaryRange{}, ErrInvalidQuery
	}
	if err := validateDateRange(from, to); err != nil {
		return SummaryRange{}, ErrInvalidQuery
	}
	return SummaryRange{From: from, To: to}, nil
}

func normalizeAccountBalanceList(input AccountBalanceListInput) (AccountBalanceFilter, int, error) {
	to, err := parseLocalDate(input.To)
	if err != nil {
		return AccountBalanceFilter{}, 0, ErrInvalidQuery
	}
	limit, err := normalizeLimit(input.Limit, DefaultListLimit)
	if err != nil {
		return AccountBalanceFilter{}, 0, err
	}
	return AccountBalanceFilter{To: to}, limit, nil
}

func normalizeCategoryExpenseList(input CategoryExpenseListInput) (CategoryExpenseFilter, int, error) {
	rangeValue, err := normalizeSummaryRange(SummaryRangeInput{From: input.From, To: input.To})
	if err != nil {
		return CategoryExpenseFilter{}, 0, err
	}
	limit, err := normalizeLimit(input.Limit, DefaultListLimit)
	if err != nil {
		return CategoryExpenseFilter{}, 0, err
	}
	return CategoryExpenseFilter{From: rangeValue.From, To: rangeValue.To}, limit, nil
}

func validateDateRange(from, to LocalDate) error {
	if to.Before(from.Time) {
		return ErrValidation
	}
	days := int(to.Sub(from.Time).Hours()/24) + 1
	if days > 366 {
		return ErrValidation
	}
	return nil
}
