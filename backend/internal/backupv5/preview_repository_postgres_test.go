package backupv5

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	_ "github.com/jackc/pgx/v5/stdlib"
)

var previewFixtureSequence atomic.Uint64

type previewDBFixture struct {
	database     *sql.DB
	repository   *PostgresPreviewRepository
	householdID  uuid.UUID
	userID       uuid.UUID
	membershipID uuid.UUID
	subject      string
}

func TestPostgresPreviewOwnerPersistsOnlyControlMetadata(t *testing.T) {
	fixture := newPreviewDBFixture(t, "owner")
	before := financialAndAuditCounts(t, fixture.database, fixture.householdID)
	entropy := make([]byte, previewTokenBytes+16)
	for index := range entropy {
		entropy[index] = byte(index + 5)
	}
	createdAt := time.Date(2026, 7, 15, 8, 0, 0, 0, time.UTC)
	service, err := NewPreviewService(
		fixture.repository,
		WithClock(fixedClock{now: createdAt}),
		WithEntropy(bytes.NewReader(entropy)),
	)
	if err != nil {
		t.Fatal(err)
	}
	_, err = service.Preview(
		context.Background(), fixture.subject, fixture.householdID,
		"2026-07-01", strings.NewReader(canonicalFixture),
	)
	if err != nil {
		t.Fatalf("Preview() error = %v", err)
	}

	var (
		id, householdID, actorID, budgetMonth, policy string
		tokenHash, digest                             []byte
		expiresAt, storedCreatedAt                    time.Time
		consumedAt, revokedAt                         sql.NullTime
	)
	err = fixture.database.QueryRow(`
		SELECT id::text, household_id::text, actor_user_id::text,
		       token_hash, backup_digest, budget_month::text, policy_version,
		       expires_at, created_at, consumed_at, revoked_at
		FROM backup_v5_import_previews
		WHERE household_id = $1
	`, fixture.householdID).Scan(
		&id, &householdID, &actorID, &tokenHash, &digest, &budgetMonth, &policy,
		&expiresAt, &storedCreatedAt, &consumedAt, &revokedAt,
	)
	if err != nil {
		t.Fatal(err)
	}
	wantHash := sha256.Sum256(entropy[:previewTokenBytes])
	wantDigest := sha256.Sum256([]byte(canonicalFixture))
	if !bytes.Equal(tokenHash, wantHash[:]) || !bytes.Equal(digest, wantDigest[:]) {
		t.Fatalf("stored hash/digest differ")
	}
	if id == "" || householdID != fixture.householdID.String() || actorID != fixture.userID.String() {
		t.Fatalf("stored binding = %s/%s/%s", id, householdID, actorID)
	}
	if budgetMonth != "2026-07-01" || policy != PolicyVersion || consumedAt.Valid || revokedAt.Valid {
		t.Fatalf("stored state = %s/%s/%v/%v", budgetMonth, policy, consumedAt, revokedAt)
	}
	if !storedCreatedAt.Equal(createdAt) || !expiresAt.Equal(createdAt.Add(DefaultPreviewTTL)) {
		t.Fatalf("stored timestamps = %s/%s", storedCreatedAt, expiresAt)
	}
	assertPreviewColumnAllowlist(t, fixture.database)
	if after := financialAndAuditCounts(t, fixture.database, fixture.householdID); after != before {
		t.Fatalf("preview mutated finance/audit: before=%v after=%v", before, after)
	}
}

