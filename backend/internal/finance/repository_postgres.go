package finance

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"math"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
)

// Mutation lock order is invariant across this package:
// household row (KEY SHARE), actor user/membership, target, then references.
// Multiple account references are always locked in UUID order. Household role
// mutations lock the household first, so the shared prefix prevents deadlocks.
type PostgresRepository struct {
	database *sql.DB
}

var _ Repository = (*PostgresRepository)(nil)

func NewPostgresRepository(database *sql.DB) *PostgresRepository {
	return &PostgresRepository{database: database}
}

type actor struct {
	ID   uuid.UUID
	Role string
}

type scanner interface {
	Scan(...any) error
}

func (repository *PostgresRepository) beginMutation(ctx context.Context, subject string, householdID uuid.UUID) (*sql.Tx, actor, error) {
	tx, err := repository.database.BeginTx(ctx, nil)
	if err != nil {
		return nil, actor{}, err
	}
	var locked uuid.UUID
	if err := tx.QueryRowContext(ctx, `
		SELECT id FROM households
		WHERE id = $1 AND deleted_at IS NULL AND archived_at IS NULL
		FOR KEY SHARE
	`, householdID).Scan(&locked); err != nil {
		tx.Rollback()
		if errors.Is(err, sql.ErrNoRows) {
			return nil, actor{}, ErrNotFound
		}
		return nil, actor{}, err
	}
	var value actor
	if err := tx.QueryRowContext(ctx, `
		SELECT u.id, m.role
		FROM users u
		JOIN household_members m
		  ON m.user_id = u.id AND m.household_id = $2 AND m.status = 'active'
		WHERE u.auth_subject = $1 AND u.deleted_at IS NULL
		FOR UPDATE OF u, m
	`, subject, householdID).Scan(&value.ID, &value.Role); err != nil {
		tx.Rollback()
		if errors.Is(err, sql.ErrNoRows) {
			return nil, actor{}, ErrNotFound
		}
		return nil, actor{}, err
	}
	return tx, value, nil
}

func (repository *PostgresRepository) beginRead(ctx context.Context, subject string, householdID uuid.UUID) (*sql.Tx, error) {
	tx, err := repository.database.BeginTx(ctx, &sql.TxOptions{ReadOnly: true, Isolation: sql.LevelRepeatableRead})
	if err != nil {
		return nil, err
	}
	var exists bool
	err = tx.QueryRowContext(ctx, `
		SELECT EXISTS (
			SELECT 1 FROM users u
			JOIN household_members m
			  ON m.user_id = u.id AND m.household_id = $2 AND m.status = 'active'
			JOIN households h
			  ON h.id = m.household_id AND h.deleted_at IS NULL AND h.archived_at IS NULL
			WHERE u.auth_subject = $1 AND u.deleted_at IS NULL
		)
	`, subject, householdID).Scan(&exists)
	if err != nil || !exists {
		tx.Rollback()
		if err != nil {
			return nil, err
		}
		return nil, ErrNotFound
	}
	return tx, nil
}

func requireManager(value actor) error {
	if value.Role != "owner" && value.Role != "admin" {
		return ErrForbidden
	}
	return nil
}

func insertFinanceAudit(ctx context.Context, tx *sql.Tx, householdID, actorID, entityID uuid.UUID, entityType, action, requestID string, fields []string) error {
	changes, err := json.Marshal(struct {
		Source string   `json:"source"`
		Fields []string `json:"fields"`
	}{Source: "api", Fields: fields})
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `
		INSERT INTO audit_log (
			id, household_id, actor_user_id, entity_type, entity_id,
			action, request_id, changes
		) VALUES (gen_random_uuid(), $1, $2, $3, $4, $5, $6, $7)
	`, householdID, actorID, entityType, entityID, action, requestID, changes)
	return err
}

func mapPostgresError(err error) error {
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) {
		return err
	}
	switch pgErr.Code {
	case "23505":
		return ErrConflict
	case "23503":
		return ErrNotFound
	case "23514", "22003":
		return ErrValidation
	default:
		return err
	}
}

func ensureVersion(current int64, expected int64) error {
	if current != expected {
		return ErrVersionConflict
	}
	if current == math.MaxInt64 {
		return ErrVersionExhausted
	}
	return nil
}

