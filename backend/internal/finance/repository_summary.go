package finance

import (
	"context"
	"database/sql"

	"github.com/google/uuid"
)

const balanceLedger = `
	SELECT account_id, CASE transaction_type WHEN 'income' THEN amount_cents ELSE -amount_cents END AS amount
	FROM transactions WHERE household_id=$1 AND deleted_at IS NULL AND event_date <= $2::date
	UNION ALL
	SELECT to_account_id, amount_cents FROM transactions
	WHERE household_id=$1 AND deleted_at IS NULL AND event_date <= $2::date AND transaction_type='transfer'`

func (repository *PostgresRepository) GetSummary(ctx context.Context, subject string, householdID uuid.UUID, rangeValue SummaryRange) (Summary, error) {
	tx, err := repository.beginRead(ctx, subject, householdID)
	if err != nil {
		return Summary{}, err
	}
	defer tx.Rollback()
	var totalText, incomeText, expenseText string
	err = tx.QueryRowContext(ctx, `WITH ledger AS (`+balanceLedger+`), balances AS (SELECT account_id,SUM(amount) amount FROM ledger GROUP BY account_id)
		SELECT COALESCE((SELECT SUM(COALESCE(b.amount,0)) FROM accounts a LEFT JOIN balances b ON b.account_id=a.id WHERE a.household_id=$1 AND a.deleted_at IS NULL),0)::text,
		COALESCE(SUM(t.amount_cents) FILTER(WHERE t.transaction_type='income' AND NOT t.is_balance_adjustment),0)::text,
		COALESCE(SUM(t.amount_cents) FILTER(WHERE t.transaction_type='expense' AND NOT t.is_balance_adjustment),0)::text
		FROM transactions t WHERE t.household_id=$1 AND t.deleted_at IS NULL AND t.event_date BETWEEN $3::date AND $2::date`, householdID, rangeValue.To.String(), rangeValue.From.String()).Scan(&totalText, &incomeText, &expenseText)
	if err != nil {
		return Summary{}, err
	}
	total, err := parseSignedCents(totalText)
	if err != nil {
		return Summary{}, err
	}
	income, err := parseSignedCents(incomeText)
	if err != nil {
		return Summary{}, err
	}
	expense, err := parseSignedCents(expenseText)
	if err != nil {
		return Summary{}, err
	}
	if err := tx.Commit(); err != nil {
		return Summary{}, err
	}
	return Summary{From: rangeValue.From.String(), To: rangeValue.To.String(), HouseholdTotalCents: formatInt64(total), CashFlow: CashFlow{IncomeCents: formatInt64(income), ExpenseCents: formatInt64(expense)}}, nil
}

func (repository *PostgresRepository) ListAccountBalances(ctx context.Context, subject string, householdID uuid.UUID, filter AccountBalanceFilter, limit int, cursor *CursorPosition) ([]AccountBalance, []CursorPosition, bool, error) {
	tx, err := repository.beginRead(ctx, subject, householdID)
	if err != nil {
		return nil, nil, false, err
	}
	defer tx.Rollback()
	var ct, ci any
	if cursor != nil {
		ct, ci = cursor.CreatedAt, cursor.ID
	}
	rows, err := tx.QueryContext(ctx, `WITH ledger AS (`+balanceLedger+`), balances AS (SELECT account_id,SUM(amount) amount FROM ledger GROUP BY account_id)
		SELECT a.id::text,a.name,a.archived_at,COALESCE(b.amount,0)::text,a.created_at
		FROM accounts a LEFT JOIN balances b ON b.account_id=a.id
		WHERE a.household_id=$1 AND a.deleted_at IS NULL AND (a.archived_at IS NULL OR COALESCE(b.amount,0)<>0)
		AND ($3::timestamptz IS NULL OR (a.created_at,a.id)<($3,$4::uuid)) ORDER BY a.created_at DESC,a.id DESC LIMIT $5`, householdID, filter.To.String(), ct, ci, limit+1)
	if err != nil {
		return nil, nil, false, err
	}
	defer rows.Close()
	items := make([]AccountBalance, 0, limit)
	positions := make([]CursorPosition, 0, limit)
	for rows.Next() {
		var value AccountBalance
		var amount string
		var createdAt sql.NullTime
		if err := rows.Scan(&value.AccountID, &value.Name, &value.ArchivedAt, &amount, &createdAt); err != nil {
			return nil, nil, false, err
		}
		parsed, err := parseSignedCents(amount)
		if err != nil {
			return nil, nil, false, err
		}
		value.BalanceCents = formatInt64(parsed)
		id, _ := uuid.Parse(value.AccountID)
		items = append(items, value)
		positions = append(positions, CursorPosition{CreatedAt: createdAt.Time, ID: id})
	}
	if err := rows.Err(); err != nil {
		return nil, nil, false, err
	}
	more := len(items) > limit
	if more {
		items = items[:limit]
		positions = positions[:limit]
	}
	if err := tx.Commit(); err != nil {
		return nil, nil, false, err
	}
	return items, positions, more, nil
}

func (repository *PostgresRepository) ListCategoryExpenses(ctx context.Context, subject string, householdID uuid.UUID, filter CategoryExpenseFilter, limit int, cursor *CursorPosition) ([]CategoryExpense, []CursorPosition, bool, error) {
	tx, err := repository.beginRead(ctx, subject, householdID)
	if err != nil {
		return nil, nil, false, err
	}
	defer tx.Rollback()
	var ct, ci any
	if cursor != nil {
		ct, ci = cursor.CreatedAt, cursor.ID
	}
	rows, err := tx.QueryContext(ctx, `SELECT c.id::text,c.name,COALESCE(SUM(t.amount_cents),0)::text,c.created_at
		FROM categories c LEFT JOIN transactions t ON t.household_id=c.household_id AND t.category_id=c.id AND t.transaction_type='expense' AND t.deleted_at IS NULL AND NOT t.is_balance_adjustment AND t.event_date BETWEEN $2::date AND $3::date
		WHERE c.household_id=$1 AND c.deleted_at IS NULL AND c.category_type='expense'
		AND ($4::timestamptz IS NULL OR (c.created_at,c.id)<($4,$5::uuid))
		GROUP BY c.id,c.name,c.created_at ORDER BY c.created_at DESC,c.id DESC LIMIT $6`, householdID, filter.From.String(), filter.To.String(), ct, ci, limit+1)
	if err != nil {
		return nil, nil, false, err
	}
	defer rows.Close()
	items := make([]CategoryExpense, 0, limit)
	positions := make([]CursorPosition, 0, limit)
	for rows.Next() {
		var value CategoryExpense
		var amount string
		var createdAt sql.NullTime
		if err := rows.Scan(&value.CategoryID, &value.Name, &amount, &createdAt); err != nil {
			return nil, nil, false, err
		}
		parsed, err := parseSignedCents(amount)
		if err != nil {
			return nil, nil, false, err
		}
		value.AmountCents = formatInt64(parsed)
		id, _ := uuid.Parse(value.CategoryID)
		items = append(items, value)
		positions = append(positions, CursorPosition{CreatedAt: createdAt.Time, ID: id})
	}
	if err := rows.Err(); err != nil {
		return nil, nil, false, err
	}
	more := len(items) > limit
	if more {
		items = items[:limit]
		positions = positions[:limit]
	}
	if err := tx.Commit(); err != nil {
		return nil, nil, false, err
	}
	return items, positions, more, nil
}