func TestPostgresPreviewAuthorizationIsFailClosed(t *testing.T) {
	for _, role := range []string{"admin", "member"} {
		t.Run(role+" forbidden", func(t *testing.T) {
			fixture := newPreviewDBFixture(t, role)
			err := fixture.repository.CreatePreview(context.Background(), fixture.metadata(t), nil)
			if !errors.Is(err, ErrForbidden) {
				t.Fatalf("error = %v", err)
			}
			assertPreviewCount(t, fixture.database, fixture.householdID, 0)
		})
	}
	t.Run("missing household", func(t *testing.T) {
		fixture := newPreviewDBFixture(t, "owner")
		metadata := fixture.metadata(t)
		metadata.HouseholdID = uuid.New()
		if err := fixture.repository.CreatePreview(context.Background(), metadata, nil); !errors.Is(err, ErrNotFound) {
			t.Fatalf("error = %v", err)
		}
	})
	for _, state := range []string{"archived_at", "deleted_at"} {
		t.Run(state+" household", func(t *testing.T) {
			fixture := newPreviewDBFixture(t, "owner")
			query := `UPDATE households SET ` + state + ` = now() WHERE id = $1`
			if _, err := fixture.database.Exec(query, fixture.householdID); err != nil {
				t.Fatal(err)
			}
			if err := fixture.repository.CreatePreview(context.Background(), fixture.metadata(t), nil); !errors.Is(err, ErrNotFound) {
				t.Fatalf("error = %v", err)
			}
			assertPreviewCount(t, fixture.database, fixture.householdID, 0)
		})
	}
	t.Run("foreign actor", func(t *testing.T) {
		fixture := newPreviewDBFixture(t, "owner")
		foreign := insertPreviewUser(t, fixture.database, uuid.New().String(), false)
		metadata := fixture.metadata(t)
		metadata.ActorSubject = foreign.subject
		if err := fixture.repository.CreatePreview(context.Background(), metadata, nil); !errors.Is(err, ErrNotFound) {
			t.Fatalf("error = %v", err)
		}
	})
	t.Run("deleted actor", func(t *testing.T) {
		fixture := newPreviewDBFixture(t, "owner")
		if _, err := fixture.database.Exec(`UPDATE users SET deleted_at = now() WHERE id = $1`, fixture.userID); err != nil {
			t.Fatal(err)
		}
		if err := fixture.repository.CreatePreview(context.Background(), fixture.metadata(t), nil); !errors.Is(err, ErrNotFound) {
			t.Fatalf("error = %v", err)
		}
	})
	t.Run("inactive membership", func(t *testing.T) {
		fixture := newPreviewDBFixture(t, "owner")
		if _, err := fixture.database.Exec(`
			UPDATE household_members SET status = 'removed', removed_at = now()
			WHERE id = $1
		`, fixture.membershipID); err != nil {
			t.Fatal(err)
		}
		if err := fixture.repository.CreatePreview(context.Background(), fixture.metadata(t), nil); !errors.Is(err, ErrNotFound) {
			t.Fatalf("error = %v", err)
		}
	})
}

func TestPostgresPreviewRejectsEveryNonEmptyFinancialArea(t *testing.T) {
	cases := []struct {
		name       string
		targetFlag int
		seed       func(*testing.T, *previewDBFixture)
	}{
		{name: "accounts archived and deleted", targetFlag: presenceAccounts, seed: seedPreviewAccount},
		{name: "categories archived and deleted", targetFlag: presenceCategories, seed: seedPreviewCategory},
		{name: "transactions soft deleted", targetFlag: presenceTransactions, seed: seedPreviewTransaction},
		{name: "budgets soft deleted", targetFlag: presenceBudgets, seed: seedPreviewBudget},
		{name: "goals archived and deleted", targetFlag: presenceGoals, seed: seedPreviewGoal},
		{name: "goal contributions soft deleted", targetFlag: presenceGoalContributions, seed: seedPreviewGoalContribution},
		{name: "debts archived and deleted", targetFlag: presenceDebts, seed: seedPreviewDebt},
		{name: "debt payments soft deleted", targetFlag: presenceDebtPayments, seed: seedPreviewDebtPayment},
		{name: "recurring transactions archived and deleted", targetFlag: presenceRecurringTransactions, seed: seedPreviewRecurringTransaction},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			fixture := newPreviewDBFixture(t, "owner")
			test.seed(t, fixture)
			tx, err := fixture.database.BeginTx(context.Background(), nil)
			if err != nil {
				t.Fatal(err)
			}
			presence, err := financialPresence(context.Background(), tx, fixture.householdID)
			_ = tx.Rollback()
			if err != nil {
				t.Fatal(err)
			}
			if !presence[test.targetFlag] {
				t.Fatalf("target financial presence flag %d is false: %v", test.targetFlag, presence)
			}
			err = fixture.repository.CreatePreview(context.Background(), fixture.metadata(t), nil)
			if !errors.Is(err, ErrHouseholdNotEmpty) {
				t.Fatalf("error = %v", err)
			}
			assertPreviewCount(t, fixture.database, fixture.householdID, 0)
		})
	}
}

