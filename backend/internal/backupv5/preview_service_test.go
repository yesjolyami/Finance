package backupv5

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
)

const previewActorSubject = "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa"

type fixedClock struct{ now time.Time }

func (clock fixedClock) Now() time.Time { return clock.now }

type capturingPreviewRepository struct {
	metadata   PreviewMetadata
	categories []CategoryCandidate
	err        error
	calls      int
}

func (repository *capturingPreviewRepository) CreatePreview(
	_ context.Context,
	metadata PreviewMetadata,
	categories []CategoryCandidate,
) error {
	repository.calls++
	repository.metadata = metadata
	repository.categories = append([]CategoryCandidate(nil), categories...)
	return repository.err
}

func TestPreviewCreatesOneTimeTokenAndSafeResponse(t *testing.T) {
	now := time.Date(2026, 7, 15, 9, 30, 0, 123456000, time.FixedZone("test", 3*60*60))
	entropy := make([]byte, previewTokenBytes+16)
	for index := range entropy {
		entropy[index] = byte(index)
	}
	repository := &capturingPreviewRepository{}
	service, err := NewPreviewService(
		repository,
		WithClock(fixedClock{now: now}),
		WithEntropy(bytes.NewReader(entropy)),
	)
	if err != nil {
		t.Fatal(err)
	}
	raw := []byte(canonicalFixture)
	response, err := service.Preview(
		context.Background(),
		previewActorSubject,
		fixtureInput.HouseholdID,
		fixtureInput.BudgetMonth,
		bytes.NewReader(raw),
	)
	if err != nil {
		t.Fatalf("Preview() error = %v", err)
	}

	wantToken := base64.RawURLEncoding.EncodeToString(entropy[:previewTokenBytes])
	if response.ConfirmationToken != wantToken || strings.Contains(response.ConfirmationToken, "=") {
		t.Fatalf("confirmation token is not exact unpadded base64url")
	}
	if repository.metadata.TokenHash != sha256.Sum256(entropy[:previewTokenBytes]) {
		t.Fatalf("repository token hash differs")
	}
	if strings.Contains(string(repository.metadata.TokenHash[:]), response.ConfirmationToken) {
		t.Fatalf("repository received raw token")
	}
	wantIDBytes := append([]byte(nil), entropy[previewTokenBytes:]...)
	wantIDBytes[6] = (wantIDBytes[6] & 0x0f) | 0x40
	wantIDBytes[8] = (wantIDBytes[8] & 0x3f) | 0x80
	wantID, _ := uuid.FromBytes(wantIDBytes)
	if repository.metadata.ID != wantID {
		t.Fatalf("preview id = %s, want %s", repository.metadata.ID, wantID)
	}
	if repository.metadata.CreatedAt != now.UTC() || repository.metadata.ExpiresAt != now.UTC().Add(DefaultPreviewTTL) {
		t.Fatalf("preview timestamps = %s / %s", repository.metadata.CreatedAt, repository.metadata.ExpiresAt)
	}
	if repository.metadata.Policy != PolicyVersion || repository.metadata.ActorSubject != previewActorSubject {
		t.Fatalf("binding metadata is incomplete")
	}
	if len(repository.categories) != 2 || repository.categories[1].Name != "Café" {
		t.Fatalf("normalized category candidates = %#v", repository.categories)
	}
	if response.Warnings == nil || len(response.Warnings) != 6 {
		t.Fatalf("warnings = %#v", response.Warnings)
	}
	if response.Counts.GoalContributions != 0 || response.Counts.DebtPayments != 2 {
		t.Fatalf("response counts = %#v", response.Counts)
	}
	if response.Totals.HouseholdBalanceCents != "650" || response.Totals.TransferCents != "200" {
		t.Fatalf("response totals = %#v", response.Totals)
	}

	for index := range raw {
		raw[index] = 'x'
	}
	if response.Counts.Accounts != 2 || response.BackupDigest == "" {
		t.Fatalf("response retained mutable raw input")
	}
}

