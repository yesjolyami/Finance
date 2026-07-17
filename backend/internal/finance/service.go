package finance

import (
	"context"
	"crypto/sha256"
	"time"

	"github.com/google/uuid"
)

const (
	readTimeout     = 3 * time.Second
	mutationTimeout = 5 * time.Second
)

type CreateMeta struct {
	IdempotencyKey string
	PayloadHash    [sha256.Size]byte
	RequestID      string
}

type MutationMeta struct {
	ExpectedVersion int64
	RequestID       string
}

type CreateResult[T any] struct {
	Value    T
	Replayed bool
}

type Repository interface {
	ListAccounts(context.Context, string, uuid.UUID, AccountListFilter, int, *CursorPosition) ([]Account, bool, error)
	CreateAccount(context.Context, string, uuid.UUID, AccountCreate, CreateMeta) (CreateResult[Account], error)
	UpdateAccount(context.Context, string, uuid.UUID, uuid.UUID, AccountPatch, MutationMeta) (Account, error)
	SetAccountArchived(context.Context, string, uuid.UUID, uuid.UUID, bool, MutationMeta) (Account, error)

	ListCategories(context.Context, string, uuid.UUID, CategoryListFilter, int, *CursorPosition) ([]Category, bool, error)
	CreateCategory(context.Context, string, uuid.UUID, CategoryCreate, CreateMeta) (CreateResult[Category], error)
	UpdateCategory(context.Context, string, uuid.UUID, uuid.UUID, CategoryPatch, MutationMeta) (Category, error)
	SetCategoryArchived(context.Context, string, uuid.UUID, uuid.UUID, bool, MutationMeta) (Category, error)

	ListTransactions(context.Context, string, uuid.UUID, TransactionListFilter, int, *CursorPosition) ([]Transaction, bool, error)
	CreateTransaction(context.Context, string, uuid.UUID, TransactionValues, CreateMeta) (CreateResult[Transaction], error)
	UpdateTransaction(context.Context, string, uuid.UUID, uuid.UUID, TransactionPatch, MutationMeta) (Transaction, error)
	DeleteTransaction(context.Context, string, uuid.UUID, uuid.UUID, string, MutationMeta) (Transaction, error)
	RestoreTransaction(context.Context, string, uuid.UUID, uuid.UUID, MutationMeta) (Transaction, error)

	GetSummary(context.Context, string, uuid.UUID, SummaryRange) (Summary, error)
	ListAccountBalances(context.Context, string, uuid.UUID, AccountBalanceFilter, int, *CursorPosition) ([]AccountBalance, []CursorPosition, bool, error)
	ListCategoryExpenses(context.Context, string, uuid.UUID, CategoryExpenseFilter, int, *CursorPosition) ([]CategoryExpense, []CursorPosition, bool, error)
}

type Service struct {
	repository Repository
}

func NewService(repository Repository) *Service {
	return &Service{repository: repository}
}

func (service *Service) ListAccounts(ctx context.Context, subject, householdID string, input AccountListInput) (AccountPage, error) {
	household, err := pathUUID(householdID)
	if err != nil {
		return AccountPage{}, err
	}
	filter, limit, err := normalizeAccountList(input)
	if err != nil {
		return AccountPage{}, err
	}
	position, err := optionalCursor(input.Cursor, "accounts", filter)
	if err != nil {
		return AccountPage{}, err
	}
	ctx, cancel := boundedContext(ctx, readTimeout)
	defer cancel()
	items, more, err := service.repository.ListAccounts(ctx, subject, household, filter, limit, position)
	if err != nil {
		return AccountPage{}, err
	}
	page := AccountPage{Accounts: nonNilAccounts(items)}
	if more && len(items) > 0 {
		page.NextCursor, err = nextEntityCursor("accounts", filter, items[len(items)-1].CreatedAt, items[len(items)-1].ID)
	}
	return page, err
}

func (service *Service) CreateAccount(ctx context.Context, subject, householdID, key, requestID string, input CreateAccountInput) (CreateResult[Account], error) {
	household, err := pathUUID(householdID)
	if err != nil {
		return CreateResult[Account]{}, err
	}
	if err := validateMutationHeaders(key, requestID); err != nil {
		return CreateResult[Account]{}, err
	}
	value, err := normalizeAccountCreate(input)
	if err != nil {
		return CreateResult[Account]{}, err
	}
	ctx, cancel := boundedContext(ctx, mutationTimeout)
	defer cancel()
	return service.repository.CreateAccount(ctx, subject, household, value, CreateMeta{key, accountPayloadHash(value), requestID})
}

