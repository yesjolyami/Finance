package finance

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
)

type serviceStubRepository struct {
	Repository
	listAccounts  func(context.Context, string, uuid.UUID, AccountListFilter, int, *CursorPosition) ([]Account, bool, error)
	createAccount func(context.Context, string, uuid.UUID, AccountCreate, CreateMeta) (CreateResult[Account], error)
}

func (stub serviceStubRepository) ListAccounts(ctx context.Context, subject string, household uuid.UUID, filter AccountListFilter, limit int, cursor *CursorPosition) ([]Account, bool, error) {
	return stub.listAccounts(ctx, subject, household, filter, limit, cursor)
}

func (stub serviceStubRepository) CreateAccount(ctx context.Context, subject string, household uuid.UUID, value AccountCreate, meta CreateMeta) (CreateResult[Account], error) {
	return stub.createAccount(ctx, subject, household, value, meta)
}

func TestServiceReadBoundsContextAndReturnsNonNilSlice(t *testing.T) {
	t.Parallel()
	service := NewService(serviceStubRepository{listAccounts: func(ctx context.Context, _ string, _ uuid.UUID, filter AccountListFilter, limit int, _ *CursorPosition) ([]Account, bool, error) {
		deadline, ok := ctx.Deadline()
		if !ok || time.Until(deadline) > readTimeout {
			t.Fatalf("read context deadline is not bounded: %v %v", deadline, ok)
		}
		if filter.State != "active" || limit != DefaultListLimit {
			t.Fatalf("normalization not applied: %#v %d", filter, limit)
		}
		return nil, false, nil
	}})
	page, err := service.ListAccounts(context.Background(), "subject", "123e4567-e89b-42d3-a456-426614174000", AccountListInput{})
	if err != nil || page.Accounts == nil || len(page.Accounts) != 0 {
		t.Fatalf("page=%#v err=%v", page, err)
	}
}

func TestServiceMutationFingerprintHeadersAndShorterDeadline(t *testing.T) {
	t.Parallel()
	input := CreateAccountInput{Name: "Cafe\u0301", Color: "#aabbcc"}
	service := NewService(serviceStubRepository{createAccount: func(ctx context.Context, _ string, _ uuid.UUID, value AccountCreate, meta CreateMeta) (CreateResult[Account], error) {
		if value.Name != "Café" || value.Color != "#AABBCC" || meta.IdempotencyKey != "key" || meta.RequestID != "request" {
			t.Fatalf("unexpected normalized create: %#v %#v", value, meta)
		}
		if meta.PayloadHash != accountPayloadHash(value) {
			t.Fatal("service passed a non-deterministic fingerprint")
		}
		deadline, ok := ctx.Deadline()
		if !ok || time.Until(deadline) > 100*time.Millisecond {
			t.Fatalf("service extended caller deadline: %v", deadline)
		}
		return CreateResult[Account]{Value: Account{ID: "ok"}}, nil
	}})
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	if _, err := service.CreateAccount(ctx, "subject", "123e4567-e89b-42d3-a456-426614174000", "key", "request", input); err != nil {
		t.Fatal(err)
	}
	if _, err := service.CreateAccount(context.Background(), "subject", "bad", "key", "request", input); !errors.Is(err, ErrNotFound) {
		t.Fatalf("malformed path error=%v", err)
	}
	if _, err := service.CreateAccount(context.Background(), "subject", "123e4567-e89b-42d3-a456-426614174000", " key", "request", input); !errors.Is(err, ErrValidation) {
		t.Fatalf("trimmed idempotency key error=%v", err)
	}
}