func TestFinancialPresenceAnyIncludesEveryFlag(t *testing.T) {
	for index := range (financialPresenceFlags{}) {
		var flags financialPresenceFlags
		flags[index] = true
		if !flags.any() {
			t.Fatalf("presence flag %d is omitted from any()", index)
		}
	}
	if (financialPresenceFlags{}).any() {
		t.Fatal("empty financial presence is non-empty")
	}
}

func TestPostgresPreviewUsesDatabaseICUCollisionSemantics(t *testing.T) {
	fixture := newPreviewDBFixture(t, "owner")
	err := fixture.repository.CreatePreview(context.Background(), fixture.metadata(t), []CategoryCandidate{
		{Type: "expense", Name: " Расход "},
		{Type: "expense", Name: "рАсХоД"},
		{Type: "income", Name: "Расход"},
	})
	if !errors.Is(err, ErrValue) {
		t.Fatalf("error = %v", err)
	}
	var validation *ValidationError
	if !errors.As(err, &validation) || validation.Code != "duplicate_category_name" || validation.Path != "categories" {
		t.Fatalf("validation error = %#v", validation)
	}
	assertPreviewCount(t, fixture.database, fixture.householdID, 0)
}

func TestPostgresPreviewConstraintCollisionAndCancellationRollback(t *testing.T) {
	t.Run("constraint rollback", func(t *testing.T) {
		fixture := newPreviewDBFixture(t, "owner")
		before := financialAndAuditCounts(t, fixture.database, fixture.householdID)
		metadata := fixture.metadata(t)
		metadata.ExpiresAt = metadata.CreatedAt
		if err := fixture.repository.CreatePreview(context.Background(), metadata, nil); !errors.Is(err, ErrRepository) {
			t.Fatalf("error = %v", err)
		}
		assertPreviewCount(t, fixture.database, fixture.householdID, 0)
		if after := financialAndAuditCounts(t, fixture.database, fixture.householdID); after != before {
			t.Fatalf("constraint failure mutated finance/audit: before=%v after=%v", before, after)
		}
	})
	t.Run("duplicate token hash", func(t *testing.T) {
		fixture := newPreviewDBFixture(t, "owner")
		before := financialAndAuditCounts(t, fixture.database, fixture.householdID)
		first := fixture.metadata(t)
		if err := fixture.repository.CreatePreview(context.Background(), first, nil); err != nil {
			t.Fatal(err)
		}
		second := fixture.metadata(t)
		second.TokenHash = first.TokenHash
		if err := fixture.repository.CreatePreview(context.Background(), second, nil); !errors.Is(err, ErrTokenCollision) {
			t.Fatalf("error = %v", err)
		}
		assertPreviewCount(t, fixture.database, fixture.householdID, 1)
		if after := financialAndAuditCounts(t, fixture.database, fixture.householdID); after != before {
			t.Fatalf("duplicate preview mutated finance/audit: before=%v after=%v", before, after)
		}
	})
	t.Run("context cancellation", func(t *testing.T) {
		fixture := newPreviewDBFixture(t, "owner")
		before := financialAndAuditCounts(t, fixture.database, fixture.householdID)
		blocker, err := fixture.database.Begin()
		if err != nil {
			t.Fatal(err)
		}
		defer blocker.Rollback()
		if _, err := blocker.Exec(`SELECT 1 FROM households WHERE id = $1 FOR UPDATE`, fixture.householdID); err != nil {
			t.Fatal(err)
		}
		ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
		defer cancel()
		err = fixture.repository.CreatePreview(ctx, fixture.metadata(t), nil)
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("error = %v", err)
		}
		assertPreviewCount(t, fixture.database, fixture.householdID, 0)
		if after := financialAndAuditCounts(t, fixture.database, fixture.householdID); after != before {
			t.Fatalf("cancellation mutated finance/audit: before=%v after=%v", before, after)
		}
	})
}

