import { APIClient, APIError, type APIResponse } from "./api";

export type DirectoryState = "active" | "archived" | "all";
export type TransactionState = "active" | "deleted" | "all";
export type TransactionType = "income" | "expense" | "transfer";
export type CategoryType = "income" | "expense";
export type AccountType = "regular" | "savings" | "cash";

export interface Account {
  id: string;
  name: string;
  color: string;
  sortOrder: number;
  accountType: AccountType;
  bankLabel: string;
  legacyOwnerLabel: string;
  ownerUserId: string | null;
  currencyCode: "RUB";
  isSystem: boolean;
  version: string;
  createdAt: string;
  updatedAt: string;
  archivedAt: string | null;
}

export interface Category {
  id: string;
  type: CategoryType;
  name: string;
  color: string;
  sortOrder: number;
  isSystem: boolean;
  version: string;
  createdAt: string;
  updatedAt: string;
  archivedAt: string | null;
}

export interface FinanceTransaction {
  id: string;
  type: TransactionType;
  accountId: string;
  toAccountId: string | null;
  categoryId: string | null;
  amountCents: string;
  eventDate: string;
  note: string;
  isBalanceAdjustment: boolean;
  source: string;
  createdByUserId: string | null;
  createdAt: string;
  updatedAt: string;
  deletedAt: string | null;
  deletionReason: string | null;
  version: string;
}

export interface AccountPage {
  accounts: Account[];
  nextCursor: string | null;
}

export interface CategoryPage {
  categories: Category[];
  nextCursor: string | null;
}

export interface TransactionPage {
  transactions: FinanceTransaction[];
  nextCursor: string | null;
}

export interface FinanceSummary {
  from: string;
  to: string;
  householdTotalCents: string;
  cashFlow: { incomeCents: string; expenseCents: string };
}

export interface AccountBalance {
  accountId: string;
  name: string;
  archivedAt: string | null;
  balanceCents: string;
}

export interface AccountBalancePage {
  accountBalances: AccountBalance[];
  nextCursor: string | null;
}

export interface CategoryExpense {
  categoryId: string;
  name: string;
  amountCents: string;
}

export interface CategoryExpensePage {
  expenseByCategory: CategoryExpense[];
  nextCursor: string | null;
}

export interface AccountCreateInput {
  name: string;
  color: string;
  sortOrder: number;
  accountType: AccountType;
  bankLabel: string;
  legacyOwnerLabel: string;
  ownerUserId: string | null;
}

export type AccountPatchInput = Partial<AccountCreateInput>;

export interface CategoryCreateInput {
  type: CategoryType;
  name: string;
  color: string;
  sortOrder: number;
}

export type CategoryPatchInput = Partial<Omit<CategoryCreateInput, "type">>;

export interface TransactionCreateInput {
  type: TransactionType;
  accountId: string;
  toAccountId: string | null;
  categoryId: string | null;
  amountCents: string;
  eventDate: string;
  note: string;
  isBalanceAdjustment: boolean;
}

export type TransactionPatchInput = Partial<TransactionCreateInput>;

export interface AccountListOptions {
  state?: DirectoryState;
  limit?: number;
  cursor?: string;
}

export interface CategoryListOptions extends AccountListOptions {
  type: CategoryType;
}

export interface TransactionListOptions {
  from?: string;
  to?: string;
  type?: TransactionType;
  accountId?: string;
  categoryId?: string;
  state?: TransactionState;
  limit?: number;
  cursor?: string;
}

export interface PageOptions {
  limit?: number;
  cursor?: string;
}

export interface MutationResult<T> {
  resource: T;
  replayed: boolean;
}

