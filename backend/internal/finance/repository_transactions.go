package finance

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"sort"
	"time"

	"github.com/google/uuid"
)

const transactionColumns = `t.id::text,t.transaction_type,t.account_id::text,t.to_account_id::text,
	t.category_id::text,t.amount_cents::text,to_char(t.event_date,'YYYY-MM-DD'),t.note,
	t.is_balance_adjustment,t.source,t.created_by_user_id::text,t.created_at,t.updated_at,
	t.deleted_at,t.deletion_reason,t.version::text`

const transactionReturning = `id::text,transaction_type,account_id::text,to_account_id::text,
	category_id::text,amount_cents::text,to_char(event_date,'YYYY-MM-DD'),note,
	is_balance_adjustment,source,created_by_user_id::text,created_at,updated_at,
	deleted_at,deletion_reason,version::text`

func scanTransaction(row scanner) (Transaction, error) {
	var value Transaction
	var toAccount, category, creator, reason sql.NullString
	err := row.Scan(&value.ID, &value.Type, &value.AccountID, &toAccount, &category,
		&value.AmountCents, &value.EventDate, &value.Note, &value.IsBalanceAdjustment,
		&value.Source, &creator, &value.CreatedAt, &value.UpdatedAt, &value.DeletedAt,
		&reason, &value.Version)
	if toAccount.Valid {
		value.ToAccountID = &toAccount.String
	}
	if category.Valid {
		value.CategoryID = &category.String
	}
	if creator.Valid {
		value.CreatedByUserID = &creator.String
	}
	if reason.Valid {
		value.DeletionReason = &reason.String
	}
	return value, err
}