func TestPostgresPreviewHouseholdLockCompatibilityAndOrdering(t *testing.T) {
	t.Run("finance compatible key share", func(t *testing.T) {
		fixture := newPreviewDBFixture(t, "owner")
		finance, err := fixture.database.Begin()
		if err != nil {
			t.Fatal(err)
		}
		defer finance.Rollback()
		if _, err := finance.Exec(`SELECT 1 FROM households WHERE id = $1 FOR KEY SHARE`, fixture.householdID); err != nil {
			t.Fatal(err)
		}
		if _, err := finance.Exec(`
			INSERT INTO accounts (id, household_id, name, color)
			VALUES ($1, $2, 'Uncommitted finance mutation', '#112233')
		`, uuid.New(), fixture.householdID); err != nil {
			t.Fatal(err)
		}
		metadata := fixture.metadata(t)
		result := make(chan error, 1)
		go func() {
			result <- fixture.repository.CreatePreview(context.Background(), metadata, nil)
		}()
		select {
		case err := <-result:
			if err != nil {
				t.Fatalf("compatible lock error = %v", err)
			}
		case <-time.After(2 * time.Second):
			t.Fatal("preview blocked behind compatible finance household lock")
		}
	})
	t.Run("role mutation serializes then fails closed", func(t *testing.T) {
		fixture := newPreviewDBFixture(t, "owner")
		roleMutation, err := fixture.database.Begin()
		if err != nil {
			t.Fatal(err)
		}
		if _, err := roleMutation.Exec(`SELECT 1 FROM households WHERE id = $1 FOR UPDATE`, fixture.householdID); err != nil {
			t.Fatal(err)
		}
		if _, err := roleMutation.Exec(`
			UPDATE household_members SET status = 'removed', removed_at = now()
			WHERE id = $1
		`, fixture.membershipID); err != nil {
			t.Fatal(err)
		}
		metadata := fixture.metadata(t)
		result := make(chan error, 1)
		go func() {
			result <- fixture.repository.CreatePreview(context.Background(), metadata, nil)
		}()
		time.Sleep(50 * time.Millisecond)
		if err := roleMutation.Commit(); err != nil {
			t.Fatal(err)
		}
		select {
		case err := <-result:
			if !errors.Is(err, ErrNotFound) {
				t.Fatalf("serialized result = %v", err)
			}
		case <-time.After(2 * time.Second):
			t.Fatal("role/preview lock ordering deadlocked")
		}
		assertPreviewCount(t, fixture.database, fixture.householdID, 0)
	})
	t.Run("household delete serializes then fails closed", func(t *testing.T) {
		fixture := newPreviewDBFixture(t, "owner")
		deletion, err := fixture.database.Begin()
		if err != nil {
			t.Fatal(err)
		}
		if _, err := deletion.Exec(`SELECT 1 FROM households WHERE id = $1 FOR UPDATE`, fixture.householdID); err != nil {
			t.Fatal(err)
		}
		if _, err := deletion.Exec(`UPDATE households SET deleted_at = now() WHERE id = $1`, fixture.householdID); err != nil {
			t.Fatal(err)
		}
		metadata := fixture.metadata(t)
		result := make(chan error, 1)
		go func() {
			result <- fixture.repository.CreatePreview(context.Background(), metadata, nil)
		}()
		time.Sleep(50 * time.Millisecond)
		if err := deletion.Commit(); err != nil {
			t.Fatal(err)
		}
		select {
		case err := <-result:
			if !errors.Is(err, ErrNotFound) {
				t.Fatalf("serialized result = %v", err)
			}
		case <-time.After(2 * time.Second):
			t.Fatal("delete/preview lock ordering deadlocked")
		}
		assertPreviewCount(t, fixture.database, fixture.householdID, 0)
	})
}

