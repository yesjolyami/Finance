package backupv5

import (
	"context"
	"errors"
	"io"
	"time"

	"github.com/google/uuid"
)

const (
	DefaultConfirmTimeout = 60 * time.Second
	MaximumConfirmTimeout = 120 * time.Second
	maxHMACKeys           = 32
)

var (
	ErrPreviewTokenInvalid  = errors.New("backup v5 preview token invalid")
	ErrIdempotencyKey       = errors.New("backup v5 idempotency key invalid")
	ErrRequestID            = errors.New("backup v5 request id invalid")
	ErrIdempotencyConflict  = errors.New("backup v5 idempotency conflict")
	ErrImportStateConflict  = errors.New("backup v5 import state conflict")
	ErrInvalidKeyring       = errors.New("backup v5 HMAC keyring invalid")
	ErrReferencedKeyMissing = errors.New("backup v5 referenced HMAC key missing")
)

type ConfirmInput struct {
	Subject         string
	HouseholdID     uuid.UUID
	BudgetMonth     string
	RawJSON         io.Reader
	PreviewToken    string
	IdempotencyKey  string
	ServerRequestID string
}

type ConfirmResponse struct {
	ImportRunID   string               `json:"importRunId"`
	Status        string               `json:"status"`
	PolicyVersion string               `json:"policyVersion"`
	CompletedAt   time.Time            `json:"completedAt"`
	Counts        PreviewCounts        `json:"counts"`
	WarningCounts ConfirmWarningCounts `json:"warningCounts"`
}

type ConfirmWarningCounts struct {
	LegacyOwnerNotLinked      int `json:"legacyOwnerNotLinked"`
	ArchiveTimeApproximated   int `json:"archiveTimeApproximated"`
	GoalExceedsTarget         int `json:"goalExceedsTarget"`
	DebtOverpaid              int `json:"debtOverpaid"`
	SystemResourcePreserved   int `json:"systemResourcePreserved"`
	BudgetMonthExplicitChoice int `json:"budgetMonthExplicitChoice"`
}

type ConfirmResult struct {
	Response ConfirmResponse `json:"-"`
	Replayed bool            `json:"-"`
}

type HMACCandidate struct {
	KeyID       string
	Lookup      [32]byte
	Fingerprint [32]byte
}

type ConfirmCommand struct {
	ActorSubject    string
	HouseholdID     uuid.UUID
	Model           *Model
	TokenHash       [32]byte
	TokenValid      bool
	Candidates      []HMACCandidate
	ActiveKeyID     string
	ActiveLookup    [32]byte
	Fingerprint     [32]byte
	ServerRequestID string
}

func (ConfirmInput) String() string               { return "backupv5.ConfirmInput[redacted]" }
func (ConfirmInput) GoString() string             { return "backupv5.ConfirmInput[redacted]" }
func (ConfirmInput) MarshalJSON() ([]byte, error) { return nil, ErrInvalidService }

func (ConfirmCommand) String() string               { return "backupv5.ConfirmCommand[redacted]" }
func (ConfirmCommand) GoString() string             { return "backupv5.ConfirmCommand[redacted]" }
func (ConfirmCommand) MarshalJSON() ([]byte, error) { return nil, ErrInvalidService }

type ConfirmRepository interface {
	Confirm(context.Context, ConfirmCommand) (ConfirmResult, error)
	ReferencedHMACKeyIDs(context.Context) ([]string, error)
}