func lockOwnerReference(ctx context.Context, tx *sql.Tx, householdID uuid.UUID, ownerID *uuid.UUID) error {
	if ownerID == nil {
		return nil
	}
	var locked uuid.UUID
	err := tx.QueryRowContext(ctx, `
		SELECT m.user_id
		FROM household_members m
		JOIN users u ON u.id = m.user_id AND u.deleted_at IS NULL
		WHERE m.household_id = $1 AND m.user_id = $2 AND m.status = 'active'
		FOR UPDATE OF m, u
	`, householdID, *ownerID).Scan(&locked)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrNotFound
	}
	return err
}

func scanAccount(row scanner) (Account, error) {
	var value Account
	var owner sql.NullString
	err := row.Scan(
		&value.ID, &value.Name, &value.Color, &value.SortOrder, &value.AccountType,
		&value.BankLabel, &value.LegacyOwnerLabel, &owner, &value.CurrencyCode,
		&value.IsSystem, &value.Version, &value.CreatedAt, &value.UpdatedAt, &value.ArchivedAt,
	)
	if owner.Valid {
		value.OwnerUserID = &owner.String
	}
	return value, err
}

const accountColumns = `
	a.id::text, a.name, a.color, a.sort_order, a.account_type,
	a.bank_label, a.legacy_owner_label, a.owner_user_id::text, a.currency_code,
	a.is_system, a.version::text, a.created_at, a.updated_at, a.archived_at`

func (repository *PostgresRepository) ListAccounts(ctx context.Context, subject string, householdID uuid.UUID, filter AccountListFilter, limit int, cursor *CursorPosition) ([]Account, bool, error) {
	tx, err := repository.beginRead(ctx, subject, householdID)
	if err != nil {
		return nil, false, err
	}
	defer tx.Rollback()
	var cursorTime any
	var cursorID any
	if cursor != nil {
		cursorTime, cursorID = cursor.CreatedAt, cursor.ID
	}
	rows, err := tx.QueryContext(ctx, `
		SELECT `+accountColumns+`
		FROM accounts a
		WHERE a.household_id = $1 AND a.deleted_at IS NULL
		  AND ($2 = 'all' OR ($2 = 'active' AND a.archived_at IS NULL)
		       OR ($2 = 'archived' AND a.archived_at IS NOT NULL))
		  AND ($3::timestamptz IS NULL OR (a.created_at, a.id) < ($3, $4::uuid))
		ORDER BY a.created_at DESC, a.id DESC
		LIMIT $5
	`, householdID, filter.State, cursorTime, cursorID, limit+1)
	if err != nil {
		return nil, false, err
	}
	defer rows.Close()
	items := make([]Account, 0, limit)
	for rows.Next() {
		value, scanErr := scanAccount(rows)
		if scanErr != nil {
			return nil, false, scanErr
		}
		items = append(items, value)
	}
	if err := rows.Err(); err != nil {
		return nil, false, err
	}
	more := len(items) > limit
	if more {
		items = items[:limit]
	}
	return items, more, tx.Commit()
}

func (repository *PostgresRepository) CreateAccount(ctx context.Context, subject string, householdID uuid.UUID, input AccountCreate, meta CreateMeta) (CreateResult[Account], error) {
	tx, actorValue, err := repository.beginMutation(ctx, subject, householdID)
	if err != nil {
		return CreateResult[Account]{}, err
	}
	defer tx.Rollback()
	if err := requireManager(actorValue); err != nil {
		return CreateResult[Account]{}, err
	}
	if replay, found, err := lookupAccountReplay(ctx, tx, householdID, meta); found || err != nil {
		if err == nil {
			err = tx.Commit()
		}
		return CreateResult[Account]{Value: replay, Replayed: found}, err
	}
	if err := lockOwnerReference(ctx, tx, householdID, input.OwnerUserID); err != nil {
		return CreateResult[Account]{}, err
	}
	id := uuid.New()
	row := tx.QueryRowContext(ctx, `
		INSERT INTO accounts (
			id, household_id, name, color, sort_order, account_type, bank_label,
			legacy_owner_label, owner_user_id, creation_idempotency_key, creation_payload_hash
		) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11)
		ON CONFLICT (household_id, creation_idempotency_key)
		WHERE creation_idempotency_key IS NOT NULL DO NOTHING
		RETURNING `+accountColumnsForReturning()+`
	`, id, householdID, input.Name, input.Color, input.SortOrder, input.AccountType,
		input.BankLabel, input.LegacyOwnerLabel, input.OwnerUserID, meta.IdempotencyKey, meta.PayloadHash[:])
	value, err := scanAccount(row)
	if errors.Is(err, sql.ErrNoRows) {
		value, _, err = lookupAccountReplay(ctx, tx, householdID, meta)
		if err == nil {
			err = tx.Commit()
		}
		return CreateResult[Account]{Value: value, Replayed: err == nil}, err
	}
	if err != nil {
		return CreateResult[Account]{}, mapPostgresError(err)
	}
	if err := insertFinanceAudit(ctx, tx, householdID, actorValue.ID, id, "accounts", "created", meta.RequestID, []string{"created"}); err != nil {
		return CreateResult[Account]{}, err
	}
	if err := tx.Commit(); err != nil {
		return CreateResult[Account]{}, err
	}
	return CreateResult[Account]{Value: value}, nil
}

