package backupv5

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
)

const (
	PolicyVersion     = "backup-v5-import/1"
	DefaultPreviewTTL = 10 * time.Minute
	MaximumPreviewTTL = 15 * time.Minute
	previewTokenBytes = 32
)

var (
	ErrNotFound          = errors.New("backup v5 resource not found")
	ErrForbidden         = errors.New("backup v5 operation forbidden")
	ErrHouseholdNotEmpty = errors.New("backup v5 household is not empty")
	ErrTokenCollision    = errors.New("backup v5 preview unavailable")
	ErrEntropy           = errors.New("backup v5 entropy unavailable")
	ErrInvalidService    = errors.New("backup v5 service configuration invalid")
	ErrRepository        = errors.New("backup v5 repository unavailable")
)

type PreviewMetadata struct {
	ID           uuid.UUID
	HouseholdID  uuid.UUID
	ActorSubject string
	TokenHash    [32]byte
	BackupDigest [32]byte
	BudgetMonth  LocalDate
	Policy       string
	ExpiresAt    time.Time
	CreatedAt    time.Time
}

type CategoryCandidate struct {
	Type string
	Name string
}

type Repository interface {
	CreatePreview(context.Context, PreviewMetadata, []CategoryCandidate) error
}

type PreviewResponse struct {
	BackupDigest      string           `json:"backupDigest"`
	ExpiresAt         time.Time        `json:"expiresAt"`
	ConfirmationToken string           `json:"confirmationToken"`
	BudgetMonth       string           `json:"budgetMonth"`
	Counts            PreviewCounts    `json:"counts"`
	Totals            PreviewTotals    `json:"totals"`
	Warnings          []PreviewWarning `json:"warnings"`
}

type PreviewCounts struct {
	Accounts          int `json:"accounts"`
	Categories        int `json:"categories"`
	Transactions      int `json:"transactions"`
	Budgets           int `json:"budgets"`
	Goals             int `json:"goals"`
	GoalContributions int `json:"goalContributions"`
	Debts             int `json:"debts"`
	DebtPayments      int `json:"debtPayments"`
}

type PreviewTotals struct {
	IncomeCents           string `json:"incomeCents"`
	ExpenseCents          string `json:"expenseCents"`
	TransferCents         string `json:"transferCents"`
	HouseholdBalanceCents string `json:"householdBalanceCents"`
}

type PreviewWarning struct {
	Code  string `json:"code"`
	Count int    `json:"count"`
}

type Clock interface {
	Now() time.Time
}
