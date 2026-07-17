package backupv5

import (
	"context"
	"crypto/hmac"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"sort"
	"strconv"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
)

type PostgresConfirmRepository struct {
	database *sql.DB
}

var _ ConfirmRepository = (*PostgresConfirmRepository)(nil)

func NewPostgresConfirmRepository(database *sql.DB) *PostgresConfirmRepository {
	return &PostgresConfirmRepository{database: database}
}

func (repository *PostgresConfirmRepository) ReferencedHMACKeyIDs(ctx context.Context) ([]string, error) {
	if repository == nil || repository.database == nil {
		return nil, ErrRepository
	}
	rows, err := repository.database.QueryContext(ctx, `
		SELECT DISTINCT hmac_key_id FROM backup_v5_import_runs ORDER BY hmac_key_id
	`)
	if err != nil {
		return nil, confirmRepositoryError(ctx, err)
	}
	defer rows.Close()
	keyIDs := make([]string, 0)
	for rows.Next() {
		var keyID string
		if err := rows.Scan(&keyID); err != nil {
			return nil, confirmRepositoryError(ctx, err)
		}
		keyIDs = append(keyIDs, keyID)
	}
	if err := rows.Err(); err != nil {
		return nil, confirmRepositoryError(ctx, err)
	}
	return keyIDs, nil
}