func (service *Service) UpdateAccount(ctx context.Context, subject, householdID, accountID, version, requestID string, input AccountPatchInput) (Account, error) {
	household, object, meta, err := mutationIdentity(householdID, accountID, version, requestID)
	if err != nil {
		return Account{}, err
	}
	patch, err := normalizeAccountPatch(input)
	if err != nil {
		return Account{}, err
	}
	ctx, cancel := boundedContext(ctx, mutationTimeout)
	defer cancel()
	return service.repository.UpdateAccount(ctx, subject, household, object, patch, meta)
}

func (service *Service) SetAccountArchived(ctx context.Context, subject, householdID, accountID, version, requestID string, archived bool) (Account, error) {
	household, object, meta, err := mutationIdentity(householdID, accountID, version, requestID)
	if err != nil {
		return Account{}, err
	}
	ctx, cancel := boundedContext(ctx, mutationTimeout)
	defer cancel()
	return service.repository.SetAccountArchived(ctx, subject, household, object, archived, meta)
}

func (service *Service) ListCategories(ctx context.Context, subject, householdID string, input CategoryListInput) (CategoryPage, error) {
	household, err := pathUUID(householdID)
	if err != nil {
		return CategoryPage{}, err
	}
	filter, limit, err := normalizeCategoryList(input)
	if err != nil {
		return CategoryPage{}, err
	}
	position, err := optionalCursor(input.Cursor, "categories", filter)
	if err != nil {
		return CategoryPage{}, err
	}
	ctx, cancel := boundedContext(ctx, readTimeout)
	defer cancel()
	items, more, err := service.repository.ListCategories(ctx, subject, household, filter, limit, position)
	if err != nil {
		return CategoryPage{}, err
	}
	page := CategoryPage{Categories: nonNilCategories(items)}
	if more && len(items) > 0 {
		page.NextCursor, err = nextEntityCursor("categories", filter, items[len(items)-1].CreatedAt, items[len(items)-1].ID)
	}
	return page, err
}

func (service *Service) CreateCategory(ctx context.Context, subject, householdID, key, requestID string, input CreateCategoryInput) (CreateResult[Category], error) {
	household, err := pathUUID(householdID)
	if err != nil {
		return CreateResult[Category]{}, err
	}
	if err := validateMutationHeaders(key, requestID); err != nil {
		return CreateResult[Category]{}, err
	}
	value, err := normalizeCategoryCreate(input)
	if err != nil {
		return CreateResult[Category]{}, err
	}
	ctx, cancel := boundedContext(ctx, mutationTimeout)
	defer cancel()
	return service.repository.CreateCategory(ctx, subject, household, value, CreateMeta{key, categoryPayloadHash(value), requestID})
}

func (service *Service) UpdateCategory(ctx context.Context, subject, householdID, categoryID, version, requestID string, input CategoryPatchInput) (Category, error) {
	household, object, meta, err := mutationIdentity(householdID, categoryID, version, requestID)
	if err != nil {
		return Category{}, err
	}
	patch, err := normalizeCategoryPatch(input)
	if err != nil {
		return Category{}, err
	}
	ctx, cancel := boundedContext(ctx, mutationTimeout)
	defer cancel()
	return service.repository.UpdateCategory(ctx, subject, household, object, patch, meta)
}

func (service *Service) SetCategoryArchived(ctx context.Context, subject, householdID, categoryID, version, requestID string, archived bool) (Category, error) {
	household, object, meta, err := mutationIdentity(householdID, categoryID, version, requestID)
	if err != nil {
		return Category{}, err
	}
	ctx, cancel := boundedContext(ctx, mutationTimeout)
	defer cancel()
	return service.repository.SetCategoryArchived(ctx, subject, household, object, archived, meta)
}

