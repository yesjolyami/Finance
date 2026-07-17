package backupv5

import (
	"time"

	"github.com/google/uuid"
)

const (
	MaxRawBytes       int64 = 32 * 1024 * 1024
	MaxJSONDepth            = 12
	MaxRawStringBytes       = 4 * 1024
	MaxLegacyIDBytes        = 255
	MaxAccounts             = 10_000
	MaxCategories           = 20_000
	MaxTransactions         = 200_000
	MaxGoals                = 20_000
	MaxDebts                = 20_000
	MaxDebtPayments         = 200_000
	MaxRawEntities          = 300_000
	MaximumMoneyCents int64 = 9_000_000_000_000_000
)

type Input struct {
	HouseholdID uuid.UUID
	BudgetMonth string
}

type LocalDate struct {
	time.Time
}

func (date LocalDate) String() string {
	return date.Format("2006-01-02")
}

type Model struct {
	HouseholdID    uuid.UUID
	BackupDigest   [32]byte
	ExportedAt     time.Time
	BudgetMonth    LocalDate
	Accounts       []Account
	Categories     []Category
	Budgets        []Budget
	Transactions   []Transaction
	Goals          []Goal
	Debts          []Debt
	DebtPayments   []DebtPayment
	Counts         Counts
	Totals         Totals
	Warnings       WarningCounts
	Reconciliation Reconciliation
}

type Account struct {
	ID               uuid.UUID
	LegacyID         string
	Name             string
	Color            string
	SortOrder        int32
	IsSystem         bool
	Kind             string
	BankLabel        string
	LegacyOwnerLabel string
	CreatedAt        time.Time
}

type Category struct {
	ID          uuid.UUID
	LegacyID    string
	Type        string
	Name        string
	Color       string
	SortOrder   int32
	IsSystem    bool
	BudgetCents int64
	CreatedAt   time.Time
}

type Budget struct {
	ID          uuid.UUID
	CategoryID  uuid.UUID
	Month       LocalDate
	AmountCents int64
}

type Transaction struct {
	ID                  uuid.UUID
	LegacyID            string
	Type                string
	AccountID           uuid.UUID
	ToAccountID         *uuid.UUID
	CategoryID          *uuid.UUID
	AmountCents         int64
	EventDate           LocalDate
	Note                string
	IsBalanceAdjustment bool
	CreatedAt           time.Time
	IdempotencyKey      string
	PayloadHash         [32]byte
}

type Goal struct {
	ID                uuid.UUID
	LegacyID          string
	Name              string
	TargetCents       int64
	InitialSavedCents int64
	TargetDate        *LocalDate
	Color             string
	Archived          bool
	CreatedAt         time.Time
}

type Debt struct {
	ID                  uuid.UUID
	LegacyID            string
	PersonLabel         string
	Direction           string
	OriginalAmountCents int64
	LegacyPaidCents     int64
	LegacyLeftCents     int64
	DueDate             *LocalDate
	Note                string
	Archived            bool
	CreatedAt           time.Time
}

type DebtPayment struct {
	ID          uuid.UUID
	LegacyID    string
	DebtID      uuid.UUID
	AmountCents int64
	EventDate   LocalDate
	Note        string
	CreatedAt   time.Time
}

type Counts struct {
	Accounts          int
	Categories        int
	Transactions      int
	Budgets           int
	Goals             int
	GoalContributions int
	Debts             int
	DebtPayments      int
}

type Totals struct {
	IncomeCents           int64
	ExpenseCents          int64
	TransferCents         int64
	HouseholdBalanceCents int64
	CashFlowIncomeCents   int64
	CashFlowExpenseCents  int64
	BudgetCents           int64
}

type WarningCounts struct {
	LegacyOwnerNotLinked      int
	ArchiveTimeApproximated   int
	GoalExceedsTarget         int
	DebtOverpaid              int
	SystemResourcePreserved   int
	BudgetMonthExplicitChoice int
}

type MonthTotals struct {
	IncomeCount          int
	IncomeCents          int64
	ExpenseCount         int
	ExpenseCents         int64
	TransferCount        int
	TransferCents        int64
	CashFlowIncomeCents  int64
	CashFlowExpenseCents int64
}

type DebtReconciliation struct {
	DebtID    uuid.UUID
	PaidCents int64
	LeftCents int64
}

type Reconciliation struct {
	AccountBalances map[uuid.UUID]int64
	Monthly         map[string]MonthTotals
	Debts           []DebtReconciliation
}
