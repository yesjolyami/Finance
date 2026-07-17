package backupv5

import (
	"context"
	"math/big"

	"github.com/google/uuid"
)

type bigMonthTotals struct {
	incomeCount, expenseCount, transferCount int
	income, expense, transfer                big.Int
	cashFlowIncome, cashFlowExpense          big.Int
}

func calculateReconciliation(ctx context.Context, model *Model) error {
	balances := make(map[uuid.UUID]*big.Int, len(model.Accounts))
	for _, account := range model.Accounts {
		balances[account.ID] = new(big.Int)
	}
	months := make(map[string]*bigMonthTotals)
	income := new(big.Int)
	expense := new(big.Int)
	transfer := new(big.Int)
	cashFlowIncome := new(big.Int)
	cashFlowExpense := new(big.Int)

	for index, transaction := range model.Transactions {
		if err := ctx.Err(); err != nil {
			return err
		}
		amount := big.NewInt(transaction.AmountCents)
		monthKey := transaction.EventDate.Format("2006-01")
		month := months[monthKey]
		if month == nil {
			month = &bigMonthTotals{}
			months[monthKey] = month
		}
		switch transaction.Type {
		case "income":
			balances[transaction.AccountID].Add(balances[transaction.AccountID], amount)
			income.Add(income, amount)
			month.income.Add(&month.income, amount)
			month.incomeCount++
			if !transaction.IsBalanceAdjustment {
				cashFlowIncome.Add(cashFlowIncome, amount)
				month.cashFlowIncome.Add(&month.cashFlowIncome, amount)
			}
		case "expense":
			balances[transaction.AccountID].Sub(balances[transaction.AccountID], amount)
			expense.Add(expense, amount)
			month.expense.Add(&month.expense, amount)
			month.expenseCount++
			if !transaction.IsBalanceAdjustment {
				cashFlowExpense.Add(cashFlowExpense, amount)
				month.cashFlowExpense.Add(&month.cashFlowExpense, amount)
			}
		case "transfer":
			balances[transaction.AccountID].Sub(balances[transaction.AccountID], amount)
			balances[*transaction.ToAccountID].Add(balances[*transaction.ToAccountID], amount)
			transfer.Add(transfer, amount)
			month.transfer.Add(&month.transfer, amount)
			month.transferCount++
		}
		if !balances[transaction.AccountID].IsInt64() ||
			(transaction.ToAccountID != nil && !balances[*transaction.ToAccountID].IsInt64()) ||
			!income.IsInt64() || !expense.IsInt64() || !transfer.IsInt64() ||
			!month.income.IsInt64() || !month.expense.IsInt64() || !month.transfer.IsInt64() ||
			!cashFlowIncome.IsInt64() || !cashFlowExpense.IsInt64() ||
			!month.cashFlowIncome.IsInt64() || !month.cashFlowExpense.IsInt64() {
			return validationError(ErrReconciliation, "aggregate_overflow", itemPath("transactions", index))
		}
	}

	householdBalance := new(big.Int)
	accountBalances := make(map[uuid.UUID]int64, len(balances))
	for accountID, balance := range balances {
		if err := ctx.Err(); err != nil {
			return err
		}
		householdBalance.Add(householdBalance, balance)
		accountBalances[accountID] = balance.Int64()
	}
	if !householdBalance.IsInt64() {
		return validationError(ErrReconciliation, "aggregate_overflow", "transactions")
	}

	budgetTotal := new(big.Int)
	for index, budget := range model.Budgets {
		budgetTotal.Add(budgetTotal, big.NewInt(budget.AmountCents))
		if !budgetTotal.IsInt64() {
			return validationError(ErrReconciliation, "aggregate_overflow", itemPath("budgets", index))
		}
	}

	monthly := make(map[string]MonthTotals, len(months))
	for key, month := range months {
		monthly[key] = MonthTotals{
			IncomeCount: month.incomeCount, IncomeCents: month.income.Int64(),
			ExpenseCount: month.expenseCount, ExpenseCents: month.expense.Int64(),
			TransferCount: month.transferCount, TransferCents: month.transfer.Int64(),
			CashFlowIncomeCents:  month.cashFlowIncome.Int64(),
			CashFlowExpenseCents: month.cashFlowExpense.Int64(),
		}
	}

	paidByDebt := make(map[uuid.UUID]*big.Int, len(model.Debts))
	for _, debt := range model.Debts {
		paidByDebt[debt.ID] = new(big.Int)
	}
	for index, payment := range model.DebtPayments {
		paid := paidByDebt[payment.DebtID]
		paid.Add(paid, big.NewInt(payment.AmountCents))
		if !paid.IsInt64() {
			return validationError(ErrReconciliation, "aggregate_overflow", itemPath("debtPayments", index))
		}
	}
	debtReconciliation := make([]DebtReconciliation, 0, len(model.Debts))
	for index, debt := range model.Debts {
		if err := ctx.Err(); err != nil {
			return err
		}
		paid := paidByDebt[debt.ID].Int64()
		left := debt.OriginalAmountCents - paid
		if left < 0 {
			left = 0
			model.Warnings.DebtOverpaid++
		}
		if paid != debt.LegacyPaidCents || left != debt.LegacyLeftCents {
			return validationError(ErrReconciliation, "debt_derived_values_mismatch", itemPath("debts", index))
		}
		debtReconciliation = append(debtReconciliation, DebtReconciliation{
			DebtID: debt.ID, PaidCents: paid, LeftCents: left,
		})
	}

	model.Totals = Totals{
		IncomeCents: income.Int64(), ExpenseCents: expense.Int64(), TransferCents: transfer.Int64(),
		HouseholdBalanceCents: householdBalance.Int64(),
		CashFlowIncomeCents:   cashFlowIncome.Int64(), CashFlowExpenseCents: cashFlowExpense.Int64(),
		BudgetCents: budgetTotal.Int64(),
	}
	model.Reconciliation = Reconciliation{
		AccountBalances: accountBalances,
		Monthly:         monthly,
		Debts:           debtReconciliation,
	}
	return nil
}