func accountColumnsForReturning() string {
	return `id::text, name, color, sort_order, account_type, bank_label,
		legacy_owner_label, owner_user_id::text, currency_code, is_system,
		version::text, created_at, updated_at, archived_at`
}

func lookupAccountReplay(ctx context.Context, tx *sql.Tx, householdID uuid.UUID, meta CreateMeta) (Account, bool, error) {
	var hash []byte
	var id uuid.UUID
	err := tx.QueryRowContext(ctx, `
		SELECT id, creation_payload_hash FROM accounts
		WHERE household_id = $1 AND creation_idempotency_key = $2
		FOR UPDATE
	`, householdID, meta.IdempotencyKey).Scan(&id, &hash)
	if errors.Is(err, sql.ErrNoRows) {
		return Account{}, false, nil
	}
	if err != nil {
		return Account{}, false, err
	}
	if !bytes.Equal(hash, meta.PayloadHash[:]) {
		return Account{}, true, ErrIdempotency
	}
	value, err := scanAccount(tx.QueryRowContext(ctx, `SELECT `+accountColumns+` FROM accounts a WHERE a.id = $1`, id))
	return value, true, err
}

func (repository *PostgresRepository) UpdateAccount(ctx context.Context, subject string, householdID, accountID uuid.UUID, patch AccountPatch, meta MutationMeta) (Account, error) {
	tx, actorValue, err := repository.beginMutation(ctx, subject, householdID)
	if err != nil {
		return Account{}, err
	}
	defer tx.Rollback()
	if err := requireManager(actorValue); err != nil {
		return Account{}, err
	}
	current, currentVersion, ownerID, err := lockAccount(ctx, tx, householdID, accountID)
	if err != nil {
		return Account{}, err
	}
	if current.IsSystem {
		return Account{}, ErrSystemImmutable
	}
	if err := ensureVersion(currentVersion, meta.ExpectedVersion); err != nil {
		return Account{}, err
	}
	applyAccountPatch(&current, patch)
	if patch.OwnerUserID.Present {
		ownerID = patch.OwnerUserID.Value
		if err := lockOwnerReference(ctx, tx, householdID, ownerID); err != nil {
			return Account{}, err
		}
	}
	value, err := scanAccount(tx.QueryRowContext(ctx, `
		UPDATE accounts SET name=$3,color=$4,sort_order=$5,account_type=$6,
		 bank_label=$7,legacy_owner_label=$8,owner_user_id=$9,version=version+1
		WHERE household_id=$1 AND id=$2 AND version=$10
		RETURNING `+accountColumnsForReturning()+`
	`, householdID, accountID, current.Name, current.Color, current.SortOrder, current.AccountType,
		current.BankLabel, current.LegacyOwnerLabel, ownerID, meta.ExpectedVersion))
	if err != nil {
		return Account{}, mapPostgresError(err)
	}
	if err := insertFinanceAudit(ctx, tx, householdID, actorValue.ID, accountID, "accounts", "updated", meta.RequestID, accountPatchFields(patch)); err != nil {
		return Account{}, err
	}
	return value, commitValue(tx)
}

