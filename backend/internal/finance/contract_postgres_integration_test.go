package finance

import (
	"context"
	"errors"
	"sync"
	"testing"
)

func TestPostgresFinanceRoleVersionAndReferenceContracts(t *testing.T) {
	db := financeIntegrationDB(t)
	service := NewService(NewPostgresRepository(db))
	ctx := context.Background()

	source, err := service.CreateAccount(ctx, financeOwnerSubject, financeHouseholdID, "role-source", "role-source", CreateAccountInput{Name: "Source", Color: "#112233"})
	if err != nil {
		t.Fatal(err)
	}
	destination, err := service.CreateAccount(ctx, financeOwnerSubject, financeHouseholdID, "role-destination", "role-destination", CreateAccountInput{Name: "Destination", Color: "#223344"})
	if err != nil {
		t.Fatal(err)
	}
	expense, err := service.CreateCategory(ctx, financeOwnerSubject, financeHouseholdID, "role-expense", "role-expense", CreateCategoryInput{Type: "expense", Name: "Expense", Color: "#334455"})
	if err != nil {
		t.Fatal(err)
	}

	_, err = service.UpdateAccount(ctx, financeMemberSubject, financeHouseholdID, source.Value.ID, source.Value.Version, "member-account-update", AccountPatchInput{Name: Field[string]{Present: true, Value: "Forbidden"}})
	requireFinanceError(t, err, ErrForbidden)
	_, err = service.SetAccountArchived(ctx, financeMemberSubject, financeHouseholdID, source.Value.ID, source.Value.Version, "member-account-archive", true)
	requireFinanceError(t, err, ErrForbidden)
	_, err = service.UpdateCategory(ctx, financeMemberSubject, financeHouseholdID, expense.Value.ID, expense.Value.Version, "member-category-update", CategoryPatchInput{Name: Field[string]{Present: true, Value: "Forbidden"}})
	requireFinanceError(t, err, ErrForbidden)
	_, err = service.SetCategoryArchived(ctx, financeMemberSubject, financeHouseholdID, expense.Value.ID, expense.Value.Version, "member-category-archive", true)
	requireFinanceError(t, err, ErrForbidden)

	memberTx, err := service.CreateTransaction(ctx, financeMemberSubject, financeHouseholdID, "member-tx", "member-tx-create", CreateTransactionInput{Type: "expense", AccountID: source.Value.ID, CategoryID: &expense.Value.ID, AmountCents: "10", EventDate: "2026-07-15"})
	if err != nil {
		t.Fatal(err)
	}
	updatedTx, err := service.UpdateTransaction(ctx, financeMemberSubject, financeHouseholdID, memberTx.Value.ID, memberTx.Value.Version, "member-tx-update", TransactionPatchInput{Note: Field[string]{Present: true, Value: "updated"}})
	if err != nil {
		t.Fatal(err)
	}
	deletedTx, err := service.DeleteTransaction(ctx, financeMemberSubject, financeHouseholdID, updatedTx.ID, updatedTx.Version, "member-tx-delete", "cleanup")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.RestoreTransaction(ctx, financeMemberSubject, financeHouseholdID, deletedTx.ID, deletedTx.Version, "member-tx-restore"); err != nil {
		t.Fatal(err)
	}

	var wait sync.WaitGroup
	results := make([]Account, 2)
	errs := make([]error, 2)
	for index := range 2 {
		wait.Add(1)
		go func(index int) {
			defer wait.Done()
			results[index], errs[index] = service.UpdateAccount(ctx, financeOwnerSubject, financeHouseholdID, source.Value.ID, source.Value.Version, "same-version-"+formatInt64(int64(index)), AccountPatchInput{Name: Field[string]{Present: true, Value: "Winner " + formatInt64(int64(index))}})
		}(index)
	}
	wait.Wait()
	successes, conflicts := 0, 0
	for _, updateErr := range errs {
		switch {
		case updateErr == nil:
			successes++
		case errors.Is(updateErr, ErrVersionConflict):
			conflicts++
		default:
			t.Fatalf("unexpected concurrent update error: %v", updateErr)
		}
	}
	if successes != 1 || conflicts != 1 {
		t.Fatalf("successes=%d conflicts=%d", successes, conflicts)
	}
	var version string
	var updateAudits int
	if err := db.QueryRow(`SELECT version::text FROM accounts WHERE id=$1`, source.Value.ID).Scan(&version); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow(`SELECT count(*) FROM audit_log WHERE entity_type='accounts' AND entity_id=$1 AND action='updated'`, source.Value.ID).Scan(&updateAudits); err != nil {
		t.Fatal(err)
	}
	if version != "2" || updateAudits != 1 {
		t.Fatalf("version=%s updateAudits=%d", version, updateAudits)
	}

	transfer, err := service.CreateTransaction(ctx, financeMemberSubject, financeHouseholdID, "history-transfer", "history-transfer", CreateTransactionInput{Type: "transfer", AccountID: source.Value.ID, ToAccountID: &destination.Value.ID, AmountCents: "5", EventDate: "2026-07-15"})
	if err != nil {
		t.Fatal(err)
	}
	archivedDestination, err := service.SetAccountArchived(ctx, financeOwnerSubject, financeHouseholdID, destination.Value.ID, destination.Value.Version, "archive-history-destination", true)
	if err != nil {
		t.Fatal(err)
	}
	patchedTransfer, err := service.UpdateTransaction(ctx, financeMemberSubject, financeHouseholdID, transfer.Value.ID, transfer.Value.Version, "patch-history-transfer", TransactionPatchInput{Note: Field[string]{Present: true, Value: "history remains"}})
	if err != nil {
		t.Fatal(err)
	}
	deletedTransfer, err := service.DeleteTransaction(ctx, financeMemberSubject, financeHouseholdID, patchedTransfer.ID, patchedTransfer.Version, "delete-history-transfer", "test")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.RestoreTransaction(ctx, financeMemberSubject, financeHouseholdID, deletedTransfer.ID, deletedTransfer.Version, "restore-history-transfer"); err != nil {
		t.Fatal(err)
	}

	newArchived, err := service.CreateAccount(ctx, financeOwnerSubject, financeHouseholdID, "new-archived", "new-archived", CreateAccountInput{Name: "New archived", Color: "#445566"})
	if err != nil {
		t.Fatal(err)
	}
	_, err = service.SetAccountArchived(ctx, financeOwnerSubject, financeHouseholdID, newArchived.Value.ID, newArchived.Value.Version, "archive-new", true)
	if err != nil {
		t.Fatal(err)
	}
	_, err = service.UpdateTransaction(ctx, financeMemberSubject, financeHouseholdID, transfer.Value.ID, "4", "assign-archived", TransactionPatchInput{ToAccountID: NullableField[string]{Present: true, Value: &newArchived.Value.ID}})
	requireFinanceError(t, err, ErrNotFound)

	deletedRef, err := service.CreateAccount(ctx, financeOwnerSubject, financeHouseholdID, "deleted-ref", "deleted-ref", CreateAccountInput{Name: "Deleted ref", Color: "#556677"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`UPDATE accounts SET deleted_at=CURRENT_TIMESTAMP WHERE id=$1`, deletedRef.Value.ID); err != nil {
		t.Fatal(err)
	}
	_, err = service.UpdateTransaction(ctx, financeMemberSubject, financeHouseholdID, transfer.Value.ID, "4", "assign-deleted", TransactionPatchInput{ToAccountID: NullableField[string]{Present: true, Value: &deletedRef.Value.ID}})
	requireFinanceError(t, err, ErrNotFound)

	archivedCategory, err := service.SetCategoryArchived(ctx, financeOwnerSubject, financeHouseholdID, expense.Value.ID, expense.Value.Version, "archive-history-category", true)
	if err != nil {
		t.Fatal(err)
	}
	currentMemberTx, err := service.UpdateTransaction(ctx, financeMemberSubject, financeHouseholdID, memberTx.Value.ID, "4", "patch-history-category", TransactionPatchInput{Note: Field[string]{Present: true, Value: "archived category retained"}})
	if err != nil {
		t.Fatal(err)
	}
	deletedHistory, err := service.DeleteTransaction(ctx, financeMemberSubject, financeHouseholdID, currentMemberTx.ID, currentMemberTx.Version, "delete-history-category", "test")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.RestoreTransaction(ctx, financeMemberSubject, financeHouseholdID, deletedHistory.ID, deletedHistory.Version, "restore-history-category"); err != nil {
		t.Fatal(err)
	}
	if archivedCategory.ArchivedAt == nil || archivedDestination.ArchivedAt == nil {
		t.Fatal("history references were not archived")
	}
}

func TestPostgresFinanceKeysetTraversalContracts(t *testing.T) {
	db := financeIntegrationDB(t)
	service := NewService(NewPostgresRepository(db))
	ctx := context.Background()
	accounts := make([]Account, 0, 7)
	categories := make([]Category, 0, 7)
	transactions := make([]Transaction, 0, 7)
	for index := 0; index < 7; index++ {
		suffix := formatInt64(int64(index))
		account, err := service.CreateAccount(ctx, financeOwnerSubject, financeHouseholdID, "page-account-"+suffix, "page-account-"+suffix, CreateAccountInput{Name: "Account " + suffix, Color: "#123456"})
		if err != nil {
			t.Fatal(err)
		}
		category, err := service.CreateCategory(ctx, financeOwnerSubject, financeHouseholdID, "page-category-"+suffix, "page-category-"+suffix, CreateCategoryInput{Type: "expense", Name: "Category " + suffix, Color: "#654321"})
		if err != nil {
			t.Fatal(err)
		}
		transaction, err := service.CreateTransaction(ctx, financeMemberSubject, financeHouseholdID, "page-transaction-"+suffix, "page-transaction-"+suffix, CreateTransactionInput{Type: "expense", AccountID: account.Value.ID, CategoryID: &category.Value.ID, AmountCents: "1", EventDate: "2026-07-15"})
		if err != nil {
			t.Fatal(err)
		}
		accounts = append(accounts, account.Value)
		categories = append(categories, category.Value)
		transactions = append(transactions, transaction.Value)
	}

	firstAccounts, err := service.ListAccounts(ctx, financeMemberSubject, financeHouseholdID, AccountListInput{Limit: 2})
	if err != nil || firstAccounts.NextCursor == nil {
		t.Fatal("missing account cursor")
	}
	insertedAccount, err := service.CreateAccount(ctx, financeOwnerSubject, financeHouseholdID, "page-account-new", "page-account-new", CreateAccountInput{Name: "Account new", Color: "#ABCDEF"})
	if err != nil {
		t.Fatal(err)
	}
	_, err = service.UpdateAccount(ctx, financeOwnerSubject, financeHouseholdID, firstAccounts.Accounts[0].ID, firstAccounts.Accounts[0].Version, "page-account-patch", AccountPatchInput{Name: Field[string]{Present: true, Value: "Patched"}})
	if err != nil {
		t.Fatal(err)
	}
	_, err = service.SetAccountArchived(ctx, financeOwnerSubject, financeHouseholdID, firstAccounts.Accounts[1].ID, firstAccounts.Accounts[1].Version, "page-account-archive", true)
	if err != nil {
		t.Fatal(err)
	}
	secondAccounts, err := service.ListAccounts(ctx, financeMemberSubject, financeHouseholdID, AccountListInput{Limit: 2, Cursor: *firstAccounts.NextCursor})
	if err != nil {
		t.Fatal(err)
	}
	assertNoPageDuplicates(t, accountIDs(firstAccounts.Accounts), accountIDs(secondAccounts.Accounts))
	if containsID(accountIDs(secondAccounts.Accounts), insertedAccount.Value.ID) {
		t.Fatal("new account entered an existing traversal")
	}

	firstCategories, err := service.ListCategories(ctx, financeMemberSubject, financeHouseholdID, CategoryListInput{Type: "expense", Limit: 2})
	if err != nil || firstCategories.NextCursor == nil {
		t.Fatal("missing category cursor")
	}
	insertedCategory, err := service.CreateCategory(ctx, financeOwnerSubject, financeHouseholdID, "page-category-new", "page-category-new", CreateCategoryInput{Type: "expense", Name: "Category new", Color: "#ABCDEF"})
	if err != nil {
		t.Fatal(err)
	}
	_, err = service.UpdateCategory(ctx, financeOwnerSubject, financeHouseholdID, firstCategories.Categories[0].ID, firstCategories.Categories[0].Version, "page-category-patch", CategoryPatchInput{Name: Field[string]{Present: true, Value: "Patched category"}})
	if err != nil {
		t.Fatal(err)
	}
	_, err = service.SetCategoryArchived(ctx, financeOwnerSubject, financeHouseholdID, firstCategories.Categories[1].ID, firstCategories.Categories[1].Version, "page-category-archive", true)
	if err != nil {
		t.Fatal(err)
	}
	secondCategories, err := service.ListCategories(ctx, financeMemberSubject, financeHouseholdID, CategoryListInput{Type: "expense", Limit: 2, Cursor: *firstCategories.NextCursor})
	if err != nil {
		t.Fatal(err)
	}
	assertNoPageDuplicates(t, categoryIDs(firstCategories.Categories), categoryIDs(secondCategories.Categories))
	if containsID(categoryIDs(secondCategories.Categories), insertedCategory.Value.ID) {
		t.Fatal("new category entered an existing traversal")
	}

	firstTransactions, err := service.ListTransactions(ctx, financeMemberSubject, financeHouseholdID, TransactionListInput{Limit: 2})
	if err != nil || firstTransactions.NextCursor == nil {
		t.Fatal("missing transaction cursor")
	}
	_, err = service.UpdateTransaction(ctx, financeMemberSubject, financeHouseholdID, firstTransactions.Transactions[0].ID, firstTransactions.Transactions[0].Version, "page-transaction-date", TransactionPatchInput{EventDate: Field[string]{Present: true, Value: "2026-07-01"}})
	if err != nil {
		t.Fatal(err)
	}
	newTransaction, err := service.CreateTransaction(ctx, financeMemberSubject, financeHouseholdID, "page-transaction-new", "page-transaction-new", CreateTransactionInput{Type: "expense", AccountID: insertedAccount.Value.ID, CategoryID: &insertedCategory.Value.ID, AmountCents: "1", EventDate: "2026-07-15"})
	if err != nil {
		t.Fatal(err)
	}
	secondTransactions, err := service.ListTransactions(ctx, financeMemberSubject, financeHouseholdID, TransactionListInput{Limit: 2, Cursor: *firstTransactions.NextCursor})
	if err != nil {
		t.Fatal(err)
	}
	assertNoPageDuplicates(t, transactionIDs(firstTransactions.Transactions), transactionIDs(secondTransactions.Transactions))
	if containsID(transactionIDs(secondTransactions.Transactions), newTransaction.Value.ID) {
		t.Fatal("new transaction entered an existing traversal")
	}

	if got := traverseAccounts(t, service, AccountListInput{State: "all", Limit: 2}); len(got) != 8 {
		t.Fatalf("account traversal=%d want 8", len(got))
	}
	if got := traverseCategories(t, service, CategoryListInput{Type: "expense", State: "all", Limit: 2}); len(got) != 8 {
		t.Fatalf("category traversal=%d want 8", len(got))
	}
	if got := traverseTransactions(t, service, TransactionListInput{Limit: 2}); len(got) != 8 {
		t.Fatalf("transaction traversal=%d want 8", len(got))
	}
	if got := traverseBalances(t, service, AccountBalanceListInput{To: "2026-07-31", Limit: 2}); len(got) != 8 {
		t.Fatalf("balance traversal=%d want 8", len(got))
	}
	if got := traverseExpenses(t, service, CategoryExpenseListInput{From: "2026-07-01", To: "2026-07-31", Limit: 2}); len(got) != 8 {
		t.Fatalf("expense traversal=%d want 8", len(got))
	}
}

func traverseAccounts(t *testing.T, service *Service, input AccountListInput) []string {
	t.Helper()
	var ids []string
	for {
		page, err := service.ListAccounts(context.Background(), financeMemberSubject, financeHouseholdID, input)
		if err != nil {
			t.Fatal(err)
		}
		if len(page.Accounts) > input.Limit {
			t.Fatal("account page exceeded limit")
		}
		ids = append(ids, accountIDs(page.Accounts)...)
		if page.NextCursor == nil {
			break
		}
		input.Cursor = *page.NextCursor
	}
	assertUnique(t, ids)
	return ids
}
func traverseCategories(t *testing.T, service *Service, input CategoryListInput) []string {
	t.Helper()
	var ids []string
	for {
		page, err := service.ListCategories(context.Background(), financeMemberSubject, financeHouseholdID, input)
		if err != nil {
			t.Fatal(err)
		}
		if len(page.Categories) > input.Limit {
			t.Fatal("category page exceeded limit")
		}
		ids = append(ids, categoryIDs(page.Categories)...)
		if page.NextCursor == nil {
			break
		}
		input.Cursor = *page.NextCursor
	}
	assertUnique(t, ids)
	return ids
}
func traverseTransactions(t *testing.T, service *Service, input TransactionListInput) []string {
	t.Helper()
	var ids []string
	for {
		page, err := service.ListTransactions(context.Background(), financeMemberSubject, financeHouseholdID, input)
		if err != nil {
			t.Fatal(err)
		}
		if len(page.Transactions) > input.Limit {
			t.Fatal("transaction page exceeded limit")
		}
		ids = append(ids, transactionIDs(page.Transactions)...)
		if page.NextCursor == nil {
			break
		}
		input.Cursor = *page.NextCursor
	}
	assertUnique(t, ids)
	return ids
}
func traverseBalances(t *testing.T, service *Service, input AccountBalanceListInput) []string {
	t.Helper()
	var ids []string
	for {
		page, err := service.ListAccountBalances(context.Background(), financeMemberSubject, financeHouseholdID, input)
		if err != nil {
			t.Fatal(err)
		}
		if len(page.AccountBalances) > input.Limit {
			t.Fatal("balance page exceeded limit")
		}
		for _, v := range page.AccountBalances {
			ids = append(ids, v.AccountID)
		}
		if page.NextCursor == nil {
			break
		}
		input.Cursor = *page.NextCursor
	}
	assertUnique(t, ids)
	return ids
}
func traverseExpenses(t *testing.T, service *Service, input CategoryExpenseListInput) []string {
	t.Helper()
	var ids []string
	for {
		page, err := service.ListCategoryExpenses(context.Background(), financeMemberSubject, financeHouseholdID, input)
		if err != nil {
			t.Fatal(err)
		}
		if len(page.ExpenseByCategory) > input.Limit {
			t.Fatal("expense page exceeded limit")
		}
		for _, v := range page.ExpenseByCategory {
			ids = append(ids, v.CategoryID)
		}
		if page.NextCursor == nil {
			break
		}
		input.Cursor = *page.NextCursor
	}
	assertUnique(t, ids)
	return ids
}
func accountIDs(values []Account) []string {
	ids := make([]string, 0, len(values))
	for _, v := range values {
		ids = append(ids, v.ID)
	}
	return ids
}
func categoryIDs(values []Category) []string {
	ids := make([]string, 0, len(values))
	for _, v := range values {
		ids = append(ids, v.ID)
	}
	return ids
}
func transactionIDs(values []Transaction) []string {
	ids := make([]string, 0, len(values))
	for _, v := range values {
		ids = append(ids, v.ID)
	}
	return ids
}
func containsID(values []string, id string) bool {
	for _, v := range values {
		if v == id {
			return true
		}
	}
	return false
}
func assertNoPageDuplicates(t *testing.T, left, right []string) {
	t.Helper()
	seen := map[string]bool{}
	for _, id := range left {
		seen[id] = true
	}
	for _, id := range right {
		if seen[id] {
			t.Fatalf("duplicate ID across pages: %s", id)
		}
	}
}
func assertUnique(t *testing.T, values []string) {
	t.Helper()
	seen := map[string]bool{}
	for _, id := range values {
		if seen[id] {
			t.Fatalf("duplicate traversal ID: %s", id)
		}
		seen[id] = true
	}
}
