import { useCallback, useEffect, useMemo, useRef, useState, type FormEvent } from "react";

import type { Household } from "../../lib/api";
import { IdempotencyKeyManager } from "../../lib/idempotency";
import type {
  Account,
  Category,
  FinanceAPI,
  FinanceTransaction,
  TransactionListOptions,
  TransactionState,
  TransactionType,
} from "../../lib/financeApi";
import { centsToRubles, formatMoney, monthRange, runeLength, todayLocalDate, transactionPayloadKey, validateTransactionDraft, type TransactionDraft } from "./financeModel";
import { matchesTransactionFilter, mergeUniqueByID, reconcileByID, safeFinanceError } from "./financeUI";

interface TransactionsPanelProps {
  finance: FinanceAPI;
  household: Household;
  offline: boolean;
  onSessionExpired: () => void;
}

interface FilterDraft {
  from: string;
  to: string;
  type: "" | TransactionType;
  accountId: string;
  categoryId: string;
  state: TransactionState;
}

function initialTransactionDraft(): TransactionDraft {
  return { type: "expense", accountId: "", toAccountId: "", categoryId: "", amountRubles: "", eventDate: todayLocalDate(), note: "", isBalanceAdjustment: false };
}

function initialFilters(): FilterDraft {
  const range = monthRange();
  return { ...range, type: "", accountId: "", categoryId: "", state: "active" };
}

function toListOptions(filters: FilterDraft, cursor?: string): TransactionListOptions {
  return {
    ...(filters.from ? { from: filters.from } : {}),
    ...(filters.to ? { to: filters.to } : {}),
    ...(filters.type ? { type: filters.type } : {}),
    ...(filters.accountId ? { accountId: filters.accountId } : {}),
    ...(filters.categoryId ? { categoryId: filters.categoryId } : {}),
    state: filters.state,
    limit: 40,
    ...(cursor ? { cursor } : {}),
  };
}