func (repository *PostgresRepository) SetAccountArchived(ctx context.Context, subject string, householdID, accountID uuid.UUID, archived bool, meta MutationMeta) (Account, error) {
	tx, actorValue, err := repository.beginMutation(ctx, subject, householdID)
	if err != nil {
		return Account{}, err
	}
	defer tx.Rollback()
	if err := requireManager(actorValue); err != nil {
		return Account{}, err
	}
	current, version, _, err := lockAccount(ctx, tx, householdID, accountID)
	if err != nil {
		return Account{}, err
	}
	if current.IsSystem {
		return Account{}, ErrSystemImmutable
	}
	if err := ensureVersion(version, meta.ExpectedVersion); err != nil {
		return Account{}, err
	}
	if (current.ArchivedAt != nil) == archived {
		return Account{}, ErrConflict
	}
	value, err := scanAccount(tx.QueryRowContext(ctx, `
		UPDATE accounts SET archived_at=CASE WHEN $3 THEN CURRENT_TIMESTAMP ELSE NULL END,
		 version=version+1 WHERE household_id=$1 AND id=$2 AND version=$4
		RETURNING `+accountColumnsForReturning()+`
	`, householdID, accountID, archived, meta.ExpectedVersion))
	if err != nil {
		return Account{}, mapPostgresError(err)
	}
	action := "restored"
	if archived {
		action = "archived"
	}
	if err := insertFinanceAudit(ctx, tx, householdID, actorValue.ID, accountID, "accounts", action, meta.RequestID, []string{"archivedAt"}); err != nil {
		return Account{}, err
	}
	return value, commitValue(tx)
}

func lockAccount(ctx context.Context, tx *sql.Tx, householdID, accountID uuid.UUID) (Account, int64, *uuid.UUID, error) {
	var version int64
	var ownerID *uuid.UUID
	value, err := scanAccountAndInternal(tx.QueryRowContext(ctx, `SELECT `+accountColumns+`, a.version, a.owner_user_id FROM accounts a WHERE a.household_id=$1 AND a.id=$2 AND a.deleted_at IS NULL FOR UPDATE`, householdID, accountID), &version, &ownerID)
	if errors.Is(err, sql.ErrNoRows) {
		return Account{}, 0, nil, ErrNotFound
	}
	return value, version, ownerID, err
}

func scanAccountAndInternal(row scanner, version *int64, ownerID **uuid.UUID) (Account, error) {
	var value Account
	var owner sql.NullString
	var rawOwner sql.NullString
	err := row.Scan(&value.ID, &value.Name, &value.Color, &value.SortOrder, &value.AccountType,
		&value.BankLabel, &value.LegacyOwnerLabel, &owner, &value.CurrencyCode, &value.IsSystem,
		&value.Version, &value.CreatedAt, &value.UpdatedAt, &value.ArchivedAt, version, &rawOwner)
	if owner.Valid {
		value.OwnerUserID = &owner.String
	}
	if rawOwner.Valid {
		parsed := uuid.MustParse(rawOwner.String)
		*ownerID = &parsed
	}
	return value, err
}

func applyAccountPatch(value *Account, patch AccountPatch) {
	if patch.Name.Present {
		value.Name = patch.Name.Value
	}
	if patch.Color.Present {
		value.Color = patch.Color.Value
	}
	if patch.SortOrder.Present {
		value.SortOrder = patch.SortOrder.Value
	}
	if patch.AccountType.Present {
		value.AccountType = patch.AccountType.Value
	}
	if patch.BankLabel.Present {
		value.BankLabel = patch.BankLabel.Value
	}
	if patch.LegacyOwnerLabel.Present {
		value.LegacyOwnerLabel = patch.LegacyOwnerLabel.Value
	}
}

func accountPatchFields(patch AccountPatch) []string {
	fields := make([]string, 0, 7)
	if patch.Name.Present {
		fields = append(fields, "name")
	}
	if patch.Color.Present {
		fields = append(fields, "color")
	}
	if patch.SortOrder.Present {
		fields = append(fields, "sortOrder")
	}
	if patch.AccountType.Present {
		fields = append(fields, "accountType")
	}
	if patch.BankLabel.Present {
		fields = append(fields, "bankLabel")
	}
	if patch.LegacyOwnerLabel.Present {
		fields = append(fields, "legacyOwnerLabel")
	}
	if patch.OwnerUserID.Present {
		fields = append(fields, "ownerUserId")
	}
	return fields
}

func commitValue(tx *sql.Tx) error { return tx.Commit() }

func scanCategory(row scanner) (Category, error) {
	var value Category
	err := row.Scan(&value.ID, &value.Type, &value.Name, &value.Color, &value.SortOrder,
		&value.IsSystem, &value.Version, &value.CreatedAt, &value.UpdatedAt, &value.ArchivedAt)
	return value, err
}

