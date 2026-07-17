package finance

import (
	"context"
	"database/sql"
	"errors"
	"math"
	"os"
	"sync"
	"testing"

	_ "github.com/jackc/pgx/v5/stdlib"
)

const (
	financeOwnerSubject  = "fa000000-0000-4000-8000-000000000001"
	financeAdminSubject  = "fa000000-0000-4000-8000-000000000002"
	financeMemberSubject = "fa000000-0000-4000-8000-000000000003"
	financeOtherSubject  = "fa000000-0000-4000-8000-000000000004"
	financeHouseholdID   = "fb000000-0000-4000-8000-000000000001"
	financeOtherHouseID  = "fb000000-0000-4000-8000-000000000002"
)

func financeIntegrationDB(t *testing.T) *sql.DB {
	t.Helper()
	url := os.Getenv("DATABASE_TEST_URL")
	if url == "" {
		t.Skip("DATABASE_TEST_URL is not set")
	}
	db, err := sql.Open("pgx", url)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := db.PingContext(context.Background()); err != nil {
		t.Fatal(err)
	}
	_, err = db.Exec(`TRUNCATE TABLE backup_v5_import_runs,backup_v5_import_previews,audit_log,transactions,recurring_transactions,debt_payments,debts,goal_contributions,goals,budgets,household_invitations,categories,accounts,household_members,households,users`)
	if err != nil {
		t.Fatal(err)
	}
	_, err = db.Exec(`INSERT INTO users(id,auth_subject,display_name) VALUES
		($1::uuid,$1::text,'Owner'),($2::uuid,$2::text,'Admin'),($3::uuid,$3::text,'Member'),($4::uuid,$4::text,'Other')`, financeOwnerSubject, financeAdminSubject, financeMemberSubject, financeOtherSubject)
	if err != nil {
		t.Fatal(err)
	}
	_, err = db.Exec(`INSERT INTO households(id,name,created_by_user_id) VALUES ($1::uuid,'Finance',$2::uuid),($3::uuid,'Other',$4::uuid)`, financeHouseholdID, financeOwnerSubject, financeOtherHouseID, financeOtherSubject)
	if err != nil {
		t.Fatal(err)
	}
	_, err = db.Exec(`INSERT INTO household_members(id,household_id,user_id,role) VALUES
		(gen_random_uuid(),$1::uuid,$2::uuid,'owner'),(gen_random_uuid(),$1::uuid,$3::uuid,'admin'),(gen_random_uuid(),$1::uuid,$4::uuid,'member'),(gen_random_uuid(),$5::uuid,$6::uuid,'owner')`, financeHouseholdID, financeOwnerSubject, financeAdminSubject, financeMemberSubject, financeOtherHouseID, financeOtherSubject)
	if err != nil {
		t.Fatal(err)
	}
	return db
}

func requireFinanceError(t *testing.T, err, want error) {
	t.Helper()
	if !errors.Is(err, want) {
		t.Fatalf("error=%v want %v", err, want)
	}
}

