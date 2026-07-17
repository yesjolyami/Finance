import { APIError } from "../../lib/api";
import type { DirectoryState, FinanceTransaction, TransactionState, TransactionType } from "../../lib/financeApi";
import { financeErrorMessage } from "./financeModel";

export function safeFinanceError(error: unknown, onSessionExpired: () => void): string | null {
  if (error instanceof APIError) {
    if (error.kind === "session_expired") {
      onSessionExpired();
      return null;
    }
    if (error.kind === "aborted") return null;
    if (error.kind === "offline") return "Нет соединения с API. Проверьте сеть и повторите запрос.";
    if (error.kind === "timeout") return "API не ответил вовремя. Повторите запрос.";
    if (error.status !== undefined) return financeErrorMessage(error.status, error.code);
    return error.message;
  }
  return "Не удалось выполнить операцию. Попробуйте ещё раз.";
}

export function mergeUniqueByID<T extends { id: string }>(current: readonly T[], next: readonly T[]): T[] {
  const seen = new Set(current.map((item) => item.id));
  return [...current, ...next.filter((item) => !seen.has(item.id))];
}

export function replaceByID<T extends { id: string }>(current: readonly T[], updated: T): T[] {
  return current.map((item) => item.id === updated.id ? updated : item);
}

export function reconcileByID<T extends { id: string; createdAt: string }>(current: readonly T[], updated: T, visible: boolean): T[] {
  const without = current.filter((item) => item.id !== updated.id);
  return visible ? [updated, ...without].sort((left, right) => right.createdAt.localeCompare(left.createdAt) || right.id.localeCompare(left.id)) : without;
}

export function matchesDirectoryState(item: { archivedAt: string | null }, state: DirectoryState): boolean {
  return state === "all" || (state === "active" ? item.archivedAt === null : item.archivedAt !== null);
}

export interface TransactionViewFilter {
  from: string;
  to: string;
  type: "" | TransactionType;
  accountId: string;
  categoryId: string;
  state: TransactionState;
}

export function matchesTransactionFilter(transaction: FinanceTransaction, filter: TransactionViewFilter): boolean {
  if (filter.from && transaction.eventDate < filter.from) return false;
  if (filter.to && transaction.eventDate > filter.to) return false;
  if (filter.type && transaction.type !== filter.type) return false;
  if (filter.accountId && transaction.accountId !== filter.accountId && transaction.toAccountId !== filter.accountId) return false;
  if (filter.categoryId && transaction.categoryId !== filter.categoryId) return false;
  if (filter.state === "active" && transaction.deletedAt !== null) return false;
  if (filter.state === "deleted" && transaction.deletedAt === null) return false;
  return true;
}
