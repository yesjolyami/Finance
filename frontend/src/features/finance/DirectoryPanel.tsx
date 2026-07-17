import { useCallback, useEffect, useRef, useState, type FormEvent } from "react";

import type { Household } from "../../lib/api";
import { IdempotencyKeyManager } from "../../lib/idempotency";
import type { Account, AccountType, Category, CategoryType, DirectoryState, FinanceAPI } from "../../lib/financeApi";
import { canManageDirectories, runeLength } from "./financeModel";
import { matchesDirectoryState, mergeUniqueByID, reconcileByID, safeFinanceError } from "./financeUI";

interface DirectoryPanelProps {
  kind: "accounts" | "categories";
  finance: FinanceAPI;
  household: Household;
  offline: boolean;
  onSessionExpired: () => void;
}

const palette = ["#5F714D", "#934952", "#A97A39", "#566D7E", "#76618C"] as const;

export function DirectoryPanel({ kind, finance, household, offline, onSessionExpired }: DirectoryPanelProps) {
  const [stateFilter, setStateFilter] = useState<DirectoryState>("active");
  const [categoryType, setCategoryType] = useState<CategoryType>("expense");
  const [accounts, setAccounts] = useState<Account[]>([]);
  const [categories, setCategories] = useState<Category[]>([]);
  const [cursor, setCursor] = useState<string | null>(null);
  const [loadState, setLoadState] = useState<"loading" | "ready" | "error">("loading");
  const [message, setMessage] = useState<string | null>(null);
  const [pendingID, setPendingID] = useState<string | null>(null);
  const [loadingMore, setLoadingMore] = useState(false);
  const [editingAccount, setEditingAccount] = useState<Account | null>(null);
  const [editingCategory, setEditingCategory] = useState<Category | null>(null);
  const loadController = useRef<AbortController | null>(null);
  const mutationController = useRef<AbortController | null>(null);
  const canManage = canManageDirectories(household.role);

  const load = useCallback(async (nextCursor?: string) => {
    loadController.current?.abort();
    const controller = new AbortController();
    loadController.current = controller;
    if (!nextCursor) setLoadState("loading");
    else setLoadingMore(true);
    setMessage(null);
    try {
      if (kind === "accounts") {
        const page = await finance.listAccounts(household.id, { state: stateFilter, limit: 50, ...(nextCursor ? { cursor: nextCursor } : {}) }, controller.signal);
        if (controller.signal.aborted) return;
        setAccounts((current) => nextCursor ? mergeUniqueByID(current, page.accounts) : page.accounts);
        setCursor(page.nextCursor);
      } else {
        const page = await finance.listCategories(household.id, { type: categoryType, state: stateFilter, limit: 50, ...(nextCursor ? { cursor: nextCursor } : {}) }, controller.signal);
        if (controller.signal.aborted) return;
        setCategories((current) => nextCursor ? mergeUniqueByID(current, page.categories) : page.categories);
        setCursor(page.nextCursor);
      }
      setLoadState("ready");
    } catch (error) {
      if (controller.signal.aborted) return;
      setMessage(safeFinanceError(error, onSessionExpired));
      setLoadState("error");
    } finally {
      if (loadController.current === controller) loadController.current = null;
      setLoadingMore(false);
    }
  }, [categoryType, finance, household.id, kind, onSessionExpired, stateFilter]);

  useEffect(() => {
    if (offline) {
      setMessage("Вы офлайн. Справочники доступны после восстановления соединения.");
      setLoadState("error");
      return undefined;
    }
    void load();
    return () => loadController.current?.abort();
  }, [load, offline]);

  useEffect(() => () => mutationController.current?.abort(), []);

  const runMutation = async <T extends Account | Category>(id: string, action: (signal: AbortSignal) => Promise<T>) => {
    if (offline || pendingID) return;
    mutationController.current?.abort();
    const controller = new AbortController();
    mutationController.current = controller;
    setPendingID(id);
    setMessage(null);
    try {
      const updated = await action(controller.signal);
      if (controller.signal.aborted) return;
      if (kind === "accounts") setAccounts((current) => reconcileByID(current, updated as Account, matchesDirectoryState(updated, stateFilter)));
      else setCategories((current) => reconcileByID(current, updated as Category, matchesDirectoryState(updated, stateFilter)));
      setEditingAccount(null);
      setEditingCategory(null);
    } catch (error) {
      if (!controller.signal.aborted) setMessage(safeFinanceError(error, onSessionExpired));
    } finally {
      if (mutationController.current === controller) mutationController.current = null;
      setPendingID(null);
    }
  };

  const toggleAccount = (account: Account) => runMutation(account.id, (signal) => finance.setAccountArchived(household.id, account.id, account.version, account.archivedAt === null, signal));
  const toggleCategory = (category: Category) => runMutation(category.id, (signal) => finance.setCategoryArchived(household.id, category.id, category.version, category.archivedAt === null, signal));

  const entries = kind === "accounts" ? accounts : categories;
  return (
    <section className="finance-panel" aria-labelledby="directory-title">
      <div className="panel-toolbar">
        <div><p className="eyebrow">Справочник</p><h3 id="directory-title">{kind === "accounts" ? "Счета" : "Категории"}</h3></div>
        <div className="toolbar-actions"><button className="icon-button" type="button" aria-label="Обновить справочник" onClick={() => { setEditingAccount(null); setEditingCategory(null); void load(); }} disabled={offline || loadState === "loading" || pendingID !== null}>↻</button><div className="segmented-control" aria-label="Состояние справочника">
          {(["active", "archived", "all"] as const).map((value) => <button key={value} type="button" data-active={stateFilter === value} aria-pressed={stateFilter === value} onClick={() => setStateFilter(value)} disabled={pendingID !== null}>{value === "active" ? "Активные" : value === "archived" ? "Архив" : "Все"}</button>)}
        </div></div>
      </div>
      {kind === "categories" && <div className="category-switch" role="group" aria-label="Тип категорий"><button type="button" data-active={categoryType === "expense"} aria-pressed={categoryType === "expense"} onClick={() => setCategoryType("expense")} disabled={pendingID !== null}>Расходы</button><button type="button" data-active={categoryType === "income"} aria-pressed={categoryType === "income"} onClick={() => setCategoryType("income")} disabled={pendingID !== null}>Доходы</button></div>}
      {!canManage && <div className="permission-note" role="note">Просмотр доступен. Создание и изменение справочников выполняют владелец или администратор.</div>}
      {message && <div className="finance-alert" role="alert"><span>{message}</span>{loadState === "error" && !offline && <button type="button" onClick={() => void load()}>Повторить</button>}</div>}
      {canManage && kind === "accounts" && <AccountEditor finance={finance} householdID={household.id} editing={editingAccount} offline={offline} onCancel={() => setEditingAccount(null)} onSaved={(value) => { setAccounts((current) => reconcileByID(current, value, matchesDirectoryState(value, stateFilter))); setEditingAccount(null); }} onMessage={setMessage} onSessionExpired={onSessionExpired} />}
      {canManage && kind === "categories" && <CategoryEditor finance={finance} householdID={household.id} type={categoryType} editing={editingCategory} offline={offline} onCancel={() => setEditingCategory(null)} onSaved={(value) => { setCategories((current) => reconcileByID(current, value, matchesDirectoryState(value, stateFilter))); setEditingCategory(null); }} onMessage={setMessage} onSessionExpired={onSessionExpired} />}

      {loadState === "loading" && <div className="directory-grid" aria-busy="true"><div className="directory-card skeleton-block" /><div className="directory-card skeleton-block" /><div className="directory-card skeleton-block" /></div>}
      {loadState === "ready" && entries.length === 0 && <div className="large-empty"><span aria-hidden="true">{kind === "accounts" ? "◌" : "◇"}</span><h4>Здесь пока пусто</h4><p>{stateFilter === "archived" ? "Архивных записей нет." : `Создайте ${kind === "accounts" ? "первый счёт" : "первую категорию"}.`}</p></div>}
      {loadState === "ready" && entries.length > 0 && (
        <div className="directory-grid">
          {kind === "accounts" ? accounts.map((account) => (
            <article className="directory-card" key={account.id} data-archived={account.archivedAt !== null}>
              <div className="directory-card__title"><input className="finance-color-swatch" type="color" value={account.color} disabled tabIndex={-1} aria-hidden="true" /><div><h4>{account.name}</h4><p>{account.accountType === "savings" ? "Накопительный" : account.accountType === "cash" ? "Наличные" : "Обычный"}{account.bankLabel ? ` · ${account.bankLabel}` : ""}</p></div><span>v{account.version}</span></div>
              <div className="directory-card__meta"><span>{account.currencyCode}</span>{account.isSystem && <span>Системный</span>}{account.archivedAt && <span>В архиве</span>}</div>
              {canManage && <div className="card-actions"><button type="button" onClick={() => setEditingAccount(account)} disabled={offline || account.isSystem || pendingID !== null}>Изменить</button><button type="button" onClick={() => void toggleAccount(account)} disabled={offline || account.isSystem || pendingID !== null}>{pendingID === account.id ? "Сохраняем…" : account.archivedAt ? "Восстановить" : "В архив"}</button></div>}
            </article>
          )) : categories.map((category) => (
            <article className="directory-card" key={category.id} data-archived={category.archivedAt !== null}>
              <div className="directory-card__title"><input className="finance-color-swatch" type="color" value={category.color} disabled tabIndex={-1} aria-hidden="true" /><div><h4>{category.name}</h4><p>{category.type === "income" ? "Доход" : "Расход"}</p></div><span>v{category.version}</span></div>
              <div className="directory-card__meta">{category.isSystem && <span>Системная</span>}{category.archivedAt && <span>В архиве</span>}</div>
              {canManage && <div className="card-actions"><button type="button" onClick={() => setEditingCategory(category)} disabled={offline || category.isSystem || pendingID !== null}>Изменить</button><button type="button" onClick={() => void toggleCategory(category)} disabled={offline || category.isSystem || pendingID !== null}>{pendingID === category.id ? "Сохраняем…" : category.archivedAt ? "Восстановить" : "В архив"}</button></div>}
            </article>
          ))}
        </div>
      )}
      {cursor && <button className="load-more" type="button" onClick={() => void load(cursor)} disabled={loadingMore || offline}>{loadingMore ? "Загружаем…" : "Показать ещё"}</button>}
    </section>
  );
}