const uuidPattern = /^[0-9a-f]{8}-[0-9a-f]{4}-[1-5][0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$/i;
const colorPattern = /^#[0-9A-F]{6}$/;
const integerPattern = /^-?(0|[1-9][0-9]*)$/;
const positiveIntegerPattern = /^[1-9][0-9]*$/;
const maxInt64 = 9_223_372_036_854_775_807n;
const minInt64 = -9_223_372_036_854_775_808n;

function isRecord(value: unknown): value is Record<string, unknown> {
  return typeof value === "object" && value !== null && !Array.isArray(value);
}

function isUUID(value: unknown): value is string {
  return typeof value === "string" && uuidPattern.test(value);
}

function isTimestamp(value: unknown): value is string {
  return typeof value === "string" && value.includes("T") && Number.isFinite(Date.parse(value));
}

function isNullableTimestamp(value: unknown): value is string | null {
  return value === null || isTimestamp(value);
}

function isLocalDate(value: unknown): value is string {
  if (typeof value !== "string" || !/^\d{4}-\d{2}-\d{2}$/.test(value)) return false;
  const [yearText, monthText, dayText] = value.split("-");
  const year = Number(yearText);
  const month = Number(monthText);
  const day = Number(dayText);
  const date = new Date(Date.UTC(year, month - 1, day));
  return date.getUTCFullYear() === year && date.getUTCMonth() === month - 1 && date.getUTCDate() === day;
}

function isInt64String(value: unknown, positive = false): value is string {
  if (typeof value !== "string" || !(positive ? positiveIntegerPattern : integerPattern).test(value)) return false;
  try {
    const parsed = BigInt(value);
    return parsed >= (positive ? 1n : minInt64) && parsed <= maxInt64;
  } catch {
    return false;
  }
}

function isVersion(value: unknown): value is string {
  return isInt64String(value, true);
}

function isCursor(value: unknown): value is string | null {
  return value === null || (typeof value === "string" && value.length > 0 && value.length <= 512);
}

function parseAccount(value: unknown): Account | null {
  if (
    !isRecord(value) || !isUUID(value.id) || typeof value.name !== "string" ||
    typeof value.color !== "string" || !colorPattern.test(value.color) ||
    !Number.isSafeInteger(value.sortOrder) || (value.accountType !== "regular" && value.accountType !== "savings" && value.accountType !== "cash") ||
    typeof value.bankLabel !== "string" || typeof value.legacyOwnerLabel !== "string" ||
    !(value.ownerUserId === null || isUUID(value.ownerUserId)) || value.currencyCode !== "RUB" ||
    typeof value.isSystem !== "boolean" || !isVersion(value.version) ||
    !isTimestamp(value.createdAt) || !isTimestamp(value.updatedAt) || !isNullableTimestamp(value.archivedAt)
  ) return null;
  return value as unknown as Account;
}

function parseCategory(value: unknown): Category | null {
  if (
    !isRecord(value) || !isUUID(value.id) || (value.type !== "income" && value.type !== "expense") ||
    typeof value.name !== "string" || typeof value.color !== "string" || !colorPattern.test(value.color) ||
    !Number.isSafeInteger(value.sortOrder) || typeof value.isSystem !== "boolean" || !isVersion(value.version) ||
    !isTimestamp(value.createdAt) || !isTimestamp(value.updatedAt) || !isNullableTimestamp(value.archivedAt)
  ) return null;
  return value as unknown as Category;
}

function parseTransaction(value: unknown): FinanceTransaction | null {
  if (
    !isRecord(value) || !isUUID(value.id) ||
    (value.type !== "income" && value.type !== "expense" && value.type !== "transfer") ||
    !isUUID(value.accountId) || !(value.toAccountId === null || isUUID(value.toAccountId)) ||
    !(value.categoryId === null || isUUID(value.categoryId)) || !isInt64String(value.amountCents, true) ||
    !isLocalDate(value.eventDate) || typeof value.note !== "string" || typeof value.isBalanceAdjustment !== "boolean" ||
    typeof value.source !== "string" || !(value.createdByUserId === null || isUUID(value.createdByUserId)) ||
    !isTimestamp(value.createdAt) || !isTimestamp(value.updatedAt) || !isNullableTimestamp(value.deletedAt) ||
    !(value.deletionReason === null || typeof value.deletionReason === "string") || !isVersion(value.version)
  ) return null;
  return value as unknown as FinanceTransaction;
}

function parsePage<T>(value: unknown, field: string, parser: (item: unknown) => T | null): { items: T[]; nextCursor: string | null } | null {
  if (!isRecord(value) || !Array.isArray(value[field]) || !isCursor(value.nextCursor)) return null;
  const items = value[field].map(parser);
  if (!items.every((item): item is T => item !== null)) return null;
  return { items, nextCursor: value.nextCursor };
}

function parseAccountPage(value: unknown): AccountPage | null {
  const page = parsePage(value, "accounts", parseAccount);
  return page ? { accounts: page.items, nextCursor: page.nextCursor } : null;
}

function parseCategoryPage(value: unknown): CategoryPage | null {
  const page = parsePage(value, "categories", parseCategory);
  return page ? { categories: page.items, nextCursor: page.nextCursor } : null;
}

function parseTransactionPage(value: unknown): TransactionPage | null {
  const page = parsePage(value, "transactions", parseTransaction);
  return page ? { transactions: page.items, nextCursor: page.nextCursor } : null;
}

function parseSummary(value: unknown): FinanceSummary | null {
  if (
    !isRecord(value) || !isLocalDate(value.from) || !isLocalDate(value.to) || !isInt64String(value.householdTotalCents) ||
    !isRecord(value.cashFlow) || !isInt64String(value.cashFlow.incomeCents) || !isInt64String(value.cashFlow.expenseCents)
  ) return null;
  return value as unknown as FinanceSummary;
}

function parseAccountBalance(value: unknown): AccountBalance | null {
  if (!isRecord(value) || !isUUID(value.accountId) || typeof value.name !== "string" || !isNullableTimestamp(value.archivedAt) || !isInt64String(value.balanceCents)) return null;
  return value as unknown as AccountBalance;
}

function parseAccountBalancePage(value: unknown): AccountBalancePage | null {
  const page = parsePage(value, "accountBalances", parseAccountBalance);
  return page ? { accountBalances: page.items, nextCursor: page.nextCursor } : null;
}

function parseCategoryExpense(value: unknown): CategoryExpense | null {
  if (!isRecord(value) || !isUUID(value.categoryId) || typeof value.name !== "string" || !isInt64String(value.amountCents)) return null;
  return value as unknown as CategoryExpense;
}

function parseCategoryExpensePage(value: unknown): CategoryExpensePage | null {
  const page = parsePage(value, "expenseByCategory", parseCategoryExpense);
  return page ? { expenseByCategory: page.items, nextCursor: page.nextCursor } : null;
}

function queryPath(path: string, values: Readonly<Record<string, string | number | undefined>>): string {
  const query = new URLSearchParams();
  for (const [key, value] of Object.entries(values)) {
    if (value !== undefined && value !== "") query.set(key, String(value));
  }
  const encoded = query.toString();
  return encoded ? `${path}?${encoded}` : path;
}

function entityPath(householdID: string, resource: string, entityID?: string, action?: string): string {
  const base = `/api/v1/households/${encodeURIComponent(householdID)}/finance/${resource}`;
  return [base, entityID && encodeURIComponent(entityID), action].filter(Boolean).join("/");
}

function validateEntityResponse<T extends { version: string }>(response: APIResponse<T>): MutationResult<T> {
  if (response.etag !== `\"v${response.data.version}\"`) throw new APIError("invalid_response");
  return { resource: response.data, replayed: response.replayed };
}

export class FinanceAPI {
  public constructor(private readonly api: APIClient) {}

  public async listAccounts(householdID: string, options: AccountListOptions = {}, signal?: AbortSignal): Promise<AccountPage> {
    const path = queryPath(entityPath(householdID, "accounts"), { state: options.state, limit: options.limit, cursor: options.cursor });
    return (await this.api.requestJSON(path, parseAccountPage, { method: "GET" }, signal)).data;
  }

  public async createAccount(householdID: string, input: AccountCreateInput, key: string, signal?: AbortSignal): Promise<MutationResult<Account>> {
    return validateEntityResponse(await this.api.requestJSON(entityPath(householdID, "accounts"), parseAccount, { method: "POST", body: input, idempotencyKey: key }, signal));
  }

  public async updateAccount(householdID: string, accountID: string, patch: AccountPatchInput, version: string, signal?: AbortSignal): Promise<Account> {
    return validateEntityResponse(await this.api.requestJSON(entityPath(householdID, "accounts", accountID), parseAccount, { method: "PATCH", body: patch, ifMatch: `\"v${version}\"` }, signal)).resource;
  }

  public async setAccountArchived(householdID: string, accountID: string, version: string, archived: boolean, signal?: AbortSignal): Promise<Account> {
    return validateEntityResponse(await this.api.requestJSON(entityPath(householdID, "accounts", accountID, archived ? "archive" : "restore"), parseAccount, { method: "POST", body: {}, ifMatch: `\"v${version}\"` }, signal)).resource;
  }

  public async listCategories(householdID: string, options: CategoryListOptions, signal?: AbortSignal): Promise<CategoryPage> {
    const path = queryPath(entityPath(householdID, "categories"), { type: options.type, state: options.state, limit: options.limit, cursor: options.cursor });
    return (await this.api.requestJSON(path, parseCategoryPage, { method: "GET" }, signal)).data;
  }

  public async createCategory(householdID: string, input: CategoryCreateInput, key: string, signal?: AbortSignal): Promise<MutationResult<Category>> {
    return validateEntityResponse(await this.api.requestJSON(entityPath(householdID, "categories"), parseCategory, { method: "POST", body: input, idempotencyKey: key }, signal));
  }

  public async updateCategory(householdID: string, categoryID: string, patch: CategoryPatchInput, version: string, signal?: AbortSignal): Promise<Category> {
    return validateEntityResponse(await this.api.requestJSON(entityPath(householdID, "categories", categoryID), parseCategory, { method: "PATCH", body: patch, ifMatch: `\"v${version}\"` }, signal)).resource;
  }

  public async setCategoryArchived(householdID: string, categoryID: string, version: string, archived: boolean, signal?: AbortSignal): Promise<Category> {
    return validateEntityResponse(await this.api.requestJSON(entityPath(householdID, "categories", categoryID, archived ? "archive" : "restore"), parseCategory, { method: "POST", body: {}, ifMatch: `\"v${version}\"` }, signal)).resource;
  }

  public async listTransactions(householdID: string, options: TransactionListOptions = {}, signal?: AbortSignal): Promise<TransactionPage> {
    const path = queryPath(entityPath(householdID, "transactions"), {
      from: options.from,
      to: options.to,
      type: options.type,
      accountId: options.accountId,
      categoryId: options.categoryId,
      state: options.state,
      limit: options.limit,
      cursor: options.cursor,
    });
    return (await this.api.requestJSON(path, parseTransactionPage, { method: "GET" }, signal)).data;
  }

  public async createTransaction(householdID: string, input: TransactionCreateInput, key: string, signal?: AbortSignal): Promise<MutationResult<FinanceTransaction>> {
    return validateEntityResponse(await this.api.requestJSON(entityPath(householdID, "transactions"), parseTransaction, { method: "POST", body: input, idempotencyKey: key }, signal));
  }

  public async updateTransaction(householdID: string, transactionID: string, patch: TransactionPatchInput, version: string, signal?: AbortSignal): Promise<FinanceTransaction> {
    return validateEntityResponse(await this.api.requestJSON(entityPath(householdID, "transactions", transactionID), parseTransaction, { method: "PATCH", body: patch, ifMatch: `\"v${version}\"` }, signal)).resource;
  }

  public async deleteTransaction(householdID: string, transactionID: string, reason: string, version: string, signal?: AbortSignal): Promise<FinanceTransaction> {
    return validateEntityResponse(await this.api.requestJSON(entityPath(householdID, "transactions", transactionID, "delete"), parseTransaction, { method: "POST", body: { reason }, ifMatch: `\"v${version}\"` }, signal)).resource;
  }

  public async restoreTransaction(householdID: string, transactionID: string, version: string, signal?: AbortSignal): Promise<FinanceTransaction> {
    return validateEntityResponse(await this.api.requestJSON(entityPath(householdID, "transactions", transactionID, "restore"), parseTransaction, { method: "POST", body: {}, ifMatch: `\"v${version}\"` }, signal)).resource;
  }

  public async getSummary(householdID: string, from: string, to: string, signal?: AbortSignal): Promise<FinanceSummary> {
    const path = queryPath(entityPath(householdID, "summary"), { from, to });
    return (await this.api.requestJSON(path, parseSummary, { method: "GET" }, signal)).data;
  }

  public async listAccountBalances(householdID: string, to: string, options: PageOptions = {}, signal?: AbortSignal): Promise<AccountBalancePage> {
    const path = queryPath(entityPath(householdID, "summary/account-balances"), { to, ...options });
    return (await this.api.requestJSON(path, parseAccountBalancePage, { method: "GET" }, signal)).data;
  }

  public async listCategoryExpenses(householdID: string, from: string, to: string, options: PageOptions = {}, signal?: AbortSignal): Promise<CategoryExpensePage> {
    const path = queryPath(entityPath(householdID, "summary/expense-by-category"), { from, to, ...options });
    return (await this.api.requestJSON(path, parseCategoryExpensePage, { method: "GET" }, signal)).data;
  }
}

export const financeParsers = {
  account: parseAccount,
  category: parseCategory,
  transaction: parseTransaction,
  accountPage: parseAccountPage,
  categoryPage: parseCategoryPage,
  transactionPage: parseTransactionPage,
  summary: parseSummary,
  accountBalancePage: parseAccountBalancePage,
  categoryExpensePage: parseCategoryExpensePage,
};
