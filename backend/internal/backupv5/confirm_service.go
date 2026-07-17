package backupv5

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
)

type ConfirmService struct {
	repository ConfirmRepository
	keyring    *HMACKeyring
	timeout    time.Duration
}

type ConfirmServiceOption func(*ConfirmService) error

func WithConfirmTimeout(timeout time.Duration) ConfirmServiceOption {
	return func(service *ConfirmService) error {
		if timeout <= 0 || timeout > MaximumConfirmTimeout {
			return ErrInvalidService
		}
		service.timeout = timeout
		return nil
	}
}

func NewConfirmService(repository ConfirmRepository, keyring *HMACKeyring, options ...ConfirmServiceOption) (*ConfirmService, error) {
	if repository == nil || keyring == nil {
		return nil, ErrInvalidService
	}
	service := &ConfirmService{repository: repository, keyring: keyring, timeout: DefaultConfirmTimeout}
	for _, option := range options {
		if option == nil {
			return nil, ErrInvalidService
		}
		if err := option(service); err != nil {
			return nil, err
		}
	}
	return service, nil
}

func (service *ConfirmService) Confirm(ctx context.Context, input ConfirmInput) (ConfirmResult, error) {
	if _, err := uuid.Parse(input.Subject); err != nil || input.HouseholdID == uuid.Nil {
		return ConfirmResult{}, ErrNotFound
	}
	if err := validateOpaqueHeader(input.IdempotencyKey, 255, ErrIdempotencyKey); err != nil {
		return ConfirmResult{}, err
	}
	if err := validateOpaqueHeader(input.ServerRequestID, 255, ErrRequestID); err != nil {
		return ConfirmResult{}, err
	}
	mutationContext, cancel := boundedMutationContext(ctx, service.timeout)
	defer cancel()
	model, err := ParseReader(mutationContext, input.RawJSON, Input{
		HouseholdID: input.HouseholdID,
		BudgetMonth: input.BudgetMonth,
	})
	if err != nil {
		return ConfirmResult{}, err
	}
	tokenHash, tokenValid := decodePreviewToken(input.PreviewToken)
	candidates := service.keyring.candidates(input.IdempotencyKey, model.BackupDigest, model.BudgetMonth.String())
	active, err := service.keyring.active(candidates)
	if err != nil {
		return ConfirmResult{}, err
	}
	result, err := service.repository.Confirm(mutationContext, ConfirmCommand{
		ActorSubject: input.Subject, HouseholdID: input.HouseholdID, Model: model,
		TokenHash: tokenHash, TokenValid: tokenValid, Candidates: candidates,
		ActiveKeyID: active.KeyID, ActiveLookup: active.Lookup, Fingerprint: active.Fingerprint,
		ServerRequestID: input.ServerRequestID,
	})
	if err != nil {
		return ConfirmResult{}, safeConfirmError(mutationContext, err)
	}
	return result, nil
}

func (service *ConfirmService) AuditKeyring(ctx context.Context) error {
	keyIDs, err := service.repository.ReferencedHMACKeyIDs(ctx)
	if err != nil {
		return safeConfirmError(ctx, err)
	}
	return service.keyring.validateReferenced(keyIDs)
}

func decodePreviewToken(raw string) ([32]byte, bool) {
	if raw == "" || strings.Contains(raw, "=") {
		return [32]byte{}, false
	}
	decoded, err := base64.RawURLEncoding.DecodeString(raw)
	if err != nil || len(decoded) != previewTokenBytes || base64.RawURLEncoding.EncodeToString(decoded) != raw {
		return [32]byte{}, false
	}
	result := sha256.Sum256(decoded)
	clear(decoded)
	return result, true
}

func validateOpaqueHeader(value string, maximumBytes int, kind error) error {
	if value == "" || len(value) > maximumBytes || !utf8.ValidString(value) || strings.TrimSpace(value) != value {
		return kind
	}
	for _, character := range []byte(value) {
		if character <= 0x1f {
			return kind
		}
	}
	return nil
}

func boundedMutationContext(ctx context.Context, timeout time.Duration) (context.Context, context.CancelFunc) {
	if deadline, exists := ctx.Deadline(); exists && time.Until(deadline) <= timeout {
		return context.WithCancel(ctx)
	}
	return context.WithTimeout(ctx, timeout)
}

func safeConfirmError(ctx context.Context, err error) error {
	if ctxErr := ctx.Err(); ctxErr != nil {
		return ctxErr
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return err
	}
	var validation *ValidationError
	if errors.As(err, &validation) {
		return validation
	}
	for _, safe := range []error{
		ErrNotFound, ErrForbidden, ErrHouseholdNotEmpty, ErrPreviewTokenInvalid,
		ErrIdempotencyConflict, ErrImportStateConflict, ErrReconciliation,
		ErrReferencedKeyMissing,
	} {
		if errors.Is(err, safe) {
			return safe
		}
	}
	return ErrRepository
}

func fingerprintsEqual(first, second []byte) bool {
	return len(first) == 32 && len(second) == 32 && subtle.ConstantTimeCompare(first, second) == 1
}