func newPreviewDBFixture(t *testing.T, role string) *previewDBFixture {
	t.Helper()
	databaseURL := os.Getenv("TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("TEST_DATABASE_URL is not set")
	}
	database, err := sql.Open("pgx", databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = database.Close() })
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := database.PingContext(ctx); err != nil {
		t.Fatalf("ping TEST_DATABASE_URL: %v", err)
	}
	resetPreviewDatabase(t, database)
	user := insertPreviewUser(t, database, uuid.New().String(), false)
	householdID := uuid.New()
	if _, err := database.Exec(`
		INSERT INTO households (id, name, created_by_user_id)
		VALUES ($1, 'Preview fixture', $2)
	`, householdID, user.id); err != nil {
		t.Fatal(err)
	}
	membershipID := uuid.New()
	if _, err := database.Exec(`
		INSERT INTO household_members (id, household_id, user_id, role, status)
		VALUES ($1, $2, $3, $4, 'active')
	`, membershipID, householdID, user.id, role); err != nil {
		t.Fatal(err)
	}
	return &previewDBFixture{
		database: database, repository: NewPostgresPreviewRepository(database),
		householdID: householdID, userID: user.id, membershipID: membershipID, subject: user.subject,
	}
}

type previewUser struct {
	id      uuid.UUID
	subject string
}

func insertPreviewUser(t *testing.T, database *sql.DB, subject string, deleted bool) previewUser {
	t.Helper()
	user := previewUser{id: uuid.New(), subject: subject}
	var deletedAt any
	if deleted {
		deletedAt = time.Now().UTC()
	}
	if _, err := database.Exec(`
		INSERT INTO users (id, auth_subject, display_name, deleted_at)
		VALUES ($1, $2, 'Preview actor', $3)
	`, user.id, user.subject, deletedAt); err != nil {
		t.Fatal(err)
	}
	return user
}

func (fixture *previewDBFixture) metadata(t *testing.T) PreviewMetadata {
	t.Helper()
	sequence := previewFixtureSequence.Add(1)
	date, err := parseLocalDate("2026-07-01", "budgetMonth")
	if err != nil {
		t.Fatal(err)
	}
	createdAt := time.Date(2026, 7, 15, 8, 0, 0, 0, time.UTC)
	return PreviewMetadata{
		ID: uuid.New(), HouseholdID: fixture.householdID, ActorSubject: fixture.subject,
		TokenHash:    sha256.Sum256([]byte(fmt.Sprintf("token-%d", sequence))),
		BackupDigest: sha256.Sum256([]byte(fmt.Sprintf("digest-%d", sequence))),
		BudgetMonth:  date, Policy: PolicyVersion,
		CreatedAt: createdAt, ExpiresAt: createdAt.Add(DefaultPreviewTTL),
	}
}

func resetPreviewDatabase(t *testing.T, database *sql.DB) {
	t.Helper()
	_, err := database.Exec(`
		TRUNCATE TABLE
			backup_v5_import_runs,
			backup_v5_import_previews,
			audit_log,
			transactions,
			recurring_transactions,
			debt_payments,
			debts,
			goal_contributions,
			goals,
			budgets,
			categories,
			accounts,
			household_invitations,
			household_members,
			households,
			users
		RESTART IDENTITY CASCADE
	`)
	if err != nil {
		t.Fatalf("reset preview test database (migrations 00001-00005 required): %v", err)
	}
}

func assertPreviewCount(t *testing.T, database *sql.DB, householdID uuid.UUID, want int) {
	t.Helper()
	var got int
	if err := database.QueryRow(`
		SELECT count(*) FROM backup_v5_import_previews WHERE household_id = $1
	`, householdID).Scan(&got); err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Fatalf("preview count = %d, want %d", got, want)
	}
}