func TestPostgresFinanceCoreLifecycleAndSummary(t *testing.T) {
	db := financeIntegrationDB(t)
	service := NewService(NewPostgresRepository(db))
	ctx := context.Background()
	accountInput := CreateAccountInput{Name: "Main", Color: "#112233"}
	mainResult, err := service.CreateAccount(ctx, financeOwnerSubject, financeHouseholdID, "account-main", "req-account-main", accountInput)
	if err != nil {
		t.Fatal(err)
	}
	replay, err := service.CreateAccount(ctx, financeAdminSubject, financeHouseholdID, "account-main", "req-account-replay", accountInput)
	if err != nil || !replay.Replayed || replay.Value.ID != mainResult.Value.ID {
		t.Fatalf("account replay %#v %v", replay, err)
	}
	_, err = service.CreateAccount(ctx, financeOwnerSubject, financeHouseholdID, "account-main", "req-account-conflict", CreateAccountInput{Name: "Changed", Color: "#112233"})
	requireFinanceError(t, err, ErrIdempotency)
	_, err = service.CreateAccount(ctx, financeMemberSubject, financeHouseholdID, "member-account", "req-member-account", accountInput)
	requireFinanceError(t, err, ErrForbidden)

	savings, err := service.CreateAccount(ctx, financeAdminSubject, financeHouseholdID, "account-save", "req-account-save", CreateAccountInput{Name: "Savings", Color: "#445566", AccountType: "savings"})
	if err != nil {
		t.Fatal(err)
	}
	_, err = service.CreateAccount(ctx, financeOwnerSubject, financeHouseholdID, "account-third", "req-account-third", CreateAccountInput{Name: "Third", Color: "#778899"})
	if err != nil {
		t.Fatal(err)
	}
	page1, err := service.ListAccounts(ctx, financeMemberSubject, financeHouseholdID, AccountListInput{Limit: 2})
	if err != nil || len(page1.Accounts) != 2 || page1.NextCursor == nil {
		t.Fatalf("account page1 %#v %v", page1, err)
	}
	page2, err := service.ListAccounts(ctx, financeMemberSubject, financeHouseholdID, AccountListInput{Limit: 2, Cursor: *page1.NextCursor})
	if err != nil || len(page2.Accounts) != 1 {
		t.Fatalf("account page2 %#v %v", page2, err)
	}
	emptyAccounts, err := service.ListAccounts(ctx, financeMemberSubject, financeHouseholdID, AccountListInput{State: "archived"})
	if err != nil || emptyAccounts.Accounts == nil || len(emptyAccounts.Accounts) != 0 {
		t.Fatalf("non-nil empty accounts %#v %v", emptyAccounts, err)
	}
	_, err = service.ListAccounts(ctx, financeOtherSubject, financeHouseholdID, AccountListInput{})
	requireFinanceError(t, err, ErrNotFound)
	_, err = db.Exec(`UPDATE household_members SET status='removed',removed_at=CURRENT_TIMESTAMP WHERE household_id=$1 AND user_id=$2`, financeHouseholdID, financeMemberSubject)
	if err != nil {
		t.Fatal(err)
	}
	_, err = service.ListAccounts(ctx, financeMemberSubject, financeHouseholdID, AccountListInput{})
	requireFinanceError(t, err, ErrNotFound)
	_, err = db.Exec(`UPDATE household_members SET status='active',removed_at=NULL WHERE household_id=$1 AND user_id=$2`, financeHouseholdID, financeMemberSubject)
	if err != nil {
		t.Fatal(err)
	}
	_, err = db.Exec(`UPDATE users SET deleted_at=CURRENT_TIMESTAMP WHERE id=$1`, financeMemberSubject)
	if err != nil {
		t.Fatal(err)
	}
	_, err = service.ListAccounts(ctx, financeMemberSubject, financeHouseholdID, AccountListInput{})
	requireFinanceError(t, err, ErrNotFound)
	_, err = db.Exec(`UPDATE users SET deleted_at=NULL WHERE id=$1`, financeMemberSubject)
	if err != nil {
		t.Fatal(err)
	}

	income, err := service.CreateCategory(ctx, financeOwnerSubject, financeHouseholdID, "cat-income", "req-cat-income", CreateCategoryInput{Type: "income", Name: "Salary", Color: "#22AA44"})
	if err != nil {
		t.Fatal(err)
	}
	expense, err := service.CreateCategory(ctx, financeAdminSubject, financeHouseholdID, "cat-expense", "req-cat-expense", CreateCategoryInput{Type: "expense", Name: "Food", Color: "#AA2244"})
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 3; i++ {
		_, err = service.CreateCategory(ctx, financeOwnerSubject, financeHouseholdID, "cat-page-"+formatInt64(int64(i)), "req-cat-page-"+formatInt64(int64(i)), CreateCategoryInput{Type: "expense", Name: "Page " + formatInt64(int64(i)), Color: "#123456"})
		if err != nil {
			t.Fatal(err)
		}
	}
	catPage, err := service.ListCategories(ctx, financeMemberSubject, financeHouseholdID, CategoryListInput{Type: "expense", Limit: 2})
	if err != nil || len(catPage.Categories) != 2 || catPage.NextCursor == nil {
		t.Fatalf("category pagination %#v %v", catPage, err)
	}

	createTx := func(key, typ, account string, to, category *string, amount string, adjust bool) Transaction {
		result, e := service.CreateTransaction(ctx, financeMemberSubject, financeHouseholdID, key, "req-"+key, CreateTransactionInput{Type: typ, AccountID: account, ToAccountID: to, CategoryID: category, AmountCents: amount, EventDate: "2026-07-15", Note: "private note", IsBalanceAdjustment: adjust})
		if e != nil {
			t.Fatal(e)
		}
		return result.Value
	}
	mainID, savingsID, incomeID, expenseID := mainResult.Value.ID, savings.Value.ID, income.Value.ID, expense.Value.ID
	incomeTx := createTx("tx-income", "income", mainID, nil, &incomeID, "10000", false)
	createTx("tx-expense", "expense", mainID, nil, &expenseID, "3000", false)
	createTx("tx-adjust", "income", mainID, nil, &incomeID, "500", true)
	createTx("tx-transfer", "transfer", mainID, &savingsID, nil, "2000", false)
	txPage1, err := service.ListTransactions(ctx, financeMemberSubject, financeHouseholdID, TransactionListInput{Limit: 2})
	if err != nil || len(txPage1.Transactions) != 2 || txPage1.NextCursor == nil {
		t.Fatalf("transaction page1 %#v %v", txPage1, err)
	}
	txPage2, err := service.ListTransactions(ctx, financeMemberSubject, financeHouseholdID, TransactionListInput{Limit: 2, Cursor: *txPage1.NextCursor})
	if err != nil || len(txPage2.Transactions) != 2 {
		t.Fatalf("transaction page2 %#v %v", txPage2, err)
	}

	summary, err := service.GetSummary(ctx, financeMemberSubject, financeHouseholdID, SummaryRangeInput{From: "2026-07-01", To: "2026-07-31"})
	if err != nil {
		t.Fatal(err)
	}
	if summary.HouseholdTotalCents != "7500" || summary.CashFlow.IncomeCents != "10000" || summary.CashFlow.ExpenseCents != "3000" {
		t.Fatalf("summary=%#v", summary)
	}
	balances, err := service.ListAccountBalances(ctx, financeMemberSubject, financeHouseholdID, AccountBalanceListInput{To: "2026-07-31", Limit: 2})
	if err != nil || len(balances.AccountBalances) != 2 || balances.NextCursor == nil {
		t.Fatalf("balances=%#v %v", balances, err)
	}
	expenses, err := service.ListCategoryExpenses(ctx, financeMemberSubject, financeHouseholdID, CategoryExpenseListInput{From: "2026-07-01", To: "2026-07-31", Limit: 2})
	if err != nil || len(expenses.ExpenseByCategory) != 2 || expenses.NextCursor == nil {
		t.Fatalf("category expenses=%#v %v", expenses, err)
	}

	archived, err := service.SetAccountArchived(ctx, financeAdminSubject, financeHouseholdID, savingsID, savings.Value.Version, "req-archive", true)
	if err != nil {
		t.Fatal(err)
	}
	afterArchive, err := service.GetSummary(ctx, financeOwnerSubject, financeHouseholdID, SummaryRangeInput{From: "2026-07-01", To: "2026-07-31"})
	if err != nil || afterArchive.HouseholdTotalCents != summary.HouseholdTotalCents {
		t.Fatalf("archive changed total %#v %v", afterArchive, err)
	}
	archivedBalances, err := service.ListAccountBalances(ctx, financeOwnerSubject, financeHouseholdID, AccountBalanceListInput{To: "2026-07-31"})
	if err != nil {
		t.Fatal(err)
	}
	foundArchived := false
	for _, b := range archivedBalances.AccountBalances {
		if b.AccountID == savingsID && b.ArchivedAt != nil {
			foundArchived = true
		}
	}
	if !foundArchived {
		t.Fatal("nonzero archived account missing")
	}
	_, err = service.CreateTransaction(ctx, financeMemberSubject, financeHouseholdID, "archived-ref", "req-archived-ref", CreateTransactionInput{Type: "transfer", AccountID: mainID, ToAccountID: &savingsID, AmountCents: "1", EventDate: "2026-07-15"})
	requireFinanceError(t, err, ErrNotFound)
	_, err = service.SetAccountArchived(ctx, financeOwnerSubject, financeHouseholdID, savingsID, archived.Version, "req-restore", false)
	if err != nil {
		t.Fatal(err)
	}

	deleted, err := service.DeleteTransaction(ctx, financeMemberSubject, financeHouseholdID, incomeTx.ID, incomeTx.Version, "req-delete", "duplicate")
	if err != nil {
		t.Fatal(err)
	}
	_, err = service.DeleteTransaction(ctx, financeMemberSubject, financeHouseholdID, incomeTx.ID, incomeTx.Version, "req-stale", "again")
	requireFinanceError(t, err, ErrVersionConflict)
	deletedPage, err := service.ListTransactions(ctx, financeMemberSubject, financeHouseholdID, TransactionListInput{State: "deleted"})
	if err != nil || len(deletedPage.Transactions) != 1 || deletedPage.Transactions[0].ID != incomeTx.ID {
		t.Fatalf("deleted state page %#v %v", deletedPage, err)
	}
	restored, err := service.RestoreTransaction(ctx, financeMemberSubject, financeHouseholdID, incomeTx.ID, deleted.Version, "req-restore-tx")
	if err != nil || restored.DeletedAt != nil {
		t.Fatalf("restore %#v %v", restored, err)
	}

	foreignFilter, err := service.ListTransactions(ctx, financeMemberSubject, financeHouseholdID, TransactionListInput{AccountID: "fc000000-0000-4000-8000-000000000099"})
	if err != nil || foreignFilter.Transactions == nil || len(foreignFilter.Transactions) != 0 {
		t.Fatalf("foreign filter %#v %v", foreignFilter, err)
	}
	_, err = db.Exec(`UPDATE accounts SET is_system=true WHERE id=$1`, mainID)
	if err != nil {
		t.Fatal(err)
	}
	_, err = service.UpdateAccount(ctx, financeOwnerSubject, financeHouseholdID, mainID, mainResult.Value.Version, "req-system", AccountPatchInput{Name: Field[string]{Present: true, Value: "No"}})
	requireFinanceError(t, err, ErrSystemImmutable)
	_, err = db.Exec(`UPDATE categories SET is_system=true WHERE id=$1`, expenseID)
	if err != nil {
		t.Fatal(err)
	}
	_, err = service.UpdateCategory(ctx, financeOwnerSubject, financeHouseholdID, expenseID, expense.Value.Version, "req-system-category", CategoryPatchInput{Name: Field[string]{Present: true, Value: "No"}})
	requireFinanceError(t, err, ErrSystemImmutable)

	_, err = db.Exec(`CREATE FUNCTION finance_test_fail_audit() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'audit failure'; END $$; CREATE TRIGGER finance_test_fail_audit BEFORE INSERT ON audit_log FOR EACH ROW WHEN (NEW.request_id='fail-audit') EXECUTE FUNCTION finance_test_fail_audit()`)
	if err != nil {
		t.Fatal(err)
	}
	_, err = service.CreateAccount(ctx, financeOwnerSubject, financeHouseholdID, "audit-failure", "fail-audit", CreateAccountInput{Name: "Must rollback", Color: "#123456"})
	if err == nil {
		t.Fatal("forced audit failure succeeded")
	}
	var rolledBack int
	_ = db.QueryRow(`SELECT count(*) FROM accounts WHERE creation_idempotency_key='audit-failure'`).Scan(&rolledBack)
	if rolledBack != 0 {
		t.Fatal("audit failure did not roll back entity")
	}
	_, _ = db.Exec(`DROP TRIGGER finance_test_fail_audit ON audit_log; DROP FUNCTION finance_test_fail_audit()`)

	var leaked int
	err = db.QueryRow(`SELECT count(*) FROM audit_log WHERE changes::text LIKE '%private note%' OR changes::text LIKE '%duplicate%'`).Scan(&leaked)
	if err != nil || leaked != 0 {
		t.Fatalf("audit leaked content count=%d err=%v", leaked, err)
	}
	var requestCount int
	err = db.QueryRow(`SELECT count(*) FROM audit_log WHERE request_id='req-tx-income' AND actor_user_id=$1`, financeMemberSubject).Scan(&requestCount)
	if err != nil || requestCount != 1 {
		t.Fatalf("audit actor/request count=%d %v", requestCount, err)
	}
}

