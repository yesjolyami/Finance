import { useCallback, useEffect, useRef, useState, type FormEvent } from "react";

import type { AccountBalance, CategoryExpense, FinanceAPI, FinanceSummary } from "../../lib/financeApi";
import { formatMoney, monthRange } from "./financeModel";
import { safeFinanceError } from "./financeUI";

interface DashboardPanelProps {
  finance: FinanceAPI;
  householdID: string;
  offline: boolean;
  onSessionExpired: () => void;
}

function expenseShare(amount: string, total: string): number {
  const denominator = BigInt(total === "0" ? "1" : total);
  const percent = Number(BigInt(amount) * 100n / denominator);
  return Math.max(0, Math.min(100, percent));
}

export function DashboardPanel({ finance, householdID, offline, onSessionExpired }: DashboardPanelProps) {
  const initialRange = useRef(monthRange());
  const [from, setFrom] = useState(initialRange.current.from);
  const [to, setTo] = useState(initialRange.current.to);
  const [applied, setApplied] = useState(initialRange.current);
  const [summary, setSummary] = useState<FinanceSummary | null>(null);
  const [balances, setBalances] = useState<AccountBalance[]>([]);
  const [expenses, setExpenses] = useState<CategoryExpense[]>([]);
  const [balanceCursor, setBalanceCursor] = useState<string | null>(null);
  const [expenseCursor, setExpenseCursor] = useState<string | null>(null);
  const [state, setState] = useState<"loading" | "ready" | "error">("loading");
  const [message, setMessage] = useState<string | null>(null);
  const [loadingMore, setLoadingMore] = useState<"balances" | "expenses" | null>(null);
  const extraController = useRef<AbortController | null>(null);

  const load = useCallback((signal: AbortSignal) => {
    setState("loading");
    setMessage(null);
    void Promise.all([
      finance.getSummary(householdID, applied.from, applied.to, signal),
      finance.listAccountBalances(householdID, applied.to, { limit: 8 }, signal),
      finance.listCategoryExpenses(householdID, applied.from, applied.to, { limit: 8 }, signal),
    ]).then(([nextSummary, accountPage, categoryPage]) => {
      if (signal.aborted) return;
      setSummary(nextSummary);
      setBalances(accountPage.accountBalances);
      setBalanceCursor(accountPage.nextCursor);
      setExpenses(categoryPage.expenseByCategory);
      setExpenseCursor(categoryPage.nextCursor);
      setState("ready");
    }).catch((error: unknown) => {
      if (signal.aborted) return;
      setMessage(safeFinanceError(error, onSessionExpired));
      setState("error");
    });
  }, [applied, finance, householdID, onSessionExpired]);

  useEffect(() => {
    const controller = new AbortController();
    if (offline) {
      setState("error");
      setMessage("Вы офлайн. Сводка обновится после восстановления соединения.");
    } else {
      load(controller.signal);
    }
    return () => {
      controller.abort();
      extraController.current?.abort();
    };
  }, [load, offline]);

  const applyRange = (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault();
    if (!from || !to || from > to) {
      setMessage("Начальная дата не должна быть позже конечной.");
      return;
    }
    setApplied({ from, to });
  };

  const loadMore = async (kind: "balances" | "expenses") => {
    if (offline || loadingMore) return;
    const cursor = kind === "balances" ? balanceCursor : expenseCursor;
    if (!cursor) return;
    extraController.current?.abort();
    const controller = new AbortController();
    extraController.current = controller;
    setLoadingMore(kind);
    try {
      if (kind === "balances") {
        const page = await finance.listAccountBalances(householdID, applied.to, { limit: 8, cursor }, controller.signal);
        if (!controller.signal.aborted) {
          setBalances((current) => [...current, ...page.accountBalances]);
          setBalanceCursor(page.nextCursor);
        }
      } else {
        const page = await finance.listCategoryExpenses(householdID, applied.from, applied.to, { limit: 8, cursor }, controller.signal);
        if (!controller.signal.aborted) {
          setExpenses((current) => [...current, ...page.expenseByCategory]);
          setExpenseCursor(page.nextCursor);
        }
      }
    } catch (error) {
      if (!controller.signal.aborted) setMessage(safeFinanceError(error, onSessionExpired));
    } finally {
      if (extraController.current === controller) extraController.current = null;
      setLoadingMore(null);
    }
  };

  return (
    <section className="finance-panel" aria-labelledby="dashboard-title">
      <div className="panel-toolbar">
        <div><p className="eyebrow">Денежный обзор</p><h3 id="dashboard-title">Состояние семьи</h3></div>
        <form className="date-range" onSubmit={applyRange} aria-label="Период сводки">
          <label>С <input type="date" value={from} onChange={(event) => setFrom(event.target.value)} disabled={offline} /></label>
          <label>По <input type="date" value={to} onChange={(event) => setTo(event.target.value)} disabled={offline} /></label>
          <button type="submit" className="secondary-button" disabled={offline}>Показать</button>
        </form>
      </div>

      {message && <div className="finance-alert" role="alert"><span>{message}</span>{state === "error" && !offline && <button type="button" onClick={() => setApplied((current) => ({ ...current }))}>Повторить</button>}</div>}
      {state === "loading" && <div className="metric-grid" aria-busy="true" aria-label="Загрузка сводки"><div className="metric-card skeleton-block" /><div className="metric-card skeleton-block" /><div className="metric-card skeleton-block" /></div>}
      {state === "ready" && summary && (
        <>
          <div className="metric-grid">
            <article className="metric-card metric-card--hero"><span>Общий баланс</span><strong>{formatMoney(summary.householdTotalCents)}</strong><small>Все счета, включая архивные с остатком</small></article>
            <article className="metric-card"><span>Доходы за период</span><strong className="money-positive">+{formatMoney(summary.cashFlow.incomeCents)}</strong><small>{summary.from} — {summary.to}</small></article>
            <article className="metric-card"><span>Расходы за период</span><strong className="money-negative">−{formatMoney(summary.cashFlow.expenseCents)}</strong><small>Без переводов и корректировок</small></article>
          </div>

          <div className="summary-grid">
            <article className="data-card">
              <div className="data-card__heading"><div><p className="eyebrow">Распределение</p><h4>Баланс по счетам</h4></div><span>{balances.length}</span></div>
              {balances.length === 0 ? <p className="compact-empty">Создайте счёт и первую операцию.</p> : <ul className="balance-list">{balances.map((item) => <li key={item.accountId}><div><strong>{item.name}</strong>{item.archivedAt && <small>Архивный счёт</small>}</div><span>{formatMoney(item.balanceCents)}</span></li>)}</ul>}
              {balanceCursor && <button className="load-more" type="button" disabled={loadingMore !== null || offline} onClick={() => void loadMore("balances")}>{loadingMore === "balances" ? "Загружаем…" : "Показать ещё"}</button>}
            </article>
            <article className="data-card">
              <div className="data-card__heading"><div><p className="eyebrow">Структура месяца</p><h4>Расходы по категориям</h4></div><span>{expenses.length}</span></div>
              {expenses.length === 0 ? <p className="compact-empty">В выбранном периоде расходов нет.</p> : <ul className="expense-list">{expenses.map((item) => <li key={item.categoryId}><div><strong>{item.name}</strong><progress value={expenseShare(item.amountCents, summary.cashFlow.expenseCents)} max={100} aria-label={`Доля расходов категории ${item.name}`} /></div><span>{formatMoney(item.amountCents)}</span></li>)}</ul>}
              {expenseCursor && <button className="load-more" type="button" disabled={loadingMore !== null || offline} onClick={() => void loadMore("expenses")}>{loadingMore === "expenses" ? "Загружаем…" : "Показать ещё"}</button>}
            </article>
          </div>
        </>
      )}
    </section>
  );
}