interface EditorProps<T> {
  finance: FinanceAPI;
  householdID: string;
  editing: T | null;
  offline: boolean;
  onSaved: (value: T) => void;
  onCancel: () => void;
  onMessage: (message: string | null) => void;
  onSessionExpired: () => void;
}

function AccountEditor({ finance, householdID, editing, offline, onSaved, onCancel, onMessage, onSessionExpired }: EditorProps<Account>) {
  const [name, setName] = useState("");
  const [color, setColor] = useState<string>(palette[0]);
  const [accountType, setAccountType] = useState<AccountType>("regular");
  const [bankLabel, setBankLabel] = useState("");
  const [pending, setPending] = useState(false);
  const keys = useRef(new IdempotencyKeyManager());
  const controllerRef = useRef<AbortController | null>(null);
  useEffect(() => () => controllerRef.current?.abort(), []);
  useEffect(() => {
    setName(editing?.name ?? "");
    setColor(editing?.color ?? palette[0]);
    setAccountType(editing?.accountType ?? "regular");
    setBankLabel(editing?.bankLabel ?? "");
  }, [editing]);
  const submit = async (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault();
    const normalized = name.trim();
    if (runeLength(normalized) < 1 || runeLength(normalized) > 120) return onMessage("Название счёта должно содержать от 1 до 120 символов.");
    const payload = { name: normalized, color, sortOrder: editing?.sortOrder ?? 0, accountType, bankLabel: bankLabel.trim(), legacyOwnerLabel: editing?.legacyOwnerLabel ?? "", ownerUserId: editing?.ownerUserId ?? null };
    controllerRef.current?.abort();
    const controller = new AbortController();
    controllerRef.current = controller;
    setPending(true); onMessage(null);
    try {
      const saved = editing
        ? await finance.updateAccount(householdID, editing.id, { name: payload.name, color, accountType, bankLabel: payload.bankLabel }, editing.version, controller.signal)
        : (await finance.createAccount(householdID, payload, keys.current.forPayload(JSON.stringify(payload)), controller.signal)).resource;
      if (controller.signal.aborted) return;
      keys.current.succeeded(); onSaved(saved);
      if (!editing) { setName(""); setBankLabel(""); }
    } catch (error) { if (!controller.signal.aborted) onMessage(safeFinanceError(error, onSessionExpired)); }
    finally { if (controllerRef.current === controller) controllerRef.current = null; setPending(false); }
  };
  return <form className="directory-editor" onSubmit={(event) => void submit(event)}><div><p className="eyebrow">{editing ? "Редактирование" : "Новый счёт"}</p><h4>{editing ? editing.name : "Добавить счёт"}</h4></div><label>Название<input value={name} onChange={(event) => setName(event.target.value)} required disabled={pending || offline} /></label><label>Тип<select value={accountType} onChange={(event) => setAccountType(event.target.value as AccountType)} disabled={pending || offline}><option value="regular">Обычный</option><option value="savings">Накопительный</option><option value="cash">Наличные</option></select></label><label>Банк<input value={bankLabel} onChange={(event) => setBankLabel(event.target.value)} maxLength={120} disabled={pending || offline} /></label><ColorPicker value={color} onChange={setColor} disabled={pending || offline} /><button className="primary-button" type="submit" disabled={pending || offline}>{pending ? "Сохраняем…" : editing ? "Сохранить" : "Создать"}</button>{editing && <button className="secondary-button" type="button" onClick={onCancel} disabled={pending}>Отмена</button>}</form>;
}

