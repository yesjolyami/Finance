import { APIClient, APIError } from "./api";

export const backupImportTimeoutMilliseconds = 90_000;
export const maximumBackupV5Bytes = 32 * 1024 * 1024;

export interface BackupImportCounts {
  accounts: number;
  categories: number;
  transactions: number;
  budgets: number;
  goals: number;
  goalContributions: number;
  debts: number;
  debtPayments: number;
}

export interface BackupImportTotals {
  incomeCents: string;
  expenseCents: string;
  transferCents: string;
  householdBalanceCents: string;
}

export type BackupWarningCode =
  | "legacy_owner_not_linked"
  | "archive_time_approximated"
  | "goal_exceeds_target"
  | "debt_overpaid"
  | "system_resource_preserved"
  | "budget_month_explicit_choice";

export interface BackupPreviewWarning {
  code: BackupWarningCode;
  count: number;
}

export interface BackupPreviewResponse {
  backupDigest: string;
  expiresAt: string;
  confirmationToken: string;
  budgetMonth: string;
  counts: BackupImportCounts;
  totals: BackupImportTotals;
  warnings: BackupPreviewWarning[];
}

export interface BackupConfirmWarningCounts {
  legacyOwnerNotLinked: number;
  archiveTimeApproximated: number;
  goalExceedsTarget: number;
  debtOverpaid: number;
  systemResourcePreserved: number;
  budgetMonthExplicitChoice: number;
}

export interface BackupConfirmResponse {
  importRunId: string;
  status: "completed";
  policyVersion: "backup-v5-import/1";
  completedAt: string;
  counts: BackupImportCounts;
  warningCounts: BackupConfirmWarningCounts;
}

export interface BackupConfirmResult {
  response: BackupConfirmResponse;
  replayed: boolean;
}