const categoryColumns = `c.id::text,c.category_type,c.name,c.color,c.sort_order,c.is_system,c.version::text,c.created_at,c.updated_at,c.archived_at`
const categoryReturning = `id::text,category_type,name,color,sort_order,is_system,version::text,created_at,updated_at,archived_at`

func (repository *PostgresRepository) ListCategories(ctx context.Context, subject string, householdID uuid.UUID, filter CategoryListFilter, limit int, cursor *CursorPosition) ([]Category, bool, error) {
	tx, err := repository.beginRead(ctx, subject, householdID)
	if err != nil {
		return nil, false, err
	}
	defer tx.Rollback()
	var ct, ci any
	if cursor != nil {
		ct, ci = cursor.CreatedAt, cursor.ID
	}
	rows, err := tx.QueryContext(ctx, `SELECT `+categoryColumns+` FROM categories c
		WHERE c.household_id=$1 AND c.deleted_at IS NULL AND c.category_type=$2
		AND ($3='all' OR ($3='active' AND c.archived_at IS NULL) OR ($3='archived' AND c.archived_at IS NOT NULL))
		AND ($4::timestamptz IS NULL OR (c.created_at,c.id)<($4,$5::uuid))
		ORDER BY c.created_at DESC,c.id DESC LIMIT $6`, householdID, filter.Type, filter.State, ct, ci, limit+1)
	if err != nil {
		return nil, false, err
	}
	defer rows.Close()
	items := make([]Category, 0, limit)
	for rows.Next() {
		value, e := scanCategory(rows)
		if e != nil {
			return nil, false, e
		}
		items = append(items, value)
	}
	if err := rows.Err(); err != nil {
		return nil, false, err
	}
	more := len(items) > limit
	if more {
		items = items[:limit]
	}
	return items, more, tx.Commit()
}

func (repository *PostgresRepository) CreateCategory(ctx context.Context, subject string, householdID uuid.UUID, input CategoryCreate, meta CreateMeta) (CreateResult[Category], error) {
	tx, actorValue, err := repository.beginMutation(ctx, subject, householdID)
	if err != nil {
		return CreateResult[Category]{}, err
	}
	defer tx.Rollback()
	if err := requireManager(actorValue); err != nil {
		return CreateResult[Category]{}, err
	}
	if replay, found, err := lookupCategoryReplay(ctx, tx, householdID, meta); found || err != nil {
		if err == nil {
			err = tx.Commit()
		}
		return CreateResult[Category]{Value: replay, Replayed: found}, err
	}
	id := uuid.New()
	value, err := scanCategory(tx.QueryRowContext(ctx, `INSERT INTO categories (id,household_id,category_type,name,color,sort_order,creation_idempotency_key,creation_payload_hash)
		VALUES($1,$2,$3,$4,$5,$6,$7,$8) ON CONFLICT (household_id,creation_idempotency_key) WHERE creation_idempotency_key IS NOT NULL DO NOTHING RETURNING `+categoryReturning,
		id, householdID, input.Type, input.Name, input.Color, input.SortOrder, meta.IdempotencyKey, meta.PayloadHash[:]))
	if errors.Is(err, sql.ErrNoRows) {
		value, _, err = lookupCategoryReplay(ctx, tx, householdID, meta)
		if err == nil {
			err = tx.Commit()
		}
		return CreateResult[Category]{Value: value, Replayed: err == nil}, err
	}
	if err != nil {
		return CreateResult[Category]{}, mapPostgresError(err)
	}
	if err := insertFinanceAudit(ctx, tx, householdID, actorValue.ID, id, "categories", "created", meta.RequestID, []string{"created"}); err != nil {
		return CreateResult[Category]{}, err
	}
	if err := tx.Commit(); err != nil {
		return CreateResult[Category]{}, err
	}
	return CreateResult[Category]{Value: value}, nil
}

func lookupCategoryReplay(ctx context.Context, tx *sql.Tx, householdID uuid.UUID, meta CreateMeta) (Category, bool, error) {
	var id uuid.UUID
	var hash []byte
	err := tx.QueryRowContext(ctx, `SELECT id,creation_payload_hash FROM categories WHERE household_id=$1 AND creation_idempotency_key=$2 FOR UPDATE`, householdID, meta.IdempotencyKey).Scan(&id, &hash)
	if errors.Is(err, sql.ErrNoRows) {
		return Category{}, false, nil
	}
	if err != nil {
		return Category{}, false, err
	}
	if !bytes.Equal(hash, meta.PayloadHash[:]) {
		return Category{}, true, ErrIdempotency
	}
	value, err := scanCategory(tx.QueryRowContext(ctx, `SELECT `+categoryColumns+` FROM categories c WHERE c.id=$1`, id))
	return value, true, err
}

