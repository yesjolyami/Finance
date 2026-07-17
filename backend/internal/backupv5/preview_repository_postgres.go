package backupv5

import (
	"context"
	"database/sql"
	"errors"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
)

type PostgresPreviewRepository struct {
	database *sql.DB
}

var _ Repository = (*PostgresPreviewRepository)(nil)

func NewPostgresPreviewRepository(database *sql.DB) *PostgresPreviewRepository {
	return &PostgresPreviewRepository{database: database}
}

// CreatePreview uses the same household-first lock order as finance mutations:
// household (KEY SHARE), actor membership, authoritative reads, preview insert.
func (repository *PostgresPreviewRepository) CreatePreview(
	ctx context.Context,
	metadata PreviewMetadata,
	categories []CategoryCandidate,
) error {
	if repository == nil || repository.database == nil {
		return ErrRepository
	}
	tx, err := repository.database.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelReadCommitted})
	if err != nil {
		return repositoryError(ctx, err)
	}
	defer func() { _ = tx.Rollback() }()

	var lockedHousehold string
	err = tx.QueryRowContext(ctx, `
		SELECT id::text
		FROM households
		WHERE id = $1
		  AND archived_at IS NULL
		  AND deleted_at IS NULL
		FOR KEY SHARE
	`, metadata.HouseholdID).Scan(&lockedHousehold)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return repositoryError(ctx, err)
	}

	var actorID string
	var role string
	err = tx.QueryRowContext(ctx, `
		SELECT u.id::text, hm.role
		FROM users AS u
		JOIN household_members AS hm
		  ON hm.user_id = u.id
		 AND hm.household_id = $2
		 AND hm.status = 'active'
		WHERE u.auth_subject = $1
		  AND u.deleted_at IS NULL
		FOR KEY SHARE OF u, hm
	`, metadata.ActorSubject, metadata.HouseholdID).Scan(&actorID, &role)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return repositoryError(ctx, err)
	}
	if role != "owner" {
		return ErrForbidden
	}

	presence, err := financialPresence(ctx, tx, metadata.HouseholdID)
	if err != nil {
		return err
	}
	if presence.any() {
		return ErrHouseholdNotEmpty
	}

	if err := detectCategoryCollision(ctx, tx, categories); err != nil {
		return err
	}

	_, err = tx.ExecContext(ctx, `
		INSERT INTO backup_v5_import_previews (
			id,
			household_id,
			actor_user_id,
			token_hash,
			backup_digest,
			budget_month,
			policy_version,
			expires_at,
			created_at
		) VALUES ($1, $2, $3::uuid, $4, $5, $6, $7, $8, $9)
	`,
		metadata.ID,
		metadata.HouseholdID,
		actorID,
		metadata.TokenHash[:],
		metadata.BackupDigest[:],
		metadata.BudgetMonth.String(),
		metadata.Policy,
		metadata.ExpiresAt,
		metadata.CreatedAt,
	)
	if err != nil {
		return repositoryError(ctx, err)
	}
	if err := tx.Commit(); err != nil {
		return repositoryError(ctx, err)
	}
	return nil
}

type financialPresenceFlags [9]bool

const (
	presenceAccounts = iota
	presenceCategories
	presenceTransactions
	presenceBudgets
	presenceGoals
	presenceGoalContributions
	presenceDebts
	presenceDebtPayments
	presenceRecurringTransactions
)

func (flags financialPresenceFlags) any() bool {
	for _, present := range flags {
		if present {
			return true
		}
	}
	return false
}

func financialPresence(ctx context.Context, tx *sql.Tx, householdID uuid.UUID) (financialPresenceFlags, error) {
	var flags financialPresenceFlags
	err := tx.QueryRowContext(ctx, `
		SELECT
			EXISTS (SELECT 1 FROM accounts WHERE household_id = $1),
			EXISTS (SELECT 1 FROM categories WHERE household_id = $1),
			EXISTS (SELECT 1 FROM transactions WHERE household_id = $1),
			EXISTS (SELECT 1 FROM budgets WHERE household_id = $1),
			EXISTS (SELECT 1 FROM goals WHERE household_id = $1),
			EXISTS (SELECT 1 FROM goal_contributions WHERE household_id = $1),
			EXISTS (SELECT 1 FROM debts WHERE household_id = $1),
			EXISTS (SELECT 1 FROM debt_payments WHERE household_id = $1),
			EXISTS (SELECT 1 FROM recurring_transactions WHERE household_id = $1)
	`, householdID).Scan(
		&flags[presenceAccounts],
		&flags[presenceCategories],
		&flags[presenceTransactions],
		&flags[presenceBudgets],
		&flags[presenceGoals],
		&flags[presenceGoalContributions],
		&flags[presenceDebts],
		&flags[presenceDebtPayments],
		&flags[presenceRecurringTransactions],
	)
	if err != nil {
		return financialPresenceFlags{}, repositoryError(ctx, err)
	}
	return flags, nil
}

func detectCategoryCollision(ctx context.Context, tx *sql.Tx, categories []CategoryCandidate) error {
	if len(categories) < 2 {
		return nil
	}
	types := make([]string, len(categories))
	names := make([]string, len(categories))
	for index, category := range categories {
		if err := ctx.Err(); err != nil {
			return err
		}
		types[index] = category.Type
		names[index] = category.Name
	}
	var collision bool
	err := tx.QueryRowContext(ctx, `
		SELECT EXISTS (
			SELECT 1
			FROM unnest($1::text[], $2::text[]) AS candidate(category_type, category_name)
			GROUP BY category_type, (btrim(category_name) COLLATE finance_category_name_ci)
			HAVING count(*) > 1
		)
	`, types, names).Scan(&collision)
	if err != nil {
		return repositoryError(ctx, err)
	}
	if collision {
		return validationError(ErrValue, "duplicate_category_name", "categories")
	}
	return nil
}

func repositoryError(ctx context.Context, err error) error {
	if ctxErr := ctx.Err(); ctxErr != nil {
		return ctxErr
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return err
	}
	var postgresError *pgconn.PgError
	if errors.As(err, &postgresError) && postgresError.Code == "23505" {
		return ErrTokenCollision
	}
	return ErrRepository
}