export function TransactionsPanel({ finance, household, offline, onSessionExpired }: TransactionsPanelProps) {
  const [filterDraft, setFilterDraft] = useState<FilterDraft>(initialFilters);
  const [filters, setFilters] = useState<FilterDraft>(initialFilters);
  const [transactions, setTransactions] = useState<FinanceTransaction[]>([]);
  const [accounts, setAccounts] = useState<Account[]>([]);
  const [categories, setCategories] = useState<Category[]>([]);
  const [accountCursor, setAccountCursor] = useState<string | null>(null);
  const [incomeCategoryCursor, setIncomeCategoryCursor] = useState<string | null>(null);
  const [expenseCategoryCursor, setExpenseCategoryCursor] = useState<string | null>(null);
  const [cursor, setCursor] = useState<string | null>(null);
  const [state, setState] = useState<"loading" | "ready" | "error">("loading");
  const [message, setMessage] = useState<string | null>(null);
  const [loadingMore, setLoadingMore] = useState(false);
  const [loadingReference, setLoadingReference] = useState<"accounts" | "categories" | null>(null);
  const [draft, setDraft] = useState<TransactionDraft>(initialTransactionDraft);
  const [editing, setEditing] = useState<FinanceTransaction | null>(null);
  const [pending, setPending] = useState(false);
  const [deleting, setDeleting] = useState<FinanceTransaction | null>(null);
  const [deleteReason, setDeleteReason] = useState("");
  const createKeys = useRef(new IdempotencyKeyManager());
  const loadController = useRef<AbortController | null>(null);
  const mutationController = useRef<AbortController | null>(null);
  const referenceController = useRef<AbortController | null>(null);

  const load = useCallback(async (nextCursor?: string) => {
    loadController.current?.abort();
    const controller = new AbortController();
    loadController.current = controller;
    if (nextCursor) setLoadingMore(true); else setState("loading");
    setMessage(null);
    try {
      const requests: [Promise<Awaited<ReturnType<FinanceAPI["listTransactions"]>>>, Promise<Awaited<ReturnType<FinanceAPI["listAccounts"]>>>?, Promise<Awaited<ReturnType<FinanceAPI["listCategories"]>>>?, Promise<Awaited<ReturnType<FinanceAPI["listCategories"]>>>?] = [
        finance.listTransactions(household.id, toListOptions(filters, nextCursor), controller.signal),
      ];
      if (!nextCursor) {
        requests.push(
          finance.listAccounts(household.id, { state: "active", limit: 200 }, controller.signal),
          finance.listCategories(household.id, { type: "income", state: "active", limit: 200 }, controller.signal),
          finance.listCategories(household.id, { type: "expense", state: "active", limit: 200 }, controller.signal),
        );
      }
      const [page, accountPage, incomePage, expensePage] = await Promise.all(requests);
      if (controller.signal.aborted) return;
      setTransactions((current) => nextCursor ? mergeUniqueByID(current, page.transactions) : page.transactions);
      setCursor(page.nextCursor);
      if (accountPage) {
        setAccounts(accountPage.accounts);
        setAccountCursor(accountPage.nextCursor);
      }
      if (incomePage && expensePage) {
        setCategories([...incomePage.categories, ...expensePage.categories]);
        setIncomeCategoryCursor(incomePage.nextCursor);
        setExpenseCategoryCursor(expensePage.nextCursor);
      }
      setState("ready");
    } catch (error) {
      if (controller.signal.aborted) return;
      setMessage(safeFinanceError(error, onSessionExpired));
      setState("error");
    } finally {
      if (loadController.current === controller) loadController.current = null;
      setLoadingMore(false);
    }
  }, [filters, finance, household.id, onSessionExpired]);

  useEffect(() => {
    if (offline) {
      setMessage("Вы офлайн. Операции временно недоступны.");
      setState("error");
      return undefined;
    }
    void load();
    return () => loadController.current?.abort();
  }, [load, offline]);
  useEffect(() => () => {
    mutationController.current?.abort();
    referenceController.current?.abort();
  }, []);

  const accountNames = useMemo(() => new Map(accounts.map((account) => [account.id, account.name])), [accounts]);
  const categoryNames = useMemo(() => new Map(categories.map((category) => [category.id, category.name])), [categories]);
  const visibleCategories = categories.filter((category) => category.type === (draft.type === "income" ? "income" : "expense"));

  const loadMoreReferences = async (kind: "accounts" | "categories") => {
    if (offline || loadingReference) return;
    const categoryType = draft.type === "income" ? "income" : "expense";
    const nextCursor = kind === "accounts" ? accountCursor : categoryType === "income" ? incomeCategoryCursor : expenseCategoryCursor;
    if (!nextCursor) return;
    referenceController.current?.abort();
    const controller = new AbortController();
    referenceController.current = controller;
    setLoadingReference(kind);
    try {
      if (kind === "accounts") {
        const page = await finance.listAccounts(household.id, { state: "active", limit: 200, cursor: nextCursor }, controller.signal);
        if (!controller.signal.aborted) {
          setAccounts((current) => mergeUniqueByID(current, page.accounts));
          setAccountCursor(page.nextCursor);
        }
      } else {
        const page = await finance.listCategories(household.id, { type: categoryType, state: "active", limit: 200, cursor: nextCursor }, controller.signal);
        if (!controller.signal.aborted) {
          setCategories((current) => mergeUniqueByID(current, page.categories));
          if (categoryType === "income") setIncomeCategoryCursor(page.nextCursor);
          else setExpenseCategoryCursor(page.nextCursor);
        }
      }
    } catch (error) {
      if (!controller.signal.aborted) setMessage(safeFinanceError(error, onSessionExpired));
    } finally {
      if (referenceController.current === controller) referenceController.current = null;
      setLoadingReference(null);
    }
  };

  const applyFilters = (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault();
    if (filterDraft.from && filterDraft.to && filterDraft.from > filterDraft.to) {
      setMessage("Начальная дата фильтра не должна быть позже конечной.");
      return;
    }
    setFilters({ ...filterDraft });
  };

  const beginEdit = (transaction: FinanceTransaction) => {
    setEditing(transaction);
    setDraft({
      type: transaction.type,
      accountId: transaction.accountId,
      toAccountId: transaction.toAccountId ?? "",
      categoryId: transaction.categoryId ?? "",
      amountRubles: centsToRubles(transaction.amountCents),
      eventDate: transaction.eventDate,
      note: transaction.note,
      isBalanceAdjustment: transaction.isBalanceAdjustment,
    });
    setMessage(null);
  };

  const resetEditor = () => { setEditing(null); setDraft(initialTransactionDraft()); };

  const submitTransaction = async (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault();
    if (pending || offline) return;
    const validated = validateTransactionDraft(draft);
    if (!validated.input) { setMessage(validated.error); return; }
    mutationController.current?.abort();
    const controller = new AbortController();
    mutationController.current = controller;
    setPending(true); setMessage(null);
    try {
      if (editing) {
        const updated = await finance.updateTransaction(household.id, editing.id, validated.input, editing.version, controller.signal);
        if (!controller.signal.aborted) setTransactions((current) => reconcileByID(current, updated, matchesTransactionFilter(updated, filters)));
      } else {
        const payload = validated.input;
        const result = await finance.createTransaction(household.id, payload, createKeys.current.forPayload(transactionPayloadKey(payload)), controller.signal);
        if (!controller.signal.aborted) {
          createKeys.current.succeeded();
          setTransactions((current) => reconcileByID(current, result.resource, matchesTransactionFilter(result.resource, filters)));
          if (result.replayed) setMessage("Повтор запроса распознан: показана уже созданная операция.");
        }
      }
      if (!controller.signal.aborted) resetEditor();
    } catch (error) {
      if (!controller.signal.aborted) setMessage(safeFinanceError(error, onSessionExpired));
    } finally {
      if (mutationController.current === controller) mutationController.current = null;
      setPending(false);
    }
  };

  const deleteTransaction = async (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault();
    if (!deleting || pending || offline) return;
    const reason = deleteReason.trim();
    if (!reason || runeLength(reason) > 500) { setMessage("Укажите причину удаления до 500 символов."); return; }
    mutationController.current?.abort();
    const controller = new AbortController();
    mutationController.current = controller;
    setPending(true); setMessage(null);
    try {
      const updated = await finance.deleteTransaction(household.id, deleting.id, reason, deleting.version, controller.signal);
      if (!controller.signal.aborted) {
        setTransactions((current) => reconcileByID(current, updated, matchesTransactionFilter(updated, filters)));
        setDeleting(null); setDeleteReason("");
      }
    } catch (error) { if (!controller.signal.aborted) setMessage(safeFinanceError(error, onSessionExpired)); }
    finally { if (mutationController.current === controller) mutationController.current = null; setPending(false); }
  };

  const restore = async (transaction: FinanceTransaction) => {
    if (pending || offline) return;
    mutationController.current?.abort();
    const controller = new AbortController();
    mutationController.current = controller;
    setPending(true); setMessage(null);
    try {
      const updated = await finance.restoreTransaction(household.id, transaction.id, transaction.version, controller.signal);
      if (!controller.signal.aborted) setTransactions((current) => reconcileByID(current, updated, matchesTransactionFilter(updated, filters)));
    } catch (error) { if (!controller.signal.aborted) setMessage(safeFinanceError(error, onSessionExpired)); }
    finally { if (mutationController.current === controller) mutationController.current = null; setPending(false); }
  };

  return (
    <section className="finance-panel" aria-labelledby="transactions-title">
      <div className="panel-toolbar"><div><p className="eyebrow">Журнал</p><h3 id="transactions-title">Операции</h3></div><div className="toolbar-actions"><span className="count-badge">{transactions.length}</span><button className="icon-button" type="button" aria-label="Обновить операции" onClick={() => { resetEditor(); setDeleting(null); void load(); }} disabled={offline || state === "loading" || pending}>↻</button></div></div>
      <form className="transaction-filters" onSubmit={applyFilters} aria-label="Фильтры операций">
        <label>С<input type="date" value={filterDraft.from} onChange={(event) => setFilterDraft((current) => ({ ...current, from: event.target.value }))} /></label>
        <label>По<input type="date" value={filterDraft.to} onChange={(event) => setFilterDraft((current) => ({ ...current, to: event.target.value }))} /></label>
        <label>Тип<select value={filterDraft.type} onChange={(event) => setFilterDraft((current) => ({ ...current, type: event.target.value as FilterDraft["type"] }))}><option value="">Все</option><option value="income">Доход</option><option value="expense">Расход</option><option value="transfer">Перевод</option></select></label>
        <label>Состояние<select value={filterDraft.state} onChange={(event) => setFilterDraft((current) => ({ ...current, state: event.target.value as TransactionState }))}><option value="active">Активные</option><option value="deleted">Удалённые</option><option value="all">Все</option></select></label>
        <label>Счёт<select value={filterDraft.accountId} onChange={(event) => setFilterDraft((current) => ({ ...current, accountId: event.target.value }))}><option value="">Все счета</option>{accounts.map((account) => <option value={account.id} key={account.id}>{account.name}</option>)}</select></label>
        <label>Категория<select value={filterDraft.categoryId} onChange={(event) => setFilterDraft((current) => ({ ...current, categoryId: event.target.value }))}><option value="">Все категории</option>{categories.map((category) => <option value={category.id} key={category.id}>{category.name}</option>)}</select></label>
        <button className="secondary-button" type="submit" disabled={offline}>Применить</button>
      </form>

      {message && <div className="finance-alert" role="alert"><span>{message}</span>{state === "error" && !offline && <button type="button" onClick={() => void load()}>Повторить</button>}</div>}

      <form className="transaction-editor" onSubmit={(event) => void submitTransaction(event)}>
        <div className="editor-heading"><div><p className="eyebrow">{editing ? "Редактирование" : "Новая запись"}</p><h4>{editing ? "Изменить операцию" : "Добавить операцию"}</h4></div>{editing && <button type="button" className="text-button" onClick={resetEditor} disabled={pending}>Отменить</button>}</div>
        <label>Тип<select value={draft.type} onChange={(event) => setDraft((current) => ({ ...current, type: event.target.value as TransactionType, categoryId: "", toAccountId: "", isBalanceAdjustment: false }))} disabled={pending || offline}><option value="expense">Расход</option><option value="income">Доход</option><option value="transfer">Перевод</option></select></label>
        <label>{draft.type === "transfer" ? "Со счёта" : "Счёт"}<select value={draft.accountId} onChange={(event) => setDraft((current) => ({ ...current, accountId: event.target.value }))} required disabled={pending || offline}><option value="">Выберите</option>{editing && !accounts.some((item) => item.id === editing.accountId) && <option value={editing.accountId}>Исторический счёт</option>}{accounts.map((account) => <option key={account.id} value={account.id}>{account.name}</option>)}</select></label>
        {draft.type === "transfer" ? <label>На счёт<select value={draft.toAccountId} onChange={(event) => setDraft((current) => ({ ...current, toAccountId: event.target.value }))} required disabled={pending || offline}><option value="">Выберите</option>{editing?.toAccountId && !accounts.some((item) => item.id === editing.toAccountId) && <option value={editing.toAccountId}>Исторический счёт</option>}{accounts.filter((account) => account.id !== draft.accountId).map((account) => <option key={account.id} value={account.id}>{account.name}</option>)}</select></label> : <label>Категория<select value={draft.categoryId} onChange={(event) => setDraft((current) => ({ ...current, categoryId: event.target.value }))} required disabled={pending || offline}><option value="">Выберите</option>{editing?.categoryId && !categories.some((item) => item.id === editing.categoryId) && <option value={editing.categoryId}>Историческая категория</option>}{visibleCategories.map((category) => <option key={category.id} value={category.id}>{category.name}</option>)}</select></label>}
        <label>Сумма, ₽<input inputMode="decimal" placeholder="0,00" value={draft.amountRubles} onChange={(event) => setDraft((current) => ({ ...current, amountRubles: event.target.value }))} required disabled={pending || offline} /></label>
        <label>Дата<input type="date" value={draft.eventDate} onChange={(event) => setDraft((current) => ({ ...current, eventDate: event.target.value }))} required disabled={pending || offline} /></label>
        <label className="wide-field">Комментарий<input value={draft.note} onChange={(event) => setDraft((current) => ({ ...current, note: event.target.value }))} disabled={pending || offline} /></label>
        {draft.type !== "transfer" && <label className="check-field"><input type="checkbox" checked={draft.isBalanceAdjustment} onChange={(event) => setDraft((current) => ({ ...current, isBalanceAdjustment: event.target.checked }))} disabled={pending || offline} />Корректировка баланса</label>}
        <button className="primary-button" type="submit" disabled={pending || offline}>{pending ? "Сохраняем…" : editing ? "Сохранить" : "Добавить"}</button>
        {(accountCursor || (draft.type === "income" ? incomeCategoryCursor : expenseCategoryCursor)) && <div className="reference-pagination"><span>Не нашли нужное?</span>{accountCursor && <button type="button" className="text-button" onClick={() => void loadMoreReferences("accounts")} disabled={loadingReference !== null || offline}>{loadingReference === "accounts" ? "Загружаем…" : "Ещё счета"}</button>}{draft.type !== "transfer" && (draft.type === "income" ? incomeCategoryCursor : expenseCategoryCursor) && <button type="button" className="text-button" onClick={() => void loadMoreReferences("categories")} disabled={loadingReference !== null || offline}>{loadingReference === "categories" ? "Загружаем…" : "Ещё категории"}</button>}</div>}
      </form>

      {deleting && <form className="delete-confirm" onSubmit={(event) => void deleteTransaction(event)}><div><strong>Удалить операцию?</strong><p>Она останется в истории и сможет быть восстановлена.</p></div><label>Причина<input autoFocus value={deleteReason} onChange={(event) => setDeleteReason(event.target.value)} required /></label><button type="submit" className="danger-button" disabled={pending}>Удалить</button><button type="button" className="secondary-button" onClick={() => { setDeleting(null); setDeleteReason(""); }} disabled={pending}>Отмена</button></form>}

      {state === "loading" && <div className="transaction-list" aria-busy="true"><div className="transaction-row skeleton-block" /><div className="transaction-row skeleton-block" /></div>}
      {state === "ready" && transactions.length === 0 && <div className="large-empty"><span aria-hidden="true">↕</span><h4>Операций не найдено</h4><p>Измените фильтры или добавьте первую запись.</p></div>}
      {state === "ready" && transactions.length > 0 && <div className="transaction-table-wrap"><table className="transaction-table"><thead><tr><th>Операция</th><th>Счёт</th><th>Дата</th><th>Сумма</th><th><span className="sr-only">Действия</span></th></tr></thead><tbody>{transactions.map((transaction) => <tr key={transaction.id} data-deleted={transaction.deletedAt !== null}><td><span className={`transaction-kind transaction-kind--${transaction.type}`}>{transaction.type === "income" ? "Доход" : transaction.type === "expense" ? "Расход" : "Перевод"}</span><strong>{transaction.categoryId ? categoryNames.get(transaction.categoryId) ?? "Историческая категория" : transaction.note || "Между счетами"}</strong>{transaction.isBalanceAdjustment && <small>Корректировка</small>}</td><td>{accountNames.get(transaction.accountId) ?? "Исторический счёт"}{transaction.toAccountId && <small>→ {accountNames.get(transaction.toAccountId) ?? "Исторический счёт"}</small>}</td><td>{transaction.eventDate}<small>v{transaction.version}</small></td><td className={`amount amount--${transaction.type}`}>{transaction.type === "expense" ? "−" : transaction.type === "income" ? "+" : ""}{formatMoney(transaction.amountCents)}</td><td><div className="row-actions">{transaction.deletedAt ? <button type="button" onClick={() => void restore(transaction)} disabled={pending || offline}>Восстановить</button> : <><button type="button" onClick={() => beginEdit(transaction)} disabled={pending || offline}>Изменить</button><button type="button" onClick={() => setDeleting(transaction)} disabled={pending || offline}>Удалить</button></>}</div></td></tr>)}</tbody></table></div>}
      {cursor && <button type="button" className="load-more" onClick={() => void load(cursor)} disabled={loadingMore || offline}>{loadingMore ? "Загружаем…" : "Показать ещё операции"}</button>}
    </section>
  );
}
