import type { HouseholdRole } from "../../lib/api";
import type { TransactionCreateInput, TransactionType } from "../../lib/financeApi";

export const maximumMoneyCents = 9_000_000_000_000_000n;

export interface TransactionDraft {
  type: TransactionType;
  accountId: string;
  toAccountId: string;
  categoryId: string;
  amountRubles: string;
  eventDate: string;
  note: string;
  isBalanceAdjustment: boolean;
}

export interface TransactionValidation {
  input: TransactionCreateInput | null;
  error: string | null;
}

export function canManageDirectories(role: HouseholdRole): boolean {
  return role === "owner" || role === "admin";
}

export function runeLength(value: string): number {
  return Array.from(value).length;
}

export function rublesToCents(value: string): string | null {
  const normalized = value.replace(",", ".");
  if (!/^(0|[1-9][0-9]*)(?:\.([0-9]{1,2}))?$/.test(normalized)) return null;
  const [rubles = "0", fraction = ""] = normalized.split(".");
  try {
    const cents = BigInt(rubles) * 100n + BigInt(fraction.padEnd(2, "0") || "0");
    return cents > 0n && cents <= maximumMoneyCents ? cents.toString() : null;
  } catch {
    return null;
  }
}

export function centsToRubles(value: string): string {
  try {
    const cents = BigInt(value);
    const sign = cents < 0n ? "-" : "";
    const absolute = cents < 0n ? -cents : cents;
    return `${sign}${absolute / 100n}.${(absolute % 100n).toString().padStart(2, "0")}`;
  } catch {
    return "0.00";
  }
}

export function formatMoney(value: string): string {
  const raw = centsToRubles(value);
  const [whole = "0", fraction = "00"] = raw.split(".");
  const negative = whole.startsWith("-");
  const digits = negative ? whole.slice(1) : whole;
  const grouped = digits.replace(/\B(?=(\d{3})+(?!\d))/g, " ");
  return `${negative ? "−" : ""}${grouped},${fraction} ₽`;
}

export function todayLocalDate(now = new Date()): string {
  const year = now.getFullYear();
  const month = String(now.getMonth() + 1).padStart(2, "0");
  const day = String(now.getDate()).padStart(2, "0");
  return `${year}-${month}-${day}`;
}

export function monthRange(now = new Date()): { from: string; to: string } {
  const year = now.getFullYear();
  const month = now.getMonth();
  return {
    from: todayLocalDate(new Date(year, month, 1)),
    to: todayLocalDate(new Date(year, month + 1, 0)),
  };
}

export function validateTransactionDraft(draft: TransactionDraft): TransactionValidation {
  const amountCents = rublesToCents(draft.amountRubles);
  const note = draft.note.trim();
  if (!draft.accountId) return { input: null, error: "Выберите счёт." };
  if (!amountCents) return { input: null, error: "Введите положительную сумму не более двух знаков после запятой." };
  if (!/^\d{4}-\d{2}-\d{2}$/.test(draft.eventDate)) return { input: null, error: "Укажите дату операции." };
  if (runeLength(note) > 1_000) return { input: null, error: "Комментарий не должен превышать 1000 символов." };

  if (draft.type === "transfer") {
    if (!draft.toAccountId) return { input: null, error: "Выберите счёт назначения." };
    if (draft.toAccountId === draft.accountId) return { input: null, error: "Счета перевода должны отличаться." };
    if (draft.isBalanceAdjustment) return { input: null, error: "Перевод не может быть корректировкой баланса." };
  } else if (!draft.categoryId) {
    return { input: null, error: "Выберите категорию." };
  }

  return {
    input: {
      type: draft.type,
      accountId: draft.accountId,
      toAccountId: draft.type === "transfer" ? draft.toAccountId : null,
      categoryId: draft.type === "transfer" ? null : draft.categoryId,
      amountCents,
      eventDate: draft.eventDate,
      note,
      isBalanceAdjustment: draft.type === "transfer" ? false : draft.isBalanceAdjustment,
    },
    error: null,
  };
}

export function financeErrorMessage(status: number | undefined, code: string | undefined): string {
  if (status === 403) return "У вашей роли недостаточно прав для этого действия.";
  if (status === 404) return "Объект недоступен или уже удалён.";
  if (code === "version_conflict") return "Данные уже изменились в другой сессии. Обновите список и повторите действие.";
  if (code === "idempotency_conflict") return "Повтор запроса отличается от исходного. Измените данные и отправьте снова.";
  if (code === "system_resource_immutable") return "Системный справочник нельзя изменить или архивировать.";
  if (status === 409) return "Операция конфликтует с текущим состоянием данных.";
  if (status === 422) return "Проверьте заполнение формы.";
  return "Не удалось выполнить операцию. Попробуйте ещё раз.";
}

export function transactionPayloadKey(input: TransactionCreateInput): string {
  return JSON.stringify([
    input.type,
    input.accountId,
    input.toAccountId,
    input.categoryId,
    input.amountCents,
    input.eventDate,
    input.note,
    input.isBalanceAdjustment,
  ]);
}
