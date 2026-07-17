import { afterEach, describe, expect, it, vi } from "vitest";

import { APIError } from "../../lib/api";
import {
  BackupConfirmKeyManager,
  ImportOperationGate,
  budgetMonthFromInput,
  currentBudgetMonth,
  isFreshPreview,
  monthInputValue,
  nextFinanceRevision,
  resolveImportError,
  safeConfirmation,
  safePreview,
  validateBackupFile,
} from "./backupImportModel";

const counts = {
  accounts: 1,
  categories: 2,
  transactions: 3,
  budgets: 0,
  goals: 0,
  goalContributions: 0,
  debts: 0,
  debtPayments: 0,
};

afterEach(() => {
  vi.unstubAllGlobals();
});

describe("backup import input and state helpers", () => {
  it("validates file boundaries without reading its content", () => {
    expect(validateBackupFile(null)).toContain("непустой");
    expect(validateBackupFile({ name: "backup.json", size: 0, type: "application/json" })).toContain("непустой");
    expect(validateBackupFile({ name: "backup.json", size: 32 * 1024 * 1024, type: "" })).toBeNull();
    expect(validateBackupFile({ name: "backup.json", size: 32 * 1024 * 1024 + 1, type: "application/json" })).toContain("32");
    expect(validateBackupFile({ name: "backup.txt", size: 10, type: "text/plain" })).toContain("JSON");
    expect(validateBackupFile({ name: "opaque.bin", size: 10, type: "application/json" })).toBeNull();
  });

  it("uses an exact first-day budget month", () => {
    expect(currentBudgetMonth(new Date(2026, 6, 16))).toBe("2026-07-01");
    expect(monthInputValue("2026-07-01")).toBe("2026-07");
    expect(budgetMonthFromInput("2026-12")).toBe("2026-12-01");
    expect(budgetMonthFromInput("0000-01")).toBeNull();
    expect(budgetMonthFromInput("2026-13")).toBeNull();
  });

  it("keeps one idempotency key for a preview and replaces it only after a new preview", () => {
    const storage = { setItem: vi.fn(), getItem: vi.fn(), removeItem: vi.fn() };
    vi.stubGlobal("localStorage", storage);
    vi.stubGlobal("sessionStorage", storage);
    let sequence = 0;
    const keys = new BackupConfirmKeyManager(() => `crypto-key-${++sequence}`);
    expect(keys.current()).toBeNull();
    expect(keys.previewSucceeded()).toBe("crypto-key-1");
    expect(keys.current()).toBe("crypto-key-1");
    expect(keys.current()).toBe("crypto-key-1");
    expect(keys.previewSucceeded()).toBe("crypto-key-2");
    keys.invalidate();
    expect(keys.current()).toBeNull();
    expect(storage.setItem).not.toHaveBeenCalled();
  });

  it("prevents parallel preview/confirm operations synchronously", () => {
    const gate = new ImportOperationGate();
    expect(gate.begin()).toBe(true);
    expect(gate.begin()).toBe(false);
    expect(gate.isActive()).toBe(true);
    gate.end();
    expect(gate.begin()).toBe(true);
  });

  it("removes digest/token/run ID from render-safe state and tracks freshness", () => {
    const preview = safePreview({
      backupDigest: `sha256:${"a".repeat(64)}`,
      confirmationToken: "secret-preview-token",
      expiresAt: "2026-07-16T12:10:00Z",
      budgetMonth: "2026-07-01",
      counts,
      totals: {
        incomeCents: "100",
        expenseCents: "50",
        transferCents: "10",
        householdBalanceCents: "50",
      },
      warnings: [],
    });
    const confirmation = safeConfirmation({
      importRunId: "66000000-0000-4000-8000-000000000001",
      status: "completed",
      policyVersion: "backup-v5-import/1",
      completedAt: "2026-07-16T12:05:00Z",
      counts,
      warningCounts: {
        legacyOwnerNotLinked: 0,
        archiveTimeApproximated: 0,
        goalExceedsTarget: 0,
        debtOverpaid: 0,
        systemResourcePreserved: 0,
        budgetMonthExplicitChoice: 0,
      },
    }, true);

    expect(JSON.stringify(preview)).not.toContain("secret-preview-token");
    expect(JSON.stringify(preview)).not.toContain("sha256:");
    expect(JSON.stringify(confirmation)).not.toContain("66000000");
    expect(isFreshPreview(preview, Date.parse("2026-07-16T12:09:59Z"))).toBe(true);
    expect(isFreshPreview(preview, Date.parse("2026-07-16T12:10:00Z"))).toBe(false);
  });

  it("increments the finance data revision after a successful import", () => {
    expect(nextFinanceRevision(0)).toBe(1);
    expect(nextFinanceRevision(7)).toBe(8);
  });
});

describe("backup import safe error transitions", () => {
  it.each([
    [new APIError("session_expired", 401), true, false, true],
    [new APIError("request_failed", 409, "household_not_empty"), true, false, false],
    [new APIError("request_failed", 409, "idempotency_conflict"), true, false, false],
    [new APIError("request_failed", 409, "import_state_conflict"), true, false, false],
    [new APIError("request_failed", 410, "preview_token_invalid"), true, false, false],
    [new APIError("offline"), false, true, false],
    [new APIError("timeout"), false, true, false],
    [new APIError("request_failed", 429, "rate_limited"), false, true, false],
    [new APIError("request_failed", 503, "import_unavailable"), false, true, false],
  ])("maps confirm errors without backend detail", (error, invalidates, retry, expired) => {
    const result = resolveImportError(error, "confirm");
    expect(result.invalidatePreview).toBe(invalidates);
    expect(result.retryConfirm).toBe(retry);
    expect(result.sessionExpired).toBe(expired);
    if (error.code) expect(result.message).not.toContain(error.code);
  });

  it("does not claim a preview can retry after an offline failure", () => {
    expect(resolveImportError(new APIError("offline"), "preview")).toMatchObject({
      invalidatePreview: false,
      retryConfirm: false,
    });
  });

  it.each([
    [new APIError("request_failed", 403, "forbidden"), "владельцу", true],
    [new APIError("request_failed", 413, "body_too_large"), "32", true],
    [new APIError("request_failed", 422, "backup_invalid"), "проверку", true],
    [new APIError("request_failed", 404, "not_found"), "недоступно", true],
    [new APIError("request_failed", 400, "invalid_import_request"), "некорректны", true],
  ])("handles non-retryable confirm failures", (error, text, invalidates) => {
    const result = resolveImportError(error, "confirm");
    expect(result.message).toContain(text);
    expect(result.invalidatePreview).toBe(invalidates);
    expect(result.retryConfirm).toBe(false);
  });

  it.each([
    new APIError("invalid_response", 201),
    new APIError("request_failed", 500, "internal_error"),
  ])("preserves the confirm key when the committed outcome may be uncertain", (error) => {
    expect(resolveImportError(error, "confirm")).toMatchObject({
      invalidatePreview: false,
      retryConfirm: true,
    });
  });
});