func (service *Service) ListTransactions(ctx context.Context, subject, householdID string, input TransactionListInput) (TransactionPage, error) {
	household, err := pathUUID(householdID)
	if err != nil {
		return TransactionPage{}, err
	}
	filter, limit, err := normalizeTransactionList(input)
	if err != nil {
		return TransactionPage{}, err
	}
	position, err := optionalCursor(input.Cursor, "transactions", filter)
	if err != nil {
		return TransactionPage{}, err
	}
	ctx, cancel := boundedContext(ctx, readTimeout)
	defer cancel()
	items, more, err := service.repository.ListTransactions(ctx, subject, household, filter, limit, position)
	if err != nil {
		return TransactionPage{}, err
	}
	page := TransactionPage{Transactions: nonNilTransactions(items)}
	if more && len(items) > 0 {
		page.NextCursor, err = nextEntityCursor("transactions", filter, items[len(items)-1].CreatedAt, items[len(items)-1].ID)
	}
	return page, err
}

func (service *Service) CreateTransaction(ctx context.Context, subject, householdID, key, requestID string, input CreateTransactionInput) (CreateResult[Transaction], error) {
	household, err := pathUUID(householdID)
	if err != nil {
		return CreateResult[Transaction]{}, err
	}
	if err := validateMutationHeaders(key, requestID); err != nil {
		return CreateResult[Transaction]{}, err
	}
	value, err := normalizeTransactionCreate(input)
	if err != nil {
		return CreateResult[Transaction]{}, err
	}
	ctx, cancel := boundedContext(ctx, mutationTimeout)
	defer cancel()
	return service.repository.CreateTransaction(ctx, subject, household, value, CreateMeta{key, transactionPayloadHash(value), requestID})
}

func (service *Service) UpdateTransaction(ctx context.Context, subject, householdID, transactionID, version, requestID string, input TransactionPatchInput) (Transaction, error) {
	household, object, meta, err := mutationIdentity(householdID, transactionID, version, requestID)
	if err != nil {
		return Transaction{}, err
	}
	patch, err := normalizeTransactionPatch(input)
	if err != nil {
		return Transaction{}, err
	}
	ctx, cancel := boundedContext(ctx, mutationTimeout)
	defer cancel()
	return service.repository.UpdateTransaction(ctx, subject, household, object, patch, meta)
}

func (service *Service) DeleteTransaction(ctx context.Context, subject, householdID, transactionID, version, requestID, reason string) (Transaction, error) {
	household, object, meta, err := mutationIdentity(householdID, transactionID, version, requestID)
	if err != nil {
		return Transaction{}, err
	}
	reason, err = normalizeRequiredText(reason, 500)
	if err != nil {
		return Transaction{}, err
	}
	ctx, cancel := boundedContext(ctx, mutationTimeout)
	defer cancel()
	return service.repository.DeleteTransaction(ctx, subject, household, object, reason, meta)
}

func (service *Service) RestoreTransaction(ctx context.Context, subject, householdID, transactionID, version, requestID string) (Transaction, error) {
	household, object, meta, err := mutationIdentity(householdID, transactionID, version, requestID)
	if err != nil {
		return Transaction{}, err
	}
	ctx, cancel := boundedContext(ctx, mutationTimeout)
	defer cancel()
	return service.repository.RestoreTransaction(ctx, subject, household, object, meta)
}

func (service *Service) GetSummary(ctx context.Context, subject, householdID string, input SummaryRangeInput) (Summary, error) {
	household, err := pathUUID(householdID)
	if err != nil {
		return Summary{}, err
	}
	rangeValue, err := normalizeSummaryRange(input)
	if err != nil {
		return Summary{}, err
	}
	ctx, cancel := boundedContext(ctx, readTimeout)
	defer cancel()
	return service.repository.GetSummary(ctx, subject, household, rangeValue)
}

func (service *Service) ListAccountBalances(ctx context.Context, subject, householdID string, input AccountBalanceListInput) (AccountBalancePage, error) {
	household, err := pathUUID(householdID)
	if err != nil {
		return AccountBalancePage{}, err
	}
	filter, limit, err := normalizeAccountBalanceList(input)
	if err != nil {
		return AccountBalancePage{}, err
	}
	position, err := optionalCursor(input.Cursor, "account-balances", filter)
	if err != nil {
		return AccountBalancePage{}, err
	}
	ctx, cancel := boundedContext(ctx, readTimeout)
	defer cancel()
	items, positions, more, err := service.repository.ListAccountBalances(ctx, subject, household, filter, limit, position)
	if err != nil {
		return AccountBalancePage{}, err
	}
	page := AccountBalancePage{AccountBalances: nonNilBalances(items)}
	if more && len(positions) > 0 {
		cursor, cursorErr := encodeCursor("account-balances", filter, positions[len(positions)-1])
		if cursorErr != nil {
			return AccountBalancePage{}, cursorErr
		}
		page.NextCursor = &cursor
	}
	return page, nil
}

