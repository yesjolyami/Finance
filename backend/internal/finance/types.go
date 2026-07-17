package finance

import (
	"errors"
	"time"

	"github.com/google/uuid"
)

const (
	DefaultListLimit              = 100
	DefaultTransactionLimit       = 50
	MaximumListLimit              = 200
	MaximumCursorBytes            = 512
	MaximumMoneyCents       int64 = 9_000_000_000_000_000
)

var (
	ErrNotFound          = errors.New("finance resource not found")
	ErrForbidden         = errors.New("finance operation forbidden")
	ErrValidation        = errors.New("finance validation failed")
	ErrInvalidQuery      = errors.New("invalid finance query")
	ErrConflict          = errors.New("finance state conflict")
	ErrIdempotency       = errors.New("finance idempotency conflict")
	ErrVersionConflict   = errors.New("finance version conflict")
	ErrVersionExhausted  = errors.New("finance version exhausted")
	ErrSystemImmutable   = errors.New("system finance resource is immutable")
	ErrAggregateOverflow = errors.New("finance aggregate overflow")
)

type LocalDate struct {
	time.Time
}

func (date LocalDate) String() string {
	return date.Time.Format("2006-01-02")
}

type Field[T any] struct {
	Present bool
	Value   T
}

type NullableField[T any] struct {
	Present bool
	Value   *T
}

type Account struct {
	ID               string     `json:"id"`
	Name             string     `json:"name"`
	Color            string     `json:"color"`
	SortOrder        int        `json:"sortOrder"`
	AccountType      string     `json:"accountType"`
	BankLabel        string     `json:"bankLabel"`
	LegacyOwnerLabel string     `json:"legacyOwnerLabel"`
	OwnerUserID      *string    `json:"ownerUserId"`
	CurrencyCode     string     `json:"currencyCode"`
	IsSystem         bool       `json:"isSystem"`
	Version          string     `json:"version"`
	CreatedAt        time.Time  `json:"createdAt"`
	UpdatedAt        time.Time  `json:"updatedAt"`
	ArchivedAt       *time.Time `json:"archivedAt"`
}

type Category struct {
	ID         string     `json:"id"`
	Type       string     `json:"type"`
	Name       string     `json:"name"`
	Color      string     `json:"color"`
	SortOrder  int        `json:"sortOrder"`
	IsSystem   bool       `json:"isSystem"`
	Version    string     `json:"version"`
	CreatedAt  time.Time  `json:"createdAt"`
	UpdatedAt  time.Time  `json:"updatedAt"`
	ArchivedAt *time.Time `json:"archivedAt"`
}

type Transaction struct {
	ID                  string     `json:"id"`
	Type                string     `json:"type"`
	AccountID           string     `json:"accountId"`
	ToAccountID         *string    `json:"toAccountId"`
	CategoryID          *string    `json:"categoryId"`
	AmountCents         string     `json:"amountCents"`
	EventDate           string     `json:"eventDate"`
	Note                string     `json:"note"`
	IsBalanceAdjustment bool       `json:"isBalanceAdjustment"`
	Source              string     `json:"source"`
	CreatedByUserID     *string    `json:"createdByUserId"`
	CreatedAt           time.Time  `json:"createdAt"`
	UpdatedAt           time.Time  `json:"updatedAt"`
	DeletedAt           *time.Time `json:"deletedAt"`
	DeletionReason      *string    `json:"deletionReason"`
	Version             string     `json:"version"`
}

type AccountPage struct {
	Accounts   []Account `json:"accounts"`
	NextCursor *string   `json:"nextCursor"`
}

type CategoryPage struct {
	Categories []Category `json:"categories"`
	NextCursor *string    `json:"nextCursor"`
}

type TransactionPage struct {
	Transactions []Transaction `json:"transactions"`
	NextCursor   *string       `json:"nextCursor"`
}

type CashFlow struct {
	IncomeCents  string `json:"incomeCents"`
	ExpenseCents string `json:"expenseCents"`
}

type Summary struct {
	From                string   `json:"from"`
	To                  string   `json:"to"`
	HouseholdTotalCents string   `json:"householdTotalCents"`
	CashFlow            CashFlow `json:"cashFlow"`
}

type AccountBalance struct {
	AccountID    string     `json:"accountId"`
	Name         string     `json:"name"`
	ArchivedAt   *time.Time `json:"archivedAt"`
	BalanceCents string     `json:"balanceCents"`
}

