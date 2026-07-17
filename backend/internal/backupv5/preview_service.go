package backupv5

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"io"
	"strconv"
	"time"

	"github.com/google/uuid"
)

type Service struct {
	repository Repository
	clock      Clock
	entropy    io.Reader
	ttl        time.Duration
}

type serviceSettings struct {
	clock   Clock
	entropy io.Reader
	ttl     time.Duration
}

type ServiceOption func(*serviceSettings) error

func WithClock(clock Clock) ServiceOption {
	return func(settings *serviceSettings) error {
		if clock == nil {
			return ErrInvalidService
		}
		settings.clock = clock
		return nil
	}
}

func WithEntropy(entropy io.Reader) ServiceOption {
	return func(settings *serviceSettings) error {
		if entropy == nil {
			return ErrInvalidService
		}
		settings.entropy = entropy
		return nil
	}
}

func WithPreviewTTL(ttl time.Duration) ServiceOption {
	return func(settings *serviceSettings) error {
		if ttl <= 0 || ttl > MaximumPreviewTTL {
			return ErrInvalidService
		}
		settings.ttl = ttl
		return nil
	}
}

func NewPreviewService(repository Repository, options ...ServiceOption) (*Service, error) {
	if repository == nil {
		return nil, ErrInvalidService
	}
	settings := serviceSettings{
		clock:   systemClock{},
		entropy: rand.Reader,
		ttl:     DefaultPreviewTTL,
	}
	for _, option := range options {
		if option == nil {
			return nil, ErrInvalidService
		}
		if err := option(&settings); err != nil {
			return nil, err
		}
	}
	if settings.clock == nil || settings.entropy == nil || settings.ttl <= 0 || settings.ttl > MaximumPreviewTTL {
		return nil, ErrInvalidService
	}
	return &Service{
		repository: repository,
		clock:      settings.clock,
		entropy:    settings.entropy,
		ttl:        settings.ttl,
	}, nil
}

func (service *Service) Preview(
	ctx context.Context,
	subject string,
	householdID uuid.UUID,
	budgetMonth string,
	rawJSON io.Reader,
) (PreviewResponse, error) {
	if _, err := uuid.Parse(subject); err != nil || householdID == uuid.Nil {
		return PreviewResponse{}, ErrNotFound
	}
	model, err := ParseReader(ctx, rawJSON, Input{
		HouseholdID: householdID,
		BudgetMonth: budgetMonth,
	})
	if err != nil {
		return PreviewResponse{}, err
	}
	if err := ctx.Err(); err != nil {
		return PreviewResponse{}, err
	}

	random := make([]byte, previewTokenBytes+16)
	defer clear(random)
	if _, err := io.ReadFull(service.entropy, random); err != nil {
		return PreviewResponse{}, ErrEntropy
	}
	if err := ctx.Err(); err != nil {
		return PreviewResponse{}, err
	}
	tokenBytes := random[:previewTokenBytes]
	previewIDBytes := random[previewTokenBytes:]
	previewIDBytes[6] = (previewIDBytes[6] & 0x0f) | 0x40
	previewIDBytes[8] = (previewIDBytes[8] & 0x3f) | 0x80
	previewID, err := uuid.FromBytes(previewIDBytes)
	if err != nil {
		return PreviewResponse{}, ErrEntropy
	}
	token := base64.RawURLEncoding.EncodeToString(tokenBytes)
	tokenHash := sha256.Sum256(tokenBytes)
	createdAt := service.clock.Now().UTC()
	expiresAt := createdAt.Add(service.ttl)

	categories := make([]CategoryCandidate, len(model.Categories))
	for index, category := range model.Categories {
		if err := ctx.Err(); err != nil {
			return PreviewResponse{}, err
		}
		categories[index] = CategoryCandidate{Type: category.Type, Name: category.Name}
	}
	metadata := PreviewMetadata{
		ID: previewID, HouseholdID: householdID, ActorSubject: subject,
		TokenHash: tokenHash, BackupDigest: model.BackupDigest,
		BudgetMonth: model.BudgetMonth, Policy: PolicyVersion,
		ExpiresAt: expiresAt, CreatedAt: createdAt,
	}
	if err := service.repository.CreatePreview(ctx, metadata, categories); err != nil {
		return PreviewResponse{}, safePreviewError(ctx, err)
	}
	return previewResponse(model, token, expiresAt), nil
}

func previewResponse(model *Model, token string, expiresAt time.Time) PreviewResponse {
	warnings := make([]PreviewWarning, 0, 6)
	appendWarning := func(code string, count int) {
		if count > 0 {
			warnings = append(warnings, PreviewWarning{Code: code, Count: count})
		}
	}
	appendWarning("legacy_owner_not_linked", model.Warnings.LegacyOwnerNotLinked)
	appendWarning("archive_time_approximated", model.Warnings.ArchiveTimeApproximated)
	appendWarning("goal_exceeds_target", model.Warnings.GoalExceedsTarget)
	appendWarning("debt_overpaid", model.Warnings.DebtOverpaid)
	appendWarning("system_resource_preserved", model.Warnings.SystemResourcePreserved)
	appendWarning("budget_month_explicit_choice", model.Warnings.BudgetMonthExplicitChoice)
	return PreviewResponse{
		BackupDigest:      "sha256:" + hex.EncodeToString(model.BackupDigest[:]),
		ExpiresAt:         expiresAt,
		ConfirmationToken: token,
		BudgetMonth:       model.BudgetMonth.String(),
		Counts: PreviewCounts{
			Accounts:          model.Counts.Accounts,
			Categories:        model.Counts.Categories,
			Transactions:      model.Counts.Transactions,
			Budgets:           model.Counts.Budgets,
			Goals:             model.Counts.Goals,
			GoalContributions: model.Counts.GoalContributions,
			Debts:             model.Counts.Debts,
			DebtPayments:      model.Counts.DebtPayments,
		},
		Totals: PreviewTotals{
			IncomeCents:           strconv.FormatInt(model.Totals.IncomeCents, 10),
			ExpenseCents:          strconv.FormatInt(model.Totals.ExpenseCents, 10),
			TransferCents:         strconv.FormatInt(model.Totals.TransferCents, 10),
			HouseholdBalanceCents: strconv.FormatInt(model.Totals.HouseholdBalanceCents, 10),
		},
		Warnings: warnings,
	}
}

type systemClock struct{}

func (systemClock) Now() time.Time { return time.Now() }

func safePreviewError(ctx context.Context, err error) error {
	if ctxErr := ctx.Err(); ctxErr != nil {
		return ctxErr
	}
	var validation *ValidationError
	if errors.As(err, &validation) {
		return validation
	}
	for _, safe := range []error{
		ErrNotFound,
		ErrForbidden,
		ErrHouseholdNotEmpty,
		ErrTokenCollision,
	} {
		if errors.Is(err, safe) {
			return safe
		}
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return err
	}
	return ErrRepository
}