func TestPreviewResponseJSONUsesExactAllowlist(t *testing.T) {
	repository := &capturingPreviewRepository{}
	service, err := NewPreviewService(repository, WithEntropy(bytes.NewReader(make([]byte, previewTokenBytes+16))))
	if err != nil {
		t.Fatal(err)
	}
	response, err := service.Preview(
		context.Background(), previewActorSubject, fixtureInput.HouseholdID,
		fixtureInput.BudgetMonth, strings.NewReader(canonicalFixture),
	)
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := json.Marshal(response)
	if err != nil {
		t.Fatal(err)
	}
	var object map[string]json.RawMessage
	if err := json.Unmarshal(encoded, &object); err != nil {
		t.Fatal(err)
	}
	assertJSONKeys(t, object, []string{
		"backupDigest", "budgetMonth", "confirmationToken", "counts", "expiresAt", "totals", "warnings",
	})
	var counts map[string]json.RawMessage
	if err := json.Unmarshal(object["counts"], &counts); err != nil {
		t.Fatal(err)
	}
	assertJSONKeys(t, counts, []string{
		"accounts", "budgets", "categories", "debtPayments", "debts", "goalContributions", "goals", "transactions",
	})
	var totals map[string]json.RawMessage
	if err := json.Unmarshal(object["totals"], &totals); err != nil {
		t.Fatal(err)
	}
	assertJSONKeys(t, totals, []string{
		"expenseCents", "householdBalanceCents", "incomeCents", "transferCents",
	})
	var warnings []map[string]json.RawMessage
	if err := json.Unmarshal(object["warnings"], &warnings); err != nil {
		t.Fatal(err)
	}
	for _, warning := range warnings {
		assertJSONKeys(t, warning, []string{"code", "count"})
	}
	for _, forbidden := range []string{
		`"id"`, `"name"`, `"note"`, `"raw"`, `"model"`, "Main account", "Groceries", "Legacy label",
	} {
		if strings.Contains(string(encoded), forbidden) {
			t.Fatalf("response exposes forbidden field/content %q: %s", forbidden, encoded)
		}
	}
}

func TestPreviewServiceConfigurationBoundaries(t *testing.T) {
	repository := &capturingPreviewRepository{}
	for _, test := range []struct {
		name    string
		options []ServiceOption
		wantErr bool
	}{
		{name: "default"},
		{name: "positive", options: []ServiceOption{WithPreviewTTL(time.Nanosecond)}},
		{name: "maximum", options: []ServiceOption{WithPreviewTTL(MaximumPreviewTTL)}},
		{name: "zero", options: []ServiceOption{WithPreviewTTL(0)}, wantErr: true},
		{name: "negative", options: []ServiceOption{WithPreviewTTL(-time.Second)}, wantErr: true},
		{name: "too long", options: []ServiceOption{WithPreviewTTL(MaximumPreviewTTL + time.Nanosecond)}, wantErr: true},
		{name: "nil option", options: []ServiceOption{nil}, wantErr: true},
		{name: "nil clock", options: []ServiceOption{WithClock(nil)}, wantErr: true},
		{name: "nil entropy", options: []ServiceOption{WithEntropy(nil)}, wantErr: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			_, err := NewPreviewService(repository, test.options...)
			if test.wantErr != errors.Is(err, ErrInvalidService) {
				t.Fatalf("NewPreviewService() error = %v", err)
			}
		})
	}
	if _, err := NewPreviewService(nil); !errors.Is(err, ErrInvalidService) {
		t.Fatalf("nil repository error = %v", err)
	}
}