func TestPostgresFinanceIdempotencyConcurrencyLegacyAndVersionMax(t *testing.T) {
	db := financeIntegrationDB(t)
	service := NewService(NewPostgresRepository(db))
	ctx := context.Background()
	cat, err := service.CreateCategory(ctx, financeOwnerSubject, financeHouseholdID, "concurrent-cat", "req-concurrent-cat", CreateCategoryInput{Type: "expense", Name: "Concurrent", Color: "#123456"})
	if err != nil {
		t.Fatal(err)
	}
	account, err := service.CreateAccount(ctx, financeOwnerSubject, financeHouseholdID, "concurrent-account", "req-concurrent-account", CreateAccountInput{Name: "Concurrent", Color: "#654321"})
	if err != nil {
		t.Fatal(err)
	}
	foreignAccount, err := service.CreateAccount(ctx, financeOtherSubject, financeOtherHouseID, "foreign-account", "req-foreign-account", CreateAccountInput{Name: "Foreign", Color: "#112233"})
	if err != nil {
		t.Fatal(err)
	}
	_, err = service.CreateTransaction(ctx, financeMemberSubject, financeHouseholdID, "cross-account", "req-cross-account", CreateTransactionInput{Type: "expense", AccountID: foreignAccount.Value.ID, CategoryID: &cat.Value.ID, AmountCents: "1", EventDate: "2026-07-15"})
	requireFinanceError(t, err, ErrNotFound)
	input := CreateTransactionInput{Type: "expense", AccountID: account.Value.ID, CategoryID: &cat.Value.ID, AmountCents: "10", EventDate: "2026-07-15"}
	results := make([]CreateResult[Transaction], 2)
	errs := make([]error, 2)
	var wg sync.WaitGroup
	for i := range 2 {
		wg.Add(1)
		go func(index int) {
			defer wg.Done()
			results[index], errs[index] = service.CreateTransaction(ctx, financeMemberSubject, financeHouseholdID, "same-key", "req-concurrent-"+formatInt64(int64(index)), input)
		}(i)
	}
	wg.Wait()
	for _, err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	if results[0].Value.ID != results[1].Value.ID {
		t.Fatal("concurrent idempotency created two IDs")
	}
	var rows, audits int
	_ = db.QueryRow(`SELECT count(*) FROM transactions WHERE idempotency_key='same-key'`).Scan(&rows)
	_ = db.QueryRow(`SELECT count(*) FROM audit_log WHERE entity_type='transactions' AND entity_id=$1`, results[0].Value.ID).Scan(&audits)
	if rows != 1 || audits != 1 {
		t.Fatalf("rows=%d audits=%d", rows, audits)
	}

	_, err = db.Exec(`INSERT INTO transactions(id,household_id,transaction_type,account_id,category_id,amount_cents,event_date,idempotency_key) VALUES(gen_random_uuid(),$1,'expense',$2,$3,11,'2026-07-15','legacy-no-hash')`, financeHouseholdID, account.Value.ID, cat.Value.ID)
	if err != nil {
		t.Fatal(err)
	}
	_, err = service.CreateTransaction(ctx, financeMemberSubject, financeHouseholdID, "legacy-no-hash", "req-legacy", input)
	requireFinanceError(t, err, ErrIdempotency)

	_, err = db.Exec(`UPDATE accounts SET version=$2 WHERE id=$1`, account.Value.ID, math.MaxInt64)
	if err != nil {
		t.Fatal(err)
	}
	before := 0
	_ = db.QueryRow(`SELECT count(*) FROM audit_log WHERE entity_type='accounts' AND entity_id=$1`, account.Value.ID).Scan(&before)
	_, err = service.UpdateAccount(ctx, financeOwnerSubject, financeHouseholdID, account.Value.ID, "9223372036854775807", "req-max", AccountPatchInput{Name: Field[string]{Present: true, Value: "Max"}})
	requireFinanceError(t, err, ErrVersionExhausted)
	after := 0
	_ = db.QueryRow(`SELECT count(*) FROM audit_log WHERE entity_type='accounts' AND entity_id=$1`, account.Value.ID).Scan(&after)
	if before != after {
		t.Fatal("version exhausted wrote audit")
	}

	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	_, err = service.CreateCategory(cancelled, financeOwnerSubject, financeHouseholdID, "cancelled-key", "req-cancelled", CreateCategoryInput{Type: "expense", Name: "Cancelled", Color: "#123456"})
	if err == nil {
		t.Fatal("cancelled mutation succeeded")
	}
	var cancelledRows int
	_ = db.QueryRow(`SELECT count(*) FROM categories WHERE creation_idempotency_key='cancelled-key'`).Scan(&cancelledRows)
	if cancelledRows != 0 {
		t.Fatal("cancelled mutation was not rolled back")
	}
}