// Confirm serializes at the household row before actor membership and every
// financial row. This conflicts with finance KEY SHARE mutations and preserves
// the household-first order used by role mutations.
func (repository *PostgresConfirmRepository) Confirm(ctx context.Context, command ConfirmCommand) (ConfirmResult, error) {
	if repository == nil || repository.database == nil || command.Model == nil {
		return ConfirmResult{}, ErrRepository
	}
	tx, err := repository.database.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelReadCommitted})
	if err != nil {
		return ConfirmResult{}, confirmRepositoryError(ctx, err)
	}
	defer func() { _ = tx.Rollback() }()

	if err := lockImportHousehold(ctx, tx, command.HouseholdID); err != nil {
		return ConfirmResult{}, err
	}
	actorID, role, err := lockImportActor(ctx, tx, command.ActorSubject, command.HouseholdID)
	if err != nil {
		return ConfirmResult{}, err
	}
	if role != "owner" {
		return ConfirmResult{}, ErrForbidden
	}

	replay, found, err := findCompletedImport(ctx, tx, command, actorID)
	if err != nil {
		return ConfirmResult{}, err
	}
	if found {
		if err := tx.Commit(); err != nil {
			return ConfirmResult{}, confirmRepositoryError(ctx, err)
		}
		replay.Replayed = true
		return replay, nil
	}

	presence, err := financialPresence(ctx, tx, command.HouseholdID)
	if err != nil {
		return ConfirmResult{}, err
	}
	if presence.any() {
		return ConfirmResult{}, ErrHouseholdNotEmpty
	}
	if !command.TokenValid {
		return ConfirmResult{}, ErrPreviewTokenInvalid
	}
	if err := validateAndLockPreview(ctx, tx, command, actorID); err != nil {
		return ConfirmResult{}, err
	}

	var completedAt time.Time
	if err := tx.QueryRowContext(ctx, `SELECT transaction_timestamp()`).Scan(&completedAt); err != nil {
		return ConfirmResult{}, confirmRepositoryError(ctx, err)
	}
	if err := insertImportModel(ctx, tx, command, actorID, completedAt); err != nil {
		return ConfirmResult{}, err
	}
	if err := reconcileImportedModel(ctx, tx, command.Model); err != nil {
		return ConfirmResult{}, err
	}
	runID, err := uuid.NewRandom()
	if err != nil {
		return ConfirmResult{}, ErrRepository
	}
	result := ConfirmResult{Response: confirmResponse(runID, completedAt, command.Model)}
	if err := insertImportRun(ctx, tx, command, actorID, result.Response); err != nil {
		return ConfirmResult{}, err
	}
	if err := insertImportAudit(ctx, tx, command, actorID, result.Response); err != nil {
		return ConfirmResult{}, err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM backup_v5_import_previews WHERE household_id = $1`, command.HouseholdID); err != nil {
		return ConfirmResult{}, confirmRepositoryError(ctx, err)
	}
	if err := tx.Commit(); err != nil {
		return ConfirmResult{}, confirmRepositoryError(ctx, err)
	}
	return result, nil
}

func lockImportHousehold(ctx context.Context, tx *sql.Tx, householdID uuid.UUID) error {
	var id string
	err := tx.QueryRowContext(ctx, `
		SELECT id::text FROM households
		WHERE id = $1 AND archived_at IS NULL AND deleted_at IS NULL
		FOR UPDATE
	`, householdID).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return confirmRepositoryError(ctx, err)
	}
	return nil
}

func lockImportActor(ctx context.Context, tx *sql.Tx, subject string, householdID uuid.UUID) (uuid.UUID, string, error) {
	var idText, role string
	err := tx.QueryRowContext(ctx, `
		SELECT u.id::text, hm.role
		FROM users AS u
		JOIN household_members AS hm
		  ON hm.user_id = u.id AND hm.household_id = $2 AND hm.status = 'active'
		WHERE u.auth_subject = $1 AND u.deleted_at IS NULL
		FOR KEY SHARE OF u, hm
	`, subject, householdID).Scan(&idText, &role)
	if errors.Is(err, sql.ErrNoRows) {
		return uuid.Nil, "", ErrNotFound
	}
	if err != nil {
		return uuid.Nil, "", confirmRepositoryError(ctx, err)
	}
	id, err := uuid.Parse(idText)
	if err != nil {
		return uuid.Nil, "", ErrRepository
	}
	return id, role, nil
}

func findCompletedImport(ctx context.Context, tx *sql.Tx, command ConfirmCommand, actorID uuid.UUID) (ConfirmResult, bool, error) {
	type foundRun struct {
		response    ConfirmResponse
		fingerprint []byte
		candidate   HMACCandidate
	}
	found := make([]foundRun, 0, 1)
	for _, candidate := range command.Candidates {
		response, fingerprint, exists, err := scanImportRun(ctx, tx, command.HouseholdID, actorID, candidate)
		if err != nil {
			return ConfirmResult{}, false, err
		}
		if !exists {
			continue
		}
		found = append(found, foundRun{response: response, fingerprint: fingerprint, candidate: candidate})
	}
	if len(found) == 0 {
		return ConfirmResult{}, false, nil
	}
	if len(found) > 1 {
		return ConfirmResult{}, false, ErrImportStateConflict
	}
	if !fingerprintsEqual(found[0].fingerprint, found[0].candidate.Fingerprint[:]) {
		return ConfirmResult{}, false, ErrIdempotencyConflict
	}
	return ConfirmResult{Response: found[0].response, Replayed: true}, true, nil
}

func scanImportRun(ctx context.Context, tx *sql.Tx, householdID, actorID uuid.UUID, candidate HMACCandidate) (ConfirmResponse, []byte, bool, error) {
	var response ConfirmResponse
	var fingerprint []byte
	err := tx.QueryRowContext(ctx, `
		SELECT id::text, status, policy_version, completed_at,
		       accounts_count, categories_count, transactions_count, budgets_count,
		       goals_count, goal_contributions_count, debts_count, debt_payments_count,
		       legacy_owner_not_linked_count, archive_time_approximated_count,
		       goal_exceeds_target_count, debt_overpaid_count,
		       system_resource_preserved_count, budget_month_explicit_choice_count,
		       request_fingerprint_hmac
		FROM backup_v5_import_runs
		WHERE household_id = $1 AND actor_user_id = $2
		  AND hmac_key_id = $3 AND idempotency_key_hmac = $4
	`, householdID, actorID, candidate.KeyID, candidate.Lookup[:]).Scan(
		&response.ImportRunID, &response.Status, &response.PolicyVersion, &response.CompletedAt,
		&response.Counts.Accounts, &response.Counts.Categories, &response.Counts.Transactions,
		&response.Counts.Budgets, &response.Counts.Goals, &response.Counts.GoalContributions,
		&response.Counts.Debts, &response.Counts.DebtPayments,
		&response.WarningCounts.LegacyOwnerNotLinked, &response.WarningCounts.ArchiveTimeApproximated,
		&response.WarningCounts.GoalExceedsTarget, &response.WarningCounts.DebtOverpaid,
		&response.WarningCounts.SystemResourcePreserved, &response.WarningCounts.BudgetMonthExplicitChoice,
		&fingerprint,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return ConfirmResponse{}, nil, false, nil
	}
	if err != nil {
		return ConfirmResponse{}, nil, false, confirmRepositoryError(ctx, err)
	}
	if response.Status != "completed" || response.PolicyVersion != PolicyVersion || len(fingerprint) != 32 {
		return ConfirmResponse{}, nil, false, ErrImportStateConflict
	}
	return response, fingerprint, true, nil
}

func validateAndLockPreview(ctx context.Context, tx *sql.Tx, command ConfirmCommand, actorID uuid.UUID) error {
	var householdText, actorText, budgetMonth, policy string
	var digest []byte
	var expiresAt time.Time
	var consumedAt, revokedAt sql.NullTime
	err := tx.QueryRowContext(ctx, `
		SELECT household_id::text, actor_user_id::text, backup_digest,
		       budget_month::text, policy_version, expires_at, consumed_at, revoked_at
		FROM backup_v5_import_previews
		WHERE token_hash = $1
		FOR UPDATE
	`, command.TokenHash[:]).Scan(
		&householdText, &actorText, &digest, &budgetMonth, &policy,
		&expiresAt, &consumedAt, &revokedAt,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrPreviewTokenInvalid
	}
	if err != nil {
		return confirmRepositoryError(ctx, err)
	}
	var databaseNow time.Time
	if err := tx.QueryRowContext(ctx, `SELECT transaction_timestamp()`).Scan(&databaseNow); err != nil {
		return confirmRepositoryError(ctx, err)
	}
	valid := constantStringEqual(householdText, command.HouseholdID.String()) &&
		constantStringEqual(actorText, actorID.String()) &&
		constantStringEqual(budgetMonth, command.Model.BudgetMonth.String()) &&
		constantStringEqual(policy, PolicyVersion) &&
		!consumedAt.Valid && !revokedAt.Valid && previewTimeActive(databaseNow, expiresAt) &&
		len(digest) == 32 && hmac.Equal(digest, command.Model.BackupDigest[:])
	if !valid {
		return ErrPreviewTokenInvalid
	}
	return nil
}

func previewTimeActive(databaseNow, expiresAt time.Time) bool {
	return databaseNow.Before(expiresAt)
}

func constantStringEqual(first, second string) bool {
	return len(first) == len(second) && hmac.Equal([]byte(first), []byte(second))
}

func insertImportModel(ctx context.Context, tx *sql.Tx, command ConfirmCommand, actorID uuid.UUID, completedAt time.Time) error {
	model := command.Model
	accounts := append([]Account(nil), model.Accounts...)
	sort.Slice(accounts, func(i, j int) bool { return accounts[i].ID.String() < accounts[j].ID.String() })
	for _, account := range accounts {
		_, err := tx.ExecContext(ctx, `
			INSERT INTO accounts (
				id, household_id, name, color, sort_order, account_type, bank_label,
				legacy_owner_label, owner_user_id, currency_code, is_system,
				created_at, updated_at, version
			) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,NULL,'RUB',$9,$10,$10,1)
		`, account.ID, model.HouseholdID, account.Name, account.Color, account.SortOrder,
			account.Kind, account.BankLabel, account.LegacyOwnerLabel, account.IsSystem, account.CreatedAt)
		if err != nil {
			return confirmRepositoryError(ctx, err)
		}
	}
	categories := append([]Category(nil), model.Categories...)
	sort.Slice(categories, func(i, j int) bool { return categories[i].ID.String() < categories[j].ID.String() })
	for _, category := range categories {
		_, err := tx.ExecContext(ctx, `
			INSERT INTO categories (
				id, household_id, category_type, name, color, sort_order, is_system,
				created_at, updated_at, version
			) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$8,1)
		`, category.ID, model.HouseholdID, category.Type, category.Name, category.Color,
			category.SortOrder, category.IsSystem, category.CreatedAt)
		if err != nil {
			return confirmRepositoryError(ctx, err)
		}
	}
	budgets := append([]Budget(nil), model.Budgets...)
	sort.Slice(budgets, func(i, j int) bool { return budgets[i].ID.String() < budgets[j].ID.String() })
	for _, budget := range budgets {
		_, err := tx.ExecContext(ctx, `
			INSERT INTO budgets (id, household_id, category_id, category_type, budget_month, amount_cents, created_at, updated_at)
			VALUES ($1,$2,$3,'expense',$4,$5,$6,$6)
		`, budget.ID, model.HouseholdID, budget.CategoryID, budget.Month.String(), budget.AmountCents, completedAt)
		if err != nil {
			return confirmRepositoryError(ctx, err)
		}
	}
	transactions := append([]Transaction(nil), model.Transactions...)
	sort.Slice(transactions, func(i, j int) bool { return transactions[i].ID.String() < transactions[j].ID.String() })
	for _, transaction := range transactions {
		_, err := tx.ExecContext(ctx, `
			INSERT INTO transactions (
				id, household_id, transaction_type, account_id, to_account_id, category_id,
				amount_cents, event_date, note, is_balance_adjustment, source,
				idempotency_key, idempotency_payload_hash, created_by_user_id, updated_by_user_id,
				created_at, updated_at, version
			) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,'import',$11,$12,$13,$13,$14,$14,1)
		`, transaction.ID, model.HouseholdID, transaction.Type, transaction.AccountID,
			transaction.ToAccountID, transaction.CategoryID, transaction.AmountCents,
			transaction.EventDate.String(), transaction.Note, transaction.IsBalanceAdjustment,
			transaction.IdempotencyKey, transaction.PayloadHash[:], actorID, transaction.CreatedAt)
		if err != nil {
			return confirmRepositoryError(ctx, err)
		}
	}
	goals := append([]Goal(nil), model.Goals...)
	sort.Slice(goals, func(i, j int) bool { return goals[i].ID.String() < goals[j].ID.String() })
	for _, goal := range goals {
		var targetDate any
		if goal.TargetDate != nil {
			targetDate = goal.TargetDate.String()
		}
		var archivedAt any
		if goal.Archived {
			archivedAt = completedAt
		}
		_, err := tx.ExecContext(ctx, `
			INSERT INTO goals (
				id, household_id, name, target_amount_cents, initial_saved_cents,
				target_date, color, created_at, updated_at, archived_at
			) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$8,$9)
		`, goal.ID, model.HouseholdID, goal.Name, goal.TargetCents, goal.InitialSavedCents,
			targetDate, goal.Color, goal.CreatedAt, archivedAt)
		if err != nil {
			return confirmRepositoryError(ctx, err)
		}
	}
	debts := append([]Debt(nil), model.Debts...)
	sort.Slice(debts, func(i, j int) bool { return debts[i].ID.String() < debts[j].ID.String() })
	for _, debt := range debts {
		var dueDate any
		if debt.DueDate != nil {
			dueDate = debt.DueDate.String()
		}
		var archivedAt any
		if debt.Archived {
			archivedAt = completedAt
		}
		_, err := tx.ExecContext(ctx, `
			INSERT INTO debts (
				id, household_id, person_label, direction, original_amount_cents,
				due_date, note, created_at, updated_at, archived_at
			) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$8,$9)
		`, debt.ID, model.HouseholdID, debt.PersonLabel, debt.Direction,
			debt.OriginalAmountCents, dueDate, debt.Note, debt.CreatedAt, archivedAt)
		if err != nil {
			return confirmRepositoryError(ctx, err)
		}
	}
	payments := append([]DebtPayment(nil), model.DebtPayments...)
	sort.Slice(payments, func(i, j int) bool { return payments[i].ID.String() < payments[j].ID.String() })
	for _, payment := range payments {
		_, err := tx.ExecContext(ctx, `
			INSERT INTO debt_payments (
				id, household_id, debt_id, amount_cents, event_date, note, source, created_at, updated_at
			) VALUES ($1,$2,$3,$4,$5,$6,'import',$7,$7)
		`, payment.ID, model.HouseholdID, payment.DebtID, payment.AmountCents,
			payment.EventDate.String(), payment.Note, payment.CreatedAt)
		if err != nil {
			return confirmRepositoryError(ctx, err)
		}
	}
	return nil
}

func reconcileImportedModel(ctx context.Context, tx *sql.Tx, model *Model) error {
	if err := reconcileCounts(ctx, tx, model); err != nil {
		return err
	}
	if err := reconcileIDs(ctx, tx, model); err != nil {
		return err
	}
	if err := reconcileBalances(ctx, tx, model); err != nil {
		return err
	}
	if err := reconcileTransactionTotals(ctx, tx, model); err != nil {
		return err
	}
	if err := reconcileMonths(ctx, tx, model); err != nil {
		return err
	}
	if err := reconcileGoals(ctx, tx, model); err != nil {
		return err
	}
	if err := reconcileDebts(ctx, tx, model); err != nil {
		return err
	}
	return nil
}

func reconcileTransactionTotals(ctx context.Context, tx *sql.Tx, model *Model) error {
	var income, expense, transfer, cashIncome, cashExpense string
	err := tx.QueryRowContext(ctx, `
		SELECT
		 COALESCE(sum(amount_cents::numeric) FILTER (WHERE transaction_type='income'),0)::text,
		 COALESCE(sum(amount_cents::numeric) FILTER (WHERE transaction_type='expense'),0)::text,
		 COALESCE(sum(amount_cents::numeric) FILTER (WHERE transaction_type='transfer'),0)::text,
		 COALESCE(sum(amount_cents::numeric) FILTER (WHERE transaction_type='income' AND NOT is_balance_adjustment),0)::text,
		 COALESCE(sum(amount_cents::numeric) FILTER (WHERE transaction_type='expense' AND NOT is_balance_adjustment),0)::text
		FROM transactions WHERE household_id=$1 AND deleted_at IS NULL
	`, model.HouseholdID).Scan(&income, &expense, &transfer, &cashIncome, &cashExpense)
	if err != nil {
		return confirmRepositoryError(ctx, err)
	}
	if !decimalEquals(income, model.Totals.IncomeCents) ||
		!decimalEquals(expense, model.Totals.ExpenseCents) ||
		!decimalEquals(transfer, model.Totals.TransferCents) ||
		!decimalEquals(cashIncome, model.Totals.CashFlowIncomeCents) ||
		!decimalEquals(cashExpense, model.Totals.CashFlowExpenseCents) {
		return ErrReconciliation
	}
	return nil
}

func reconcileGoals(ctx context.Context, tx *sql.Tx, model *Model) error {
	wants := make(map[uuid.UUID]int64, len(model.Goals))
	for _, goal := range model.Goals {
		wants[goal.ID] = goal.InitialSavedCents
	}
	rows, err := tx.QueryContext(ctx, `
		SELECT id::text, initial_saved_cents::numeric::text
		FROM goals WHERE household_id=$1 ORDER BY id
	`, model.HouseholdID)
	if err != nil {
		return confirmRepositoryError(ctx, err)
	}
	defer rows.Close()
	seen := 0
	for rows.Next() {
		var idText, saved string
		if err := rows.Scan(&idText, &saved); err != nil {
			return confirmRepositoryError(ctx, err)
		}
		id, parseErr := uuid.Parse(idText)
		want, exists := wants[id]
		if parseErr != nil || !exists || !decimalEquals(saved, want) {
			return ErrReconciliation
		}
		seen++
	}
	if err := rows.Err(); err != nil {
		return confirmRepositoryError(ctx, err)
	}
	if seen != len(wants) {
		return ErrReconciliation
	}
	return nil
}

func reconcileCounts(ctx context.Context, tx *sql.Tx, model *Model) error {
	var counts Counts
	var budgetSum string
	var recurring int
	err := tx.QueryRowContext(ctx, `
		SELECT
		 (SELECT count(*) FROM accounts WHERE household_id=$1),
		 (SELECT count(*) FROM categories WHERE household_id=$1),
		 (SELECT count(*) FROM transactions WHERE household_id=$1),
		 (SELECT count(*) FROM budgets WHERE household_id=$1),
		 (SELECT count(*) FROM goals WHERE household_id=$1),
		 (SELECT count(*) FROM goal_contributions WHERE household_id=$1),
		 (SELECT count(*) FROM debts WHERE household_id=$1),
		 (SELECT count(*) FROM debt_payments WHERE household_id=$1),
		 (SELECT count(*) FROM recurring_transactions WHERE household_id=$1),
		 (SELECT COALESCE(sum(amount_cents::numeric),0)::text FROM budgets WHERE household_id=$1)
	`, model.HouseholdID).Scan(
		&counts.Accounts, &counts.Categories, &counts.Transactions, &counts.Budgets,
		&counts.Goals, &counts.GoalContributions, &counts.Debts, &counts.DebtPayments,
		&recurring, &budgetSum,
	)
	if err != nil {
		return confirmRepositoryError(ctx, err)
	}
	if counts != model.Counts || recurring != 0 || !decimalEquals(budgetSum, model.Totals.BudgetCents) {
		return ErrReconciliation
	}
	return nil
}

func reconcileIDs(ctx context.Context, tx *sql.Tx, model *Model) error {
	sets := []struct {
		table string
		ids   []uuid.UUID
	}{
		{"accounts", idsOfAccounts(model.Accounts)}, {"categories", idsOfCategories(model.Categories)},
		{"transactions", idsOfTransactions(model.Transactions)}, {"budgets", idsOfBudgets(model.Budgets)},
		{"goals", idsOfGoals(model.Goals)}, {"debts", idsOfDebts(model.Debts)},
		{"debt_payments", idsOfPayments(model.DebtPayments)},
	}
	for _, set := range sets {
		rows, err := tx.QueryContext(ctx, `SELECT id::text FROM `+set.table+` WHERE household_id=$1 ORDER BY id`, model.HouseholdID)
		if err != nil {
			return confirmRepositoryError(ctx, err)
		}
		actual := make([]string, 0, len(set.ids))
		for rows.Next() {
			var id string
			if err := rows.Scan(&id); err != nil {
				rows.Close()
				return confirmRepositoryError(ctx, err)
			}
			actual = append(actual, id)
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return confirmRepositoryError(ctx, err)
		}
		expected := make([]string, len(set.ids))
		for index, id := range set.ids {
			expected[index] = id.String()
		}
		sort.Strings(expected)
		if fmt.Sprint(actual) != fmt.Sprint(expected) {
			return ErrReconciliation
		}
	}
	return nil
}

func reconcileBalances(ctx context.Context, tx *sql.Tx, model *Model) error {
	rows, err := tx.QueryContext(ctx, `
		SELECT a.id::text,
		 COALESCE(sum(CASE
		  WHEN t.transaction_type='income' AND t.account_id=a.id THEN t.amount_cents::numeric
		  WHEN t.transaction_type='expense' AND t.account_id=a.id THEN -t.amount_cents::numeric
		  WHEN t.transaction_type='transfer' AND t.account_id=a.id THEN -t.amount_cents::numeric
		  WHEN t.transaction_type='transfer' AND t.to_account_id=a.id THEN t.amount_cents::numeric
		  ELSE 0::numeric END),0)::text
		FROM accounts a
		LEFT JOIN transactions t ON t.household_id=a.household_id
		 AND (t.account_id=a.id OR t.to_account_id=a.id) AND t.deleted_at IS NULL
		WHERE a.household_id=$1
		GROUP BY a.id ORDER BY a.id
	`, model.HouseholdID)
	if err != nil {
		return confirmRepositoryError(ctx, err)
	}
	defer rows.Close()
	household := new(big.Int)
	seen := 0
	for rows.Next() {
		var idText, balanceText string
		if err := rows.Scan(&idText, &balanceText); err != nil {
			return confirmRepositoryError(ctx, err)
		}
		id, parseErr := uuid.Parse(idText)
		if parseErr != nil {
			return ErrReconciliation
		}
		want, exists := model.Reconciliation.AccountBalances[id]
		if !exists || !decimalEquals(balanceText, want) {
			return ErrReconciliation
		}
		value, ok := new(big.Int).SetString(balanceText, 10)
		if !ok {
			return ErrReconciliation
		}
		household.Add(household, value)
		seen++
	}
	if err := rows.Err(); err != nil {
		return confirmRepositoryError(ctx, err)
	}
	if seen != len(model.Reconciliation.AccountBalances) || household.String() != strconv.FormatInt(model.Totals.HouseholdBalanceCents, 10) {
		return ErrReconciliation
	}
	return nil
}

func reconcileMonths(ctx context.Context, tx *sql.Tx, model *Model) error {
	rows, err := tx.QueryContext(ctx, `
		SELECT to_char(event_date, 'YYYY-MM'),
		 count(*) FILTER (WHERE transaction_type='income'),
		 COALESCE(sum(amount_cents::numeric) FILTER (WHERE transaction_type='income'),0)::text,
		 count(*) FILTER (WHERE transaction_type='expense'),
		 COALESCE(sum(amount_cents::numeric) FILTER (WHERE transaction_type='expense'),0)::text,
		 count(*) FILTER (WHERE transaction_type='transfer'),
		 COALESCE(sum(amount_cents::numeric) FILTER (WHERE transaction_type='transfer'),0)::text,
		 COALESCE(sum(amount_cents::numeric) FILTER (WHERE transaction_type='income' AND NOT is_balance_adjustment),0)::text,
		 COALESCE(sum(amount_cents::numeric) FILTER (WHERE transaction_type='expense' AND NOT is_balance_adjustment),0)::text
		FROM transactions WHERE household_id=$1 AND deleted_at IS NULL
		GROUP BY 1 ORDER BY 1
	`, model.HouseholdID)
	if err != nil {
		return confirmRepositoryError(ctx, err)
	}
	defer rows.Close()
	seen := 0
	for rows.Next() {
		var month, income, expense, transfer, cashIncome, cashExpense string
		var incomeCount, expenseCount, transferCount int
		if err := rows.Scan(&month, &incomeCount, &income, &expenseCount, &expense, &transferCount, &transfer, &cashIncome, &cashExpense); err != nil {
			return confirmRepositoryError(ctx, err)
		}
		want, exists := model.Reconciliation.Monthly[month]
		if !exists || incomeCount != want.IncomeCount || expenseCount != want.ExpenseCount || transferCount != want.TransferCount ||
			!decimalEquals(income, want.IncomeCents) || !decimalEquals(expense, want.ExpenseCents) ||
			!decimalEquals(transfer, want.TransferCents) || !decimalEquals(cashIncome, want.CashFlowIncomeCents) ||
			!decimalEquals(cashExpense, want.CashFlowExpenseCents) {
			return ErrReconciliation
		}
		seen++
	}
	if err := rows.Err(); err != nil {
		return confirmRepositoryError(ctx, err)
	}
	if seen != len(model.Reconciliation.Monthly) {
		return ErrReconciliation
	}
	return nil
}

func reconcileDebts(ctx context.Context, tx *sql.Tx, model *Model) error {
	wants := make(map[uuid.UUID]DebtReconciliation, len(model.Reconciliation.Debts))
	for _, debt := range model.Reconciliation.Debts {
		wants[debt.DebtID] = debt
	}
	rows, err := tx.QueryContext(ctx, `
		SELECT d.id::text, COALESCE(sum(p.amount_cents::numeric) FILTER (WHERE p.deleted_at IS NULL),0)::text,
		 greatest(d.original_amount_cents::numeric - COALESCE(sum(p.amount_cents::numeric) FILTER (WHERE p.deleted_at IS NULL),0),0)::text
		FROM debts d LEFT JOIN debt_payments p ON p.household_id=d.household_id AND p.debt_id=d.id
		WHERE d.household_id=$1 GROUP BY d.id ORDER BY d.id
	`, model.HouseholdID)
	if err != nil {
		return confirmRepositoryError(ctx, err)
	}
	defer rows.Close()
	seen := 0
	for rows.Next() {
		var idText, paid, left string
		if err := rows.Scan(&idText, &paid, &left); err != nil {
			return confirmRepositoryError(ctx, err)
		}
		id, parseErr := uuid.Parse(idText)
		want, exists := wants[id]
		if parseErr != nil || !exists || !decimalEquals(paid, want.PaidCents) || !decimalEquals(left, want.LeftCents) {
			return ErrReconciliation
		}
		seen++
	}
	if err := rows.Err(); err != nil {
		return confirmRepositoryError(ctx, err)
	}
	if seen != len(wants) {
		return ErrReconciliation
	}
	return nil
}

func insertImportRun(ctx context.Context, tx *sql.Tx, command ConfirmCommand, actorID uuid.UUID, response ConfirmResponse) error {
	_, err := tx.ExecContext(ctx, `
		INSERT INTO backup_v5_import_runs (
		 id, household_id, actor_user_id, hmac_key_id, idempotency_key_hmac,
		 request_fingerprint_hmac, policy_version, status,
		 accounts_count, categories_count, transactions_count, budgets_count,
		 goals_count, goal_contributions_count, debts_count, debt_payments_count,
		 legacy_owner_not_linked_count, archive_time_approximated_count,
		 goal_exceeds_target_count, debt_overpaid_count, system_resource_preserved_count,
		 budget_month_explicit_choice_count, completed_at, created_at
		) VALUES ($1,$2,$3,$4,$5,$6,$7,'completed',$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18,$19,$20,$21,$22,$22)
	`, response.ImportRunID, command.HouseholdID, actorID, command.ActiveKeyID,
		command.ActiveLookup[:], command.Fingerprint[:], PolicyVersion,
		response.Counts.Accounts, response.Counts.Categories, response.Counts.Transactions,
		response.Counts.Budgets, response.Counts.Goals, response.Counts.GoalContributions,
		response.Counts.Debts, response.Counts.DebtPayments,
		response.WarningCounts.LegacyOwnerNotLinked, response.WarningCounts.ArchiveTimeApproximated,
		response.WarningCounts.GoalExceedsTarget, response.WarningCounts.DebtOverpaid,
		response.WarningCounts.SystemResourcePreserved, response.WarningCounts.BudgetMonthExplicitChoice,
		response.CompletedAt)
	if err != nil {
		return confirmRepositoryError(ctx, err)
	}
	return nil
}

func insertImportAudit(ctx context.Context, tx *sql.Tx, command ConfirmCommand, actorID uuid.UUID, response ConfirmResponse) error {
	auditID, err := uuid.NewRandom()
	if err != nil {
		return ErrRepository
	}
	changes := struct {
		Source        string        `json:"source"`
		PolicyVersion string        `json:"policyVersion"`
		Counts        PreviewCounts `json:"counts"`
	}{Source: "import", PolicyVersion: PolicyVersion, Counts: response.Counts}
	encoded, err := json.Marshal(changes)
	if err != nil {
		return ErrRepository
	}
	_, err = tx.ExecContext(ctx, `
		INSERT INTO audit_log (id, household_id, actor_user_id, entity_type, entity_id, action, occurred_at, request_id, changes)
		VALUES ($1,$2,$3,'households',$2,'imported',$4,$5,$6::jsonb)
	`, auditID, command.HouseholdID, actorID, response.CompletedAt, command.ServerRequestID, encoded)
	if err != nil {
		return confirmRepositoryError(ctx, err)
	}
	return nil
}

func confirmResponse(runID uuid.UUID, completedAt time.Time, model *Model) ConfirmResponse {
	return ConfirmResponse{
		ImportRunID: runID.String(), Status: "completed", PolicyVersion: PolicyVersion, CompletedAt: completedAt,
		Counts: PreviewCounts{
			Accounts: model.Counts.Accounts, Categories: model.Counts.Categories,
			Transactions: model.Counts.Transactions, Budgets: model.Counts.Budgets,
			Goals: model.Counts.Goals, GoalContributions: model.Counts.GoalContributions,
			Debts: model.Counts.Debts, DebtPayments: model.Counts.DebtPayments,
		},
		WarningCounts: ConfirmWarningCounts{
			LegacyOwnerNotLinked:      model.Warnings.LegacyOwnerNotLinked,
			ArchiveTimeApproximated:   model.Warnings.ArchiveTimeApproximated,
			GoalExceedsTarget:         model.Warnings.GoalExceedsTarget,
			DebtOverpaid:              model.Warnings.DebtOverpaid,
			SystemResourcePreserved:   model.Warnings.SystemResourcePreserved,
			BudgetMonthExplicitChoice: model.Warnings.BudgetMonthExplicitChoice,
		},
	}
}

func decimalEquals(value string, expected int64) bool {
	parsed, ok := new(big.Int).SetString(value, 10)
	return ok && parsed.String() == strconv.FormatInt(expected, 10)
}

func idsOfAccounts(values []Account) []uuid.UUID {
	result := make([]uuid.UUID, len(values))
	for i, v := range values {
		result[i] = v.ID
	}
	return result
}
func idsOfCategories(values []Category) []uuid.UUID {
	result := make([]uuid.UUID, len(values))
	for i, v := range values {
		result[i] = v.ID
	}
	return result
}
func idsOfTransactions(values []Transaction) []uuid.UUID {
	result := make([]uuid.UUID, len(values))
	for i, v := range values {
		result[i] = v.ID
	}
	return result
}
func idsOfBudgets(values []Budget) []uuid.UUID {
	result := make([]uuid.UUID, len(values))
	for i, v := range values {
		result[i] = v.ID
	}
	return result
}
func idsOfGoals(values []Goal) []uuid.UUID {
	result := make([]uuid.UUID, len(values))
	for i, v := range values {
		result[i] = v.ID
	}
	return result
}
func idsOfDebts(values []Debt) []uuid.UUID {
	result := make([]uuid.UUID, len(values))
	for i, v := range values {
		result[i] = v.ID
	}
	return result
}
func idsOfPayments(values []DebtPayment) []uuid.UUID {
	result := make([]uuid.UUID, len(values))
	for i, v := range values {
		result[i] = v.ID
	}
	return result
}

func confirmRepositoryError(ctx context.Context, err error) error {
	if ctxErr := ctx.Err(); ctxErr != nil {
		return ctxErr
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return err
	}
	var postgresError *pgconn.PgError
	if errors.As(err, &postgresError) {
		switch postgresError.Code {
		case "23505":
			return ErrImportStateConflict
		case "23503", "23514", "22003", "55000", "P0001":
			return ErrRepository
		}
	}
	return ErrRepository
}