func (repository *PostgresRepository) ListTransactions(ctx context.Context, subject string, householdID uuid.UUID, filter TransactionListFilter, limit int, cursor *CursorPosition) ([]Transaction, bool, error) {
	tx, err := repository.beginRead(ctx, subject, householdID)
	if err != nil {
		return nil, false, err
	}
	defer tx.Rollback()
	var from, to, account, category, cursorTime, cursorID any
	if filter.From != nil {
		from = filter.From.String()
	}
	if filter.To != nil {
		to = filter.To.String()
	}
	if filter.AccountID != nil {
		account = *filter.AccountID
	}
	if filter.CategoryID != nil {
		category = *filter.CategoryID
	}
	if cursor != nil {
		cursorTime, cursorID = cursor.CreatedAt, cursor.ID
	}
	rows, err := tx.QueryContext(ctx, `SELECT `+transactionColumns+` FROM transactions t
		WHERE t.household_id=$1
		AND ($2::date IS NULL OR t.event_date >= $2::date)
		AND ($3::date IS NULL OR t.event_date <= $3::date)
		AND ($4='' OR t.transaction_type=$4)
		AND ($5::uuid IS NULL OR t.account_id=$5 OR t.to_account_id=$5)
		AND ($6::uuid IS NULL OR t.category_id=$6)
		AND ($7='all' OR ($7='active' AND t.deleted_at IS NULL) OR ($7='deleted' AND t.deleted_at IS NOT NULL))
		AND ($8::timestamptz IS NULL OR (t.created_at,t.id)<($8,$9::uuid))
		ORDER BY t.created_at DESC,t.id DESC LIMIT $10`, householdID, from, to, filter.Type,
		account, category, filter.State, cursorTime, cursorID, limit+1)
	if err != nil {
		return nil, false, err
	}
	defer rows.Close()
	items := make([]Transaction, 0, limit)
	for rows.Next() {
		value, scanErr := scanTransaction(rows)
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

func (repository *PostgresRepository) CreateTransaction(ctx context.Context, subject string, householdID uuid.UUID, input TransactionValues, meta CreateMeta) (CreateResult[Transaction], error) {
	tx, actorValue, err := repository.beginMutation(ctx, subject, householdID)
	if err != nil {
		return CreateResult[Transaction]{}, err
	}
	defer tx.Rollback()
	if replay, found, replayErr := lookupTransactionReplay(ctx, tx, householdID, meta); found || replayErr != nil {
		if replayErr == nil {
			replayErr = tx.Commit()
		}
		return CreateResult[Transaction]{Value: replay, Replayed: found}, replayErr
	}
	if err := lockTransactionReferences(ctx, tx, householdID, input, true); err != nil {
		return CreateResult[Transaction]{}, err
	}
	id := uuid.New()
	value, err := scanTransaction(tx.QueryRowContext(ctx, `INSERT INTO transactions
		(id,household_id,transaction_type,account_id,to_account_id,category_id,amount_cents,event_date,note,is_balance_adjustment,source,idempotency_key,idempotency_payload_hash,created_by_user_id)
		VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,'manual',$11,$12,$13)
		ON CONFLICT (household_id,idempotency_key) WHERE idempotency_key IS NOT NULL DO NOTHING
		RETURNING `+transactionReturning, id, householdID, input.Type, input.AccountID, input.ToAccountID, input.CategoryID, input.AmountCents, input.EventDate.String(), input.Note, input.IsBalanceAdjustment, meta.IdempotencyKey, meta.PayloadHash[:], actorValue.ID))
	if errors.Is(err, sql.ErrNoRows) {
		value, _, err = lookupTransactionReplay(ctx, tx, householdID, meta)
		if err == nil {
			err = tx.Commit()
		}
		return CreateResult[Transaction]{Value: value, Replayed: err == nil}, err
	}
	if err != nil {
		return CreateResult[Transaction]{}, mapPostgresError(err)
	}
	if err := insertFinanceAudit(ctx, tx, householdID, actorValue.ID, id, "transactions", "created", meta.RequestID, []string{"created"}); err != nil {
		return CreateResult[Transaction]{}, err
	}
	if err := tx.Commit(); err != nil {
		return CreateResult[Transaction]{}, err
	}
	return CreateResult[Transaction]{Value: value}, nil
}

func lookupTransactionReplay(ctx context.Context, tx *sql.Tx, householdID uuid.UUID, meta CreateMeta) (Transaction, bool, error) {
	var id uuid.UUID
	var hash []byte
	err := tx.QueryRowContext(ctx, `SELECT id,idempotency_payload_hash FROM transactions WHERE household_id=$1 AND idempotency_key=$2 FOR UPDATE`, householdID, meta.IdempotencyKey).Scan(&id, &hash)
	if errors.Is(err, sql.ErrNoRows) {
		return Transaction{}, false, nil
	}
	if err != nil {
		return Transaction{}, false, err
	}
	if len(hash) != 32 || !bytes.Equal(hash, meta.PayloadHash[:]) {
		return Transaction{}, true, ErrIdempotency
	}
	value, err := scanTransaction(tx.QueryRowContext(ctx, `SELECT `+transactionColumns+` FROM transactions t WHERE t.id=$1`, id))
	return value, true, err
}

func (repository *PostgresRepository) UpdateTransaction(ctx context.Context, subject string, householdID, transactionID uuid.UUID, patch TransactionPatch, meta MutationMeta) (Transaction, error) {
	tx, a, err := repository.beginMutation(ctx, subject, householdID)
	if err != nil {
		return Transaction{}, err
	}
	defer tx.Rollback()
	current, version, deleted, err := lockTransaction(ctx, tx, householdID, transactionID)
	if err != nil {
		return Transaction{}, err
	}
	if err := ensureVersion(version, meta.ExpectedVersion); err != nil {
		return Transaction{}, err
	}
	if deleted {
		return Transaction{}, ErrConflict
	}
	merged, err := mergeTransactionPatch(current, patch)
	if err != nil {
		return Transaction{}, err
	}
	if err := lockPatchedReferences(ctx, tx, householdID, current, merged, patch); err != nil {
		return Transaction{}, err
	}
	value, err := scanTransaction(tx.QueryRowContext(ctx, `UPDATE transactions SET transaction_type=$3,account_id=$4,to_account_id=$5,category_id=$6,amount_cents=$7,event_date=$8,note=$9,is_balance_adjustment=$10,updated_by_user_id=$11,version=version+1 WHERE household_id=$1 AND id=$2 AND version=$12 RETURNING `+transactionReturning, householdID, transactionID, merged.Type, merged.AccountID, merged.ToAccountID, merged.CategoryID, merged.AmountCents, merged.EventDate.String(), merged.Note, merged.IsBalanceAdjustment, a.ID, meta.ExpectedVersion))
	if err != nil {
		return Transaction{}, mapPostgresError(err)
	}
	if err := insertFinanceAudit(ctx, tx, householdID, a.ID, transactionID, "transactions", "updated", meta.RequestID, transactionPatchFields(patch)); err != nil {
		return Transaction{}, err
	}
	return value, tx.Commit()
}

func (repository *PostgresRepository) DeleteTransaction(ctx context.Context, subject string, householdID, transactionID uuid.UUID, reason string, meta MutationMeta) (Transaction, error) {
	tx, a, err := repository.beginMutation(ctx, subject, householdID)
	if err != nil {
		return Transaction{}, err
	}
	defer tx.Rollback()
	_, version, deleted, err := lockTransaction(ctx, tx, householdID, transactionID)
	if err != nil {
		return Transaction{}, err
	}
	if err := ensureVersion(version, meta.ExpectedVersion); err != nil {
		return Transaction{}, err
	}
	if deleted {
		return Transaction{}, ErrConflict
	}
	value, err := scanTransaction(tx.QueryRowContext(ctx, `UPDATE transactions SET deleted_at=CURRENT_TIMESTAMP,deleted_by_user_id=$3,deletion_reason=$4,version=version+1 WHERE household_id=$1 AND id=$2 AND version=$5 RETURNING `+transactionReturning, householdID, transactionID, a.ID, reason, meta.ExpectedVersion))
	if err != nil {
		return Transaction{}, mapPostgresError(err)
	}
	if err := insertFinanceAudit(ctx, tx, householdID, a.ID, transactionID, "transactions", "deleted", meta.RequestID, []string{"deletedAt"}); err != nil {
		return Transaction{}, err
	}
	return value, tx.Commit()
}

func (repository *PostgresRepository) RestoreTransaction(ctx context.Context, subject string, householdID, transactionID uuid.UUID, meta MutationMeta) (Transaction, error) {
	tx, a, err := repository.beginMutation(ctx, subject, householdID)
	if err != nil {
		return Transaction{}, err
	}
	defer tx.Rollback()
	current, version, deleted, err := lockTransaction(ctx, tx, householdID, transactionID)
	if err != nil {
		return Transaction{}, err
	}
	if err := ensureVersion(version, meta.ExpectedVersion); err != nil {
		return Transaction{}, err
	}
	if !deleted {
		return Transaction{}, ErrConflict
	}
	if err := lockTransactionReferences(ctx, tx, householdID, current, false); err != nil {
		return Transaction{}, err
	}
	value, err := scanTransaction(tx.QueryRowContext(ctx, `UPDATE transactions SET deleted_at=NULL,deleted_by_user_id=NULL,deletion_reason=NULL,updated_by_user_id=$3,version=version+1 WHERE household_id=$1 AND id=$2 AND version=$4 RETURNING `+transactionReturning, householdID, transactionID, a.ID, meta.ExpectedVersion))
	if err != nil {
		return Transaction{}, mapPostgresError(err)
	}
	if err := insertFinanceAudit(ctx, tx, householdID, a.ID, transactionID, "transactions", "restored", meta.RequestID, []string{"deletedAt"}); err != nil {
		return Transaction{}, err
	}
	return value, tx.Commit()
}

func lockTransaction(ctx context.Context, tx *sql.Tx, householdID, transactionID uuid.UUID) (TransactionValues, int64, bool, error) {
	var v TransactionValues
	var toID, catID *uuid.UUID
	var date time.Time
	var version int64
	var deleted *time.Time
	err := tx.QueryRowContext(ctx, `SELECT transaction_type,account_id,to_account_id,category_id,amount_cents,event_date,note,is_balance_adjustment,version,deleted_at FROM transactions WHERE household_id=$1 AND id=$2 FOR UPDATE`, householdID, transactionID).Scan(&v.Type, &v.AccountID, &toID, &catID, &v.AmountCents, &date, &v.Note, &v.IsBalanceAdjustment, &version, &deleted)
	if errors.Is(err, sql.ErrNoRows) {
		return TransactionValues{}, 0, false, ErrNotFound
	}
	v.ToAccountID = toID
	v.CategoryID = catID
	v.EventDate = LocalDate{Time: date}
	return v, version, deleted != nil, err
}

func lockPatchedReferences(ctx context.Context, tx *sql.Tx, householdID uuid.UUID, current, merged TransactionValues, patch TransactionPatch) error {
	// Unchanged archived references remain valid history; every newly assigned reference must be active.
	toChanged := patch.ToAccountID.Present && !equalOptionalUUID(current.ToAccountID, merged.ToAccountID)
	categoryChanged := patch.CategoryID.Present && !equalOptionalUUID(current.CategoryID, merged.CategoryID)
	return lockTransactionReferencesSelective(ctx, tx, householdID, merged, map[uuid.UUID]bool{
		merged.AccountID: patch.AccountID.Present && merged.AccountID != current.AccountID,
	}, toChanged, categoryChanged)
}

func equalOptionalUUID(left, right *uuid.UUID) bool {
	if left == nil || right == nil {
		return left == nil && right == nil
	}
	return *left == *right
}

func lockTransactionReferences(ctx context.Context, tx *sql.Tx, householdID uuid.UUID, v TransactionValues, requireActive bool) error {
	return lockTransactionReferencesSelective(ctx, tx, householdID, v, map[uuid.UUID]bool{v.AccountID: requireActive}, requireActive, requireActive)
}

func lockTransactionReferencesSelective(ctx context.Context, tx *sql.Tx, householdID uuid.UUID, v TransactionValues, active map[uuid.UUID]bool, toActive, categoryActive bool) error {
	ids := []uuid.UUID{v.AccountID}
	if v.ToAccountID != nil {
		ids = append(ids, *v.ToAccountID)
		active[*v.ToAccountID] = toActive
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i].String() < ids[j].String() })
	seen := map[uuid.UUID]bool{}
	for _, id := range ids {
		if seen[id] {
			continue
		}
		seen[id] = true
		var archived, deleted *time.Time
		err := tx.QueryRowContext(ctx, `SELECT archived_at,deleted_at FROM accounts WHERE household_id=$1 AND id=$2 FOR UPDATE`, householdID, id).Scan(&archived, &deleted)
		if errors.Is(err, sql.ErrNoRows) || deleted != nil || active[id] && archived != nil {
			return ErrNotFound
		}
		if err != nil {
			return err
		}
	}
	if v.CategoryID != nil {
		var archived, deleted *time.Time
		var typ string
		err := tx.QueryRowContext(ctx, `SELECT category_type,archived_at,deleted_at FROM categories WHERE household_id=$1 AND id=$2 FOR UPDATE`, householdID, *v.CategoryID).Scan(&typ, &archived, &deleted)
		if errors.Is(err, sql.ErrNoRows) || deleted != nil || categoryActive && archived != nil || typ != v.Type {
			return ErrNotFound
		}
		if err != nil {
			return err
		}
	}
	return nil
}

func transactionPatchFields(p TransactionPatch) []string {
	fields := make([]string, 0, 8)
	if p.Type.Present {
		fields = append(fields, "type")
	}
	if p.AccountID.Present {
		fields = append(fields, "accountId")
	}
	if p.ToAccountID.Present {
		fields = append(fields, "toAccountId")
	}
	if p.CategoryID.Present {
		fields = append(fields, "categoryId")
	}
	if p.AmountCents.Present {
		fields = append(fields, "amountCents")
	}
	if p.EventDate.Present {
		fields = append(fields, "eventDate")
	}
	if p.Note.Present {
		fields = append(fields, "note")
	}
	if p.IsBalanceAdjustment.Present {
		fields = append(fields, "isBalanceAdjustment")
	}
	return fields
}
