import { APIError } from "../../lib/api";
import { maximumBackupV5Bytes, type BackupConfirmResponse, type BackupPreviewResponse } from "../../lib/backupImportApi";
import { secureUUID, type UUIDFactory } from "../../lib/idempotency";

export type BackupImportPhase = "idle" | "previewing" | "ready" | "confirming" | "success";

export type SafeBackupPreview = Pick<BackupPreviewResponse, "expiresAt" | "budgetMonth" | "counts" | "totals" | "warnings">;
export type SafeBackupConfirmation = Pick<BackupConfirmResponse, "status" | "completedAt" | "counts" | "warningCounts"> & {
  replayed: boolean;
};

export interface ImportErrorResolution {
  message: string;
  invalidatePreview: boolean;
  retryConfirm: boolean;
  sessionExpired: boolean;
}

export class BackupConfirmKeyManager {
  private key: string | null = null;

  public constructor(private readonly createUUID: UUIDFactory = secureUUID) {}

  public previewSucceeded(): string {
    this.key = this.createUUID();
    return this.key;
  }

  public current(): string | null {
    return this.key;
  }

  public invalidate(): void {
    this.key = null;
  }
}

export class ImportOperationGate {
  private active = false;

  public begin(): boolean {
    if (this.active) return false;
    this.active = true;
    return true;
  }

  public end(): void {
    this.active = false;
  }

  public isActive(): boolean {
    return this.active;
  }
}

export function currentBudgetMonth(now = new Date()): string {
  const year = String(now.getFullYear()).padStart(4, "0");
  const month = String(now.getMonth() + 1).padStart(2, "0");
  return `${year}-${month}-01`;
}

export function monthInputValue(budgetMonth: string): string {
  return budgetMonth.slice(0, 7);
}

export function budgetMonthFromInput(value: string): string | null {
  if (!/^\d{4}-(0[1-9]|1[0-2])$/.test(value) || Number(value.slice(0, 4)) < 1) return null;
  return `${value}-01`;
}

export function validateBackupFile(file: Pick<File, "name" | "size" | "type"> | null): string | null {
  if (!file || file.size === 0) return "Выберите непустой JSON-файл резервной копии.";
  if (file.size > maximumBackupV5Bytes) return "Размер файла не должен превышать 32 МиБ.";
  const jsonType = file.type.toLowerCase() === "application/json";
  const jsonExtension = file.name.toLowerCase().endsWith(".json");
  if (!jsonType && !jsonExtension) return "Выберите файл в формате JSON.";
  return null;
}

export function safePreview(response: BackupPreviewResponse): SafeBackupPreview {
  return {
    expiresAt: response.expiresAt,
    budgetMonth: response.budgetMonth,
    counts: response.counts,
    totals: response.totals,
    warnings: response.warnings,
  };
}

export function safeConfirmation(response: BackupConfirmResponse, replayed: boolean): SafeBackupConfirmation {
  return {
    status: response.status,
    completedAt: response.completedAt,
    counts: response.counts,
    warningCounts: response.warningCounts,
    replayed,
  };
}

export function isFreshPreview(preview: SafeBackupPreview | null, now = Date.now()): boolean {
  if (!preview) return false;
  const expiresAt = Date.parse(preview.expiresAt);
  return Number.isFinite(expiresAt) && expiresAt > now;
}