func (repository *PostgresRepository) UpdateCategory(ctx context.Context, subject string, householdID, categoryID uuid.UUID, patch CategoryPatch, meta MutationMeta) (Category, error) {
	tx, a, err := repository.beginMutation(ctx, subject, householdID)
	if err != nil {
		return Category{}, err
	}
	defer tx.Rollback()
	if err := requireManager(a); err != nil {
		return Category{}, err
	}
	current, version, err := lockCategory(ctx, tx, householdID, categoryID)
	if err != nil {
		return Category{}, err
	}
	if current.IsSystem {
		return Category{}, ErrSystemImmutable
	}
	if err := ensureVersion(version, meta.ExpectedVersion); err != nil {
		return Category{}, err
	}
	if patch.Name.Present {
		current.Name = patch.Name.Value
	}
	if patch.Color.Present {
		current.Color = patch.Color.Value
	}
	if patch.SortOrder.Present {
		current.SortOrder = patch.SortOrder.Value
	}
	value, err := scanCategory(tx.QueryRowContext(ctx, `UPDATE categories SET name=$3,color=$4,sort_order=$5,version=version+1 WHERE household_id=$1 AND id=$2 AND version=$6 RETURNING `+categoryReturning, householdID, categoryID, current.Name, current.Color, current.SortOrder, meta.ExpectedVersion))
	if err != nil {
		return Category{}, mapPostgresError(err)
	}
	fields := make([]string, 0, 3)
	if patch.Name.Present {
		fields = append(fields, "name")
	}
	if patch.Color.Present {
		fields = append(fields, "color")
	}
	if patch.SortOrder.Present {
		fields = append(fields, "sortOrder")
	}
	if err := insertFinanceAudit(ctx, tx, householdID, a.ID, categoryID, "categories", "updated", meta.RequestID, fields); err != nil {
		return Category{}, err
	}
	return value, tx.Commit()
}

func (repository *PostgresRepository) SetCategoryArchived(ctx context.Context, subject string, householdID, categoryID uuid.UUID, archived bool, meta MutationMeta) (Category, error) {
	tx, a, err := repository.beginMutation(ctx, subject, householdID)
	if err != nil {
		return Category{}, err
	}
	defer tx.Rollback()
	if err := requireManager(a); err != nil {
		return Category{}, err
	}
	current, version, err := lockCategory(ctx, tx, householdID, categoryID)
	if err != nil {
		return Category{}, err
	}
	if current.IsSystem {
		return Category{}, ErrSystemImmutable
	}
	if err := ensureVersion(version, meta.ExpectedVersion); err != nil {
		return Category{}, err
	}
	if (current.ArchivedAt != nil) == archived {
		return Category{}, ErrConflict
	}
	value, err := scanCategory(tx.QueryRowContext(ctx, `UPDATE categories SET archived_at=CASE WHEN $3 THEN CURRENT_TIMESTAMP ELSE NULL END,version=version+1 WHERE household_id=$1 AND id=$2 AND version=$4 RETURNING `+categoryReturning, householdID, categoryID, archived, meta.ExpectedVersion))
	if err != nil {
		return Category{}, mapPostgresError(err)
	}
	action := "restored"
	if archived {
		action = "archived"
	}
	if err := insertFinanceAudit(ctx, tx, householdID, a.ID, categoryID, "categories", action, meta.RequestID, []string{"archivedAt"}); err != nil {
		return Category{}, err
	}
	return value, tx.Commit()
}

func lockCategory(ctx context.Context, tx *sql.Tx, householdID, categoryID uuid.UUID) (Category, int64, error) {
	var version int64
	var value Category
	err := tx.QueryRowContext(ctx, `SELECT `+categoryColumns+`,c.version FROM categories c WHERE c.household_id=$1 AND c.id=$2 AND c.deleted_at IS NULL FOR UPDATE`, householdID, categoryID).Scan(&value.ID, &value.Type, &value.Name, &value.Color, &value.SortOrder, &value.IsSystem, &value.Version, &value.CreatedAt, &value.UpdatedAt, &value.ArchivedAt, &version)
	if errors.Is(err, sql.ErrNoRows) {
		return Category{}, 0, ErrNotFound
	}
	return value, version, err
}