func assertPreviewColumnAllowlist(t *testing.T, database *sql.DB) {
	t.Helper()
	rows, err := database.Query(`
		SELECT column_name
		FROM information_schema.columns
		WHERE table_schema = 'public'
		  AND table_name = 'backup_v5_import_previews'
		ORDER BY ordinal_position
	`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	columns := make([]string, 0, 11)
	for rows.Next() {
		var column string
		if err := rows.Scan(&column); err != nil {
			t.Fatal(err)
		}
		columns = append(columns, column)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	want := []string{
		"id", "household_id", "actor_user_id", "token_hash", "backup_digest",
		"budget_month", "policy_version", "expires_at", "consumed_at", "revoked_at", "created_at",
	}
	if strings.Join(columns, ",") != strings.Join(want, ",") {
		t.Fatalf("preview table columns = %v, want %v", columns, want)
	}
}

func financialAndAuditCounts(t *testing.T, database *sql.DB, householdID uuid.UUID) [10]int {
	t.Helper()
	var counts [10]int
	if err := database.QueryRow(`
		SELECT
			(SELECT count(*) FROM accounts WHERE household_id = $1),
			(SELECT count(*) FROM categories WHERE household_id = $1),
			(SELECT count(*) FROM transactions WHERE household_id = $1),
			(SELECT count(*) FROM budgets WHERE household_id = $1),
			(SELECT count(*) FROM goals WHERE household_id = $1),
			(SELECT count(*) FROM goal_contributions WHERE household_id = $1),
			(SELECT count(*) FROM debts WHERE household_id = $1),
			(SELECT count(*) FROM debt_payments WHERE household_id = $1),
			(SELECT count(*) FROM recurring_transactions WHERE household_id = $1),
			(SELECT count(*) FROM audit_log WHERE household_id = $1)
	`, householdID).Scan(
		&counts[0], &counts[1], &counts[2], &counts[3], &counts[4],
		&counts[5], &counts[6], &counts[7], &counts[8], &counts[9],
	); err != nil {
		t.Fatal(err)
	}
	return counts
}

func seedPreviewAccount(t *testing.T, fixture *previewDBFixture) {
	t.Helper()
	_, err := fixture.database.Exec(`
		INSERT INTO accounts (id, household_id, name, color, archived_at, deleted_at)
		VALUES ($1, $2, 'Archived account', '#112233', now(), now())
	`, uuid.New(), fixture.householdID)
	if err != nil {
		t.Fatal(err)
	}
}

func seedPreviewCategory(t *testing.T, fixture *previewDBFixture) {
	t.Helper()
	_, err := fixture.database.Exec(`
		INSERT INTO categories (id, household_id, category_type, name, color, archived_at, deleted_at)
		VALUES ($1, $2, 'expense', 'Archived category', '#112233', now(), now())
	`, uuid.New(), fixture.householdID)
	if err != nil {
		t.Fatal(err)
	}
}

func seedPreviewTransaction(t *testing.T, fixture *previewDBFixture) {
	t.Helper()
	accountID, categoryID := seedPreviewTransactionReferences(t, fixture)
	_, err := fixture.database.Exec(`
		INSERT INTO transactions (
			id, household_id, transaction_type, account_id, category_id,
			amount_cents, event_date, source, deleted_at, deleted_by_user_id, deletion_reason
		) VALUES ($1, $2, 'expense', $3, $4, 1, '2026-07-01', 'manual', now(), $5, 'fixture')
	`, uuid.New(), fixture.householdID, accountID, categoryID, fixture.userID)
	if err != nil {
		t.Fatal(err)
	}
}

func seedPreviewBudget(t *testing.T, fixture *previewDBFixture) {
	t.Helper()
	categoryID := seedPreviewExpenseCategory(t, fixture)
	_, err := fixture.database.Exec(`
		INSERT INTO budgets (id, household_id, category_id, budget_month, amount_cents, deleted_at)
		VALUES ($1, $2, $3, '2026-07-01', 1, now())
	`, uuid.New(), fixture.householdID, categoryID)
	if err != nil {
		t.Fatal(err)
	}
}

func seedPreviewGoal(t *testing.T, fixture *previewDBFixture) {
	t.Helper()
	_, err := fixture.database.Exec(`
		INSERT INTO goals (id, household_id, name, target_amount_cents, color, archived_at, deleted_at)
		VALUES ($1, $2, 'Archived goal', 1, '#112233', now(), now())
	`, uuid.New(), fixture.householdID)
	if err != nil {
		t.Fatal(err)
	}
}

func seedPreviewGoalContribution(t *testing.T, fixture *previewDBFixture) {
	t.Helper()
	goalID := uuid.New()
	if _, err := fixture.database.Exec(`
		INSERT INTO goals (id, household_id, name, target_amount_cents, color)
		VALUES ($1, $2, 'Goal parent', 1, '#112233')
	`, goalID, fixture.householdID); err != nil {
		t.Fatal(err)
	}
	_, err := fixture.database.Exec(`
		INSERT INTO goal_contributions (id, household_id, goal_id, amount_cents, event_date, deleted_at)
		VALUES ($1, $2, $3, 1, '2026-07-01', now())
	`, uuid.New(), fixture.householdID, goalID)
	if err != nil {
		t.Fatal(err)
	}
}

func seedPreviewDebt(t *testing.T, fixture *previewDBFixture) {
	t.Helper()
	_, err := fixture.database.Exec(`
		INSERT INTO debts (id, household_id, person_label, direction, original_amount_cents, archived_at, deleted_at)
		VALUES ($1, $2, 'Archived debt', 'i_owe', 1, now(), now())
	`, uuid.New(), fixture.householdID)
	if err != nil {
		t.Fatal(err)
	}
}

func seedPreviewDebtPayment(t *testing.T, fixture *previewDBFixture) {
	t.Helper()
	debtID := uuid.New()
	if _, err := fixture.database.Exec(`
		INSERT INTO debts (id, household_id, person_label, direction, original_amount_cents)
		VALUES ($1, $2, 'Debt parent', 'i_owe', 1)
	`, debtID, fixture.householdID); err != nil {
		t.Fatal(err)
	}
	_, err := fixture.database.Exec(`
		INSERT INTO debt_payments (id, household_id, debt_id, amount_cents, event_date, deleted_at)
		VALUES ($1, $2, $3, 1, '2026-07-01', now())
	`, uuid.New(), fixture.householdID, debtID)
	if err != nil {
		t.Fatal(err)
	}
}

func seedPreviewRecurringTransaction(t *testing.T, fixture *previewDBFixture) {
	t.Helper()
	accountID, categoryID := seedPreviewTransactionReferences(t, fixture)
	_, err := fixture.database.Exec(`
		INSERT INTO recurring_transactions (
			id, household_id, transaction_type, account_id, category_id,
			amount_cents, frequency, next_execution_date, archived_at, deleted_at
		) VALUES ($1, $2, 'expense', $3, $4, 1, 'monthly', '2026-07-01', now(), now())
	`, uuid.New(), fixture.householdID, accountID, categoryID)
	if err != nil {
		t.Fatal(err)
	}
}

func seedPreviewTransactionReferences(t *testing.T, fixture *previewDBFixture) (uuid.UUID, uuid.UUID) {
	t.Helper()
	accountID := uuid.New()
	if _, err := fixture.database.Exec(`
		INSERT INTO accounts (id, household_id, name, color)
		VALUES ($1, $2, 'Reference account', '#112233')
	`, accountID, fixture.householdID); err != nil {
		t.Fatal(err)
	}
	return accountID, seedPreviewExpenseCategory(t, fixture)
}

func seedPreviewExpenseCategory(t *testing.T, fixture *previewDBFixture) uuid.UUID {
	t.Helper()
	categoryID := uuid.New()
	if _, err := fixture.database.Exec(`
		INSERT INTO categories (id, household_id, category_type, name, color)
		VALUES ($1, $2, 'expense', $3, '#112233')
	`, categoryID, fixture.householdID, "Expense "+categoryID.String()); err != nil {
		t.Fatal(err)
	}
	return categoryID
}