export function resolveImportError(error: unknown, operation: "preview" | "confirm"): ImportErrorResolution {
  if (!(error instanceof APIError)) {
    return {
      message: "Не удалось выполнить импорт. Попробуйте ещё раз.",
      invalidatePreview: operation === "confirm",
      retryConfirm: false,
      sessionExpired: false,
    };
  }
  if (error.kind === "session_expired") {
    return {
      message: "Сессия завершилась. Войдите снова.",
      invalidatePreview: true,
      retryConfirm: false,
      sessionExpired: true,
    };
  }
  if (error.kind === "offline") {
    return {
      message: operation === "confirm"
        ? "Ответ подтверждения не получен. После восстановления сети повторите подтверждение — ключ запроса сохранён."
        : "Нет соединения с API. Preview не создан.",
      invalidatePreview: false,
      retryConfirm: operation === "confirm",
      sessionExpired: false,
    };
  }
  if (error.kind === "timeout") {
    return {
      message: operation === "confirm"
        ? "Время ожидания истекло. Результат мог сохраниться: безопасно повторите подтверждение с тем же ключом."
        : "Preview не успел завершиться. Повторите запрос.",
      invalidatePreview: false,
      retryConfirm: operation === "confirm",
      sessionExpired: false,
    };
  }
  if (error.kind === "aborted") {
    return {
      message: "Запрос отменён.",
      invalidatePreview: false,
      retryConfirm: false,
      sessionExpired: false,
    };
  }
  if (error.kind === "invalid_response") {
    return {
      message: operation === "confirm"
        ? "API вернул неожиданный ответ. Безопасно повторите подтверждение с тем же ключом."
        : "API вернул неожиданный ответ. Preview не создан.",
      invalidatePreview: false,
      retryConfirm: operation === "confirm",
      sessionExpired: false,
    };
  }

  const code = error.code;
  if (error.status === 400) {
    return {
      message: "Файл или параметры импорта некорректны.",
      invalidatePreview: operation === "confirm",
      retryConfirm: false,
      sessionExpired: false,
    };
  }
  if (error.status === 403) {
    return {
      message: "Импорт доступен только владельцу пространства.",
      invalidatePreview: true,
      retryConfirm: false,
      sessionExpired: false,
    };
  }
  if (code === "household_not_empty") {
    return {
      message: "Импорт возможен только в полностью пустое пространство.",
      invalidatePreview: true,
      retryConfirm: false,
      sessionExpired: false,
    };
  }
  if (code === "idempotency_conflict") {
    return {
      message: "Подтверждение больше не соответствует исходному preview. Создайте новый preview.",
      invalidatePreview: true,
      retryConfirm: false,
      sessionExpired: false,
    };
  }
  if (code === "import_state_conflict") {
    return {
      message: "Состояние импорта изменилось. Создайте новый preview перед повтором.",
      invalidatePreview: true,
      retryConfirm: false,
      sessionExpired: false,
    };
  }
  if (error.status === 410 || code === "preview_token_invalid") {
    return {
      message: "Preview истёк или уже недействителен. Создайте новый preview.",
      invalidatePreview: true,
      retryConfirm: false,
      sessionExpired: false,
    };
  }
  if (error.status === 413) {
    return {
      message: "Файл превышает допустимый размер 32 МиБ.",
      invalidatePreview: operation === "confirm",
      retryConfirm: false,
      sessionExpired: false,
    };
  }
  if (error.status === 422) {
    return {
      message: "Резервная копия не прошла проверку формата или связей.",
      invalidatePreview: operation === "confirm",
      retryConfirm: false,
      sessionExpired: false,
    };
  }
  if (error.status === 429) {
    return {
      message: "Слишком много попыток импорта. Подождите и повторите запрос.",
      invalidatePreview: false,
      retryConfirm: operation === "confirm",
      sessionExpired: false,
    };
  }
  if (error.status === 503) {
    return {
      message: "Импорт временно отключён на сервере.",
      invalidatePreview: false,
      retryConfirm: operation === "confirm",
      sessionExpired: false,
    };
  }
  if (error.status !== undefined && error.status >= 500) {
    return {
      message: operation === "confirm"
        ? "Сервер не завершил ответ. Безопасно повторите подтверждение с тем же ключом."
        : "Сервис импорта временно недоступен.",
      invalidatePreview: false,
      retryConfirm: operation === "confirm",
      sessionExpired: false,
    };
  }
  if (error.status === 404) {
    return {
      message: "Пространство недоступно.",
      invalidatePreview: true,
      retryConfirm: false,
      sessionExpired: false,
    };
  }
  return {
    message: "Не удалось выполнить импорт. Попробуйте ещё раз.",
    invalidatePreview: operation === "confirm",
    retryConfirm: false,
    sessionExpired: false,
  };
}

export function nextFinanceRevision(current: number): number {
  return current + 1;
}