type AccountBalancePage struct {
	AccountBalances []AccountBalance `json:"accountBalances"`
	NextCursor      *string          `json:"nextCursor"`
}

type CategoryExpense struct {
	CategoryID  string `json:"categoryId"`
	Name        string `json:"name"`
	AmountCents string `json:"amountCents"`
}

type CategoryExpensePage struct {
	ExpenseByCategory []CategoryExpense `json:"expenseByCategory"`
	NextCursor        *string           `json:"nextCursor"`
}

type CreateAccountInput struct {
	Name             string
	Color            string
	SortOrder        int
	AccountType      string
	BankLabel        string
	LegacyOwnerLabel string
	OwnerUserID      *string
}

type AccountCreate struct {
	Name             string
	Color            string
	SortOrder        int
	AccountType      string
	BankLabel        string
	LegacyOwnerLabel string
	OwnerUserID      *uuid.UUID
}

type AccountPatchInput struct {
	Name             Field[string]
	Color            Field[string]
	SortOrder        Field[int]
	AccountType      Field[string]
	BankLabel        Field[string]
	LegacyOwnerLabel Field[string]
	OwnerUserID      NullableField[string]
}

type AccountPatch struct {
	Name             Field[string]
	Color            Field[string]
	SortOrder        Field[int]
	AccountType      Field[string]
	BankLabel        Field[string]
	LegacyOwnerLabel Field[string]
	OwnerUserID      NullableField[uuid.UUID]
}

type CreateCategoryInput struct {
	Type      string
	Name      string
	Color     string
	SortOrder int
}

type CategoryCreate struct {
	Type      string
	Name      string
	Color     string
	SortOrder int
}

type CategoryPatchInput struct {
	Name      Field[string]
	Color     Field[string]
	SortOrder Field[int]
}

type CategoryPatch struct {
	Name      Field[string]
	Color     Field[string]
	SortOrder Field[int]
}

type CreateTransactionInput struct {
	Type                string
	AccountID           string
	ToAccountID         *string
	CategoryID          *string
	AmountCents         string
	EventDate           string
	Note                string
	IsBalanceAdjustment bool
}

type TransactionValues struct {
	Type                string
	AccountID           uuid.UUID
	ToAccountID         *uuid.UUID
	CategoryID          *uuid.UUID
	AmountCents         int64
	EventDate           LocalDate
	Note                string
	IsBalanceAdjustment bool
}

type TransactionPatchInput struct {
	Type                Field[string]
	AccountID           Field[string]
	ToAccountID         NullableField[string]
	CategoryID          NullableField[string]
	AmountCents         Field[string]
	EventDate           Field[string]
	Note                Field[string]
	IsBalanceAdjustment Field[bool]
}

type TransactionPatch struct {
	Type                Field[string]
	AccountID           Field[uuid.UUID]
	ToAccountID         NullableField[uuid.UUID]
	CategoryID          NullableField[uuid.UUID]
	AmountCents         Field[int64]
	EventDate           Field[LocalDate]
	Note                Field[string]
	IsBalanceAdjustment Field[bool]
}

type AccountListInput struct {
	State  string
	Limit  int
	Cursor string
}

type AccountListFilter struct {
	State string `json:"state"`
}

type CategoryListInput struct {
	Type   string
	State  string
	Limit  int
	Cursor string
}

type CategoryListFilter struct {
	Type  string `json:"type"`
	State string `json:"state"`
}

type TransactionListInput struct {
	From       string
	To         string
	Type       string
	AccountID  string
	CategoryID string
	State      string
	Limit      int
	Cursor     string
}

type TransactionListFilter struct {
	From       *LocalDate `json:"from"`
	To         *LocalDate `json:"to"`
	Type       string     `json:"type"`
	AccountID  *uuid.UUID `json:"accountId"`
	CategoryID *uuid.UUID `json:"categoryId"`
	State      string     `json:"state"`
}

type SummaryRangeInput struct {
	From string
	To   string
}

type SummaryRange struct {
	From LocalDate `json:"from"`
	To   LocalDate `json:"to"`
}

type AccountBalanceListInput struct {
	To     string
	Limit  int
	Cursor string
}

type AccountBalanceFilter struct {
	To LocalDate `json:"to"`
}

type CategoryExpenseListInput struct {
	From   string
	To     string
	Limit  int
	Cursor string
}

type CategoryExpenseFilter struct {
	From LocalDate `json:"from"`
	To   LocalDate `json:"to"`
}

type CursorPosition struct {
	CreatedAt time.Time
	ID        uuid.UUID
}