const uuidPattern = /^[0-9a-f]{8}-[0-9a-f]{4}-[1-5][0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$/i;
const digestPattern = /^sha256:[0-9a-f]{64}$/;
const moneyPattern = /^-?(0|[1-9][0-9]*)$/;
const maximumInt64 = 9_223_372_036_854_775_807n;
const minimumInt64 = -9_223_372_036_854_775_808n;
const countKeys = [
  "accounts",
  "categories",
  "transactions",
  "budgets",
  "goals",
  "goalContributions",
  "debts",
  "debtPayments",
] as const;
const warningCodes: readonly BackupWarningCode[] = [
  "legacy_owner_not_linked",
  "archive_time_approximated",
  "goal_exceeds_target",
  "debt_overpaid",
  "system_resource_preserved",
  "budget_month_explicit_choice",
];
const warningCountKeys = [
  "legacyOwnerNotLinked",
  "archiveTimeApproximated",
  "goalExceedsTarget",
  "debtOverpaid",
  "systemResourcePreserved",
  "budgetMonthExplicitChoice",
] as const;

function isRecord(value: unknown): value is Record<string, unknown> {
  return typeof value === "object" && value !== null && !Array.isArray(value);
}

function hasExactKeys(value: Record<string, unknown>, expected: readonly string[]): boolean {
  const actual = Object.keys(value);
  return actual.length === expected.length && expected.every((key) => Object.hasOwn(value, key));
}

function isTimestamp(value: unknown): value is string {
  return typeof value === "string" && value.includes("T") && Number.isFinite(Date.parse(value));
}

export function isBudgetMonth(value: unknown): value is string {
  if (typeof value !== "string" || !/^\d{4}-(0[1-9]|1[0-2])-01$/.test(value)) return false;
  const year = Number(value.slice(0, 4));
  return year >= 1;
}

function isCount(value: unknown): value is number {
  return Number.isSafeInteger(value) && typeof value === "number" && value >= 0 && value <= 300_000;
}

function isMoney(value: unknown): value is string {
  if (typeof value !== "string" || !moneyPattern.test(value)) return false;
  try {
    const amount = BigInt(value);
    return amount >= minimumInt64 && amount <= maximumInt64;
  } catch {
    return false;
  }
}

function parseCounts(value: unknown): BackupImportCounts | null {
  if (!isRecord(value) || !hasExactKeys(value, countKeys) || !countKeys.every((key) => isCount(value[key]))) return null;
  return value as unknown as BackupImportCounts;
}

function parseTotals(value: unknown): BackupImportTotals | null {
  const keys = ["incomeCents", "expenseCents", "transferCents", "householdBalanceCents"] as const;
  if (!isRecord(value) || !hasExactKeys(value, keys) || !keys.every((key) => isMoney(value[key]))) return null;
  return value as unknown as BackupImportTotals;
}

function parseWarnings(value: unknown): BackupPreviewWarning[] | null {
  if (!Array.isArray(value) || value.length > warningCodes.length) return null;
  const seen = new Set<string>();
  const result: BackupPreviewWarning[] = [];
  for (const warning of value) {
    if (
      !isRecord(warning) ||
      !hasExactKeys(warning, ["code", "count"]) ||
      typeof warning.code !== "string" ||
      !warningCodes.includes(warning.code as BackupWarningCode) ||
      !isCount(warning.count) ||
      seen.has(warning.code)
    ) return null;
    seen.add(warning.code);
    result.push({ code: warning.code as BackupWarningCode, count: warning.count });
  }
  return result;
}

export function parseBackupPreview(value: unknown): BackupPreviewResponse | null {
  if (
    !isRecord(value) ||
    !hasExactKeys(value, ["backupDigest", "expiresAt", "confirmationToken", "budgetMonth", "counts", "totals", "warnings"]) ||
    typeof value.backupDigest !== "string" ||
    !digestPattern.test(value.backupDigest) ||
    !isTimestamp(value.expiresAt) ||
    typeof value.confirmationToken !== "string" ||
    !/^[A-Za-z0-9_-]{43}$/.test(value.confirmationToken) ||
    !isBudgetMonth(value.budgetMonth)
  ) return null;
  const counts = parseCounts(value.counts);
  const totals = parseTotals(value.totals);
  const warnings = parseWarnings(value.warnings);
  if (!counts || !totals || !warnings) return null;
  return {
    backupDigest: value.backupDigest,
    expiresAt: value.expiresAt,
    confirmationToken: value.confirmationToken,
    budgetMonth: value.budgetMonth,
    counts,
    totals,
    warnings,
  };
}

function parseWarningCounts(value: unknown): BackupConfirmWarningCounts | null {
  if (
    !isRecord(value) ||
    !hasExactKeys(value, warningCountKeys) ||
    !warningCountKeys.every((key) => isCount(value[key]))
  ) return null;
  return value as unknown as BackupConfirmWarningCounts;
}

export function parseBackupConfirm(value: unknown): BackupConfirmResponse | null {
  if (
    !isRecord(value) ||
    !hasExactKeys(value, ["importRunId", "status", "policyVersion", "completedAt", "counts", "warningCounts"]) ||
    typeof value.importRunId !== "string" ||
    !uuidPattern.test(value.importRunId) ||
    value.status !== "completed" ||
    value.policyVersion !== "backup-v5-import/1" ||
    !isTimestamp(value.completedAt)
  ) return null;
  const counts = parseCounts(value.counts);
  const warningCounts = parseWarningCounts(value.warningCounts);
  if (!counts || !warningCounts) return null;
  return {
    importRunId: value.importRunId,
    status: value.status,
    policyVersion: value.policyVersion,
    completedAt: value.completedAt,
    counts,
    warningCounts,
  };
}

function importPath(householdID: string, action: "preview" | "confirm"): string {
  return `/api/v1/households/${encodeURIComponent(householdID)}/imports/backup-v5/${action}`;
}

export class BackupImportAPI {
  public constructor(private readonly api: APIClient) {}

  public async preview(
    householdID: string,
    body: Blob,
    budgetMonth: string,
    signal?: AbortSignal,
  ): Promise<BackupPreviewResponse> {
    const result = await this.api.requestBackupImportJSON(
      importPath(householdID, "preview"),
      parseBackupPreview,
      { body, budgetMonth, timeoutMilliseconds: backupImportTimeoutMilliseconds },
      signal,
    );
    if (result.status !== 200 || result.idempotencyReplayed !== null) throw new APIError("invalid_response", result.status);
    return result.data;
  }

  public async confirm(
    householdID: string,
    body: Blob,
    budgetMonth: string,
    previewToken: string,
    idempotencyKey: string,
    signal?: AbortSignal,
  ): Promise<BackupConfirmResult> {
    const result = await this.api.requestBackupImportJSON(
      importPath(householdID, "confirm"),
      parseBackupConfirm,
      {
        body,
        budgetMonth,
        previewToken,
        idempotencyKey,
        timeoutMilliseconds: backupImportTimeoutMilliseconds,
      },
      signal,
    );
    const first = result.status === 201 && result.idempotencyReplayed === null;
    const replay = result.status === 200 && result.idempotencyReplayed === "true";
    if (!first && !replay) throw new APIError("invalid_response", result.status);
    return { response: result.data, replayed: replay };
  }
}