function CategoryEditor({ finance, householdID, type, editing, offline, onSaved, onCancel, onMessage, onSessionExpired }: EditorProps<Category> & { type: CategoryType }) {
  const [name, setName] = useState("");
  const [color, setColor] = useState<string>(palette[1]);
  const [pending, setPending] = useState(false);
  const keys = useRef(new IdempotencyKeyManager());
  const controllerRef = useRef<AbortController | null>(null);
  useEffect(() => () => controllerRef.current?.abort(), []);
  useEffect(() => { setName(editing?.name ?? ""); setColor(editing?.color ?? palette[1]); }, [editing]);
  const submit = async (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault();
    const normalized = name.trim();
    if (!normalized || runeLength(normalized) > 120) return onMessage("Название категории должно содержать от 1 до 120 символов.");
    const payload = { type, name: normalized, color, sortOrder: editing?.sortOrder ?? 0 };
    controllerRef.current?.abort();
    const controller = new AbortController();
    controllerRef.current = controller;
    setPending(true); onMessage(null);
    try {
      const saved = editing
        ? await finance.updateCategory(householdID, editing.id, { name: payload.name, color }, editing.version, controller.signal)
        : (await finance.createCategory(householdID, payload, keys.current.forPayload(JSON.stringify(payload)), controller.signal)).resource;
      if (controller.signal.aborted) return;
      keys.current.succeeded(); onSaved(saved); if (!editing) setName("");
    } catch (error) { if (!controller.signal.aborted) onMessage(safeFinanceError(error, onSessionExpired)); }
    finally { if (controllerRef.current === controller) controllerRef.current = null; setPending(false); }
  };
  return <form className="directory-editor" onSubmit={(event) => void submit(event)}><div><p className="eyebrow">{editing ? "Редактирование" : type === "income" ? "Доход" : "Расход"}</p><h4>{editing ? editing.name : "Добавить категорию"}</h4></div><label>Название<input value={name} onChange={(event) => setName(event.target.value)} required disabled={pending || offline} /></label><ColorPicker value={color} onChange={setColor} disabled={pending || offline} /><button className="primary-button" type="submit" disabled={pending || offline}>{pending ? "Сохраняем…" : editing ? "Сохранить" : "Создать"}</button>{editing && <button className="secondary-button" type="button" onClick={onCancel} disabled={pending}>Отмена</button>}</form>;
}

function ColorPicker({ value, onChange, disabled }: { value: string; onChange: (value: string) => void; disabled: boolean }) {
  return <fieldset className="color-picker"><legend>Цвет</legend>{palette.map((color) => <button key={color} type="button" className="finance-color" data-color={color} data-active={value === color} aria-pressed={value === color} onClick={() => onChange(color)} disabled={disabled} aria-label={`Цвет ${color}`} />)}</fieldset>;
}