func (service *Service) ListCategoryExpenses(ctx context.Context, subject, householdID string, input CategoryExpenseListInput) (CategoryExpensePage, error) {
	household, err := pathUUID(householdID)
	if err != nil {
		return CategoryExpensePage{}, err
	}
	filter, limit, err := normalizeCategoryExpenseList(input)
	if err != nil {
		return CategoryExpensePage{}, err
	}
	position, err := optionalCursor(input.Cursor, "category-expenses", filter)
	if err != nil {
		return CategoryExpensePage{}, err
	}
	ctx, cancel := boundedContext(ctx, readTimeout)
	defer cancel()
	items, positions, more, err := service.repository.ListCategoryExpenses(ctx, subject, household, filter, limit, position)
	if err != nil {
		return CategoryExpensePage{}, err
	}
	page := CategoryExpensePage{ExpenseByCategory: nonNilExpenses(items)}
	if more && len(positions) > 0 {
		cursor, cursorErr := encodeCursor("category-expenses", filter, positions[len(positions)-1])
		if cursorErr != nil {
			return CategoryExpensePage{}, cursorErr
		}
		page.NextCursor = &cursor
	}
	return page, nil
}

func pathUUID(value string) (uuid.UUID, error) {
	parsed, err := parseCanonicalUUID(value)
	if err != nil {
		return uuid.Nil, ErrNotFound
	}
	return parsed, nil
}

func mutationIdentity(householdID, objectID, version, requestID string) (uuid.UUID, uuid.UUID, MutationMeta, error) {
	household, err := pathUUID(householdID)
	if err != nil {
		return uuid.Nil, uuid.Nil, MutationMeta{}, err
	}
	object, err := pathUUID(objectID)
	if err != nil {
		return uuid.Nil, uuid.Nil, MutationMeta{}, err
	}
	expected, err := parseVersion(version)
	if err != nil {
		return uuid.Nil, uuid.Nil, MutationMeta{}, err
	}
	if err := validateRequestID(requestID); err != nil {
		return uuid.Nil, uuid.Nil, MutationMeta{}, err
	}
	return household, object, MutationMeta{ExpectedVersion: expected, RequestID: requestID}, nil
}

func validateMutationHeaders(key, requestID string) error {
	if err := validateIdempotencyKey(key); err != nil {
		return err
	}
	return validateRequestID(requestID)
}

func optionalCursor(raw, domain string, filter any) (*CursorPosition, error) {
	if raw == "" {
		return nil, nil
	}
	position, err := decodeCursor(raw, domain, filter)
	if err != nil {
		return nil, err
	}
	return &position, nil
}

func nextEntityCursor(domain string, filter any, createdAt time.Time, id string) (*string, error) {
	parsed, err := parseCanonicalUUID(id)
	if err != nil {
		return nil, ErrInvalidQuery
	}
	cursor, err := encodeCursor(domain, filter, CursorPosition{CreatedAt: createdAt, ID: parsed})
	if err != nil {
		return nil, err
	}
	return &cursor, nil
}

func boundedContext(parent context.Context, maximum time.Duration) (context.Context, context.CancelFunc) {
	if deadline, ok := parent.Deadline(); ok && time.Until(deadline) <= maximum {
		return context.WithCancel(parent)
	}
	return context.WithTimeout(parent, maximum)
}

func nonNilAccounts(items []Account) []Account {
	if items == nil {
		return make([]Account, 0)
	}
	return items
}

func nonNilCategories(items []Category) []Category {
	if items == nil {
		return make([]Category, 0)
	}
	return items
}

func nonNilTransactions(items []Transaction) []Transaction {
	if items == nil {
		return make([]Transaction, 0)
	}
	return items
}

func nonNilBalances(items []AccountBalance) []AccountBalance {
	if items == nil {
		return make([]AccountBalance, 0)
	}
	return items
}

func nonNilExpenses(items []CategoryExpense) []CategoryExpense {
	if items == nil {
		return make([]CategoryExpense, 0)
	}
	return items
}