func TestPreviewFailuresAreSafeAndOrdered(t *testing.T) {
	t.Run("parser before entropy and repository", func(t *testing.T) {
		repository := &capturingPreviewRepository{}
		service, err := NewPreviewService(repository, WithEntropy(strings.NewReader("")))
		if err != nil {
			t.Fatal(err)
		}
		_, err = service.Preview(context.Background(), previewActorSubject, fixtureInput.HouseholdID, fixtureInput.BudgetMonth, strings.NewReader(`{"version":5}`))
		if !errors.Is(err, ErrSchema) || repository.calls != 0 {
			t.Fatalf("error/calls = %v/%d", err, repository.calls)
		}
	})
	t.Run("entropy", func(t *testing.T) {
		repository := &capturingPreviewRepository{}
		service, _ := NewPreviewService(repository, WithEntropy(io.LimitReader(strings.NewReader("secret backup content"), 1)))
		_, err := service.Preview(context.Background(), previewActorSubject, fixtureInput.HouseholdID, fixtureInput.BudgetMonth, strings.NewReader(canonicalFixture))
		if !errors.Is(err, ErrEntropy) || repository.calls != 0 || strings.Contains(err.Error(), "secret") {
			t.Fatalf("unsafe entropy error/calls = %v/%d", err, repository.calls)
		}
	})
	t.Run("known repository error propagates", func(t *testing.T) {
		repository := &capturingPreviewRepository{err: fmt.Errorf("secret wrapper: %w", ErrForbidden)}
		service, _ := NewPreviewService(repository, WithEntropy(bytes.NewReader(make([]byte, previewTokenBytes+16))))
		_, err := service.Preview(context.Background(), previewActorSubject, fixtureInput.HouseholdID, fixtureInput.BudgetMonth, strings.NewReader(canonicalFixture))
		if !errors.Is(err, ErrForbidden) || strings.Contains(err.Error(), "secret") {
			t.Fatalf("error = %v", err)
		}
	})
	t.Run("unknown repository error sanitized", func(t *testing.T) {
		repository := &capturingPreviewRepository{err: errors.New("SQL failed with secret names and token")}
		service, _ := NewPreviewService(repository, WithEntropy(bytes.NewReader(make([]byte, previewTokenBytes+16))))
		_, err := service.Preview(context.Background(), previewActorSubject, fixtureInput.HouseholdID, fixtureInput.BudgetMonth, strings.NewReader(canonicalFixture))
		if !errors.Is(err, ErrRepository) || strings.Contains(err.Error(), "secret") || strings.Contains(err.Error(), "token") {
			t.Fatalf("unsafe repository error = %v", err)
		}
	})
	t.Run("invalid identity is opaque", func(t *testing.T) {
		repository := &capturingPreviewRepository{}
		service, _ := NewPreviewService(repository)
		_, err := service.Preview(context.Background(), "invalid", fixtureInput.HouseholdID, fixtureInput.BudgetMonth, strings.NewReader(canonicalFixture))
		if !errors.Is(err, ErrNotFound) || repository.calls != 0 {
			t.Fatalf("error/calls = %v/%d", err, repository.calls)
		}
	})
}

func TestPreviewContextCancellationIsPreserved(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	repository := &capturingPreviewRepository{}
	service, _ := NewPreviewService(repository)
	_, err := service.Preview(ctx, previewActorSubject, fixtureInput.HouseholdID, fixtureInput.BudgetMonth, strings.NewReader(canonicalFixture))
	if !errors.Is(err, context.Canceled) || repository.calls != 0 {
		t.Fatalf("error/calls = %v/%d", err, repository.calls)
	}
}

func assertJSONKeys(t *testing.T, object map[string]json.RawMessage, want []string) {
	t.Helper()
	got := make([]string, 0, len(object))
	for key := range object {
		got = append(got, key)
	}
	slicesSort(got)
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("JSON keys = %v, want %v", got, want)
	}
}

func slicesSort(values []string) {
	for index := 1; index < len(values); index++ {
		for current := index; current > 0 && values[current] < values[current-1]; current-- {
			values[current], values[current-1] = values[current-1], values[current]
		}
	}
}
