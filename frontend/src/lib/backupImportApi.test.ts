import { afterEach, describe, expect, it, vi } from "vitest";

import { APIClient, APIError } from "./api";
import {
  BackupImportAPI,
  backupImportTimeoutMilliseconds,
  parseBackupConfirm,
  parseBackupPreview,
} from "./backupImportApi";

const householdID = "22000000-0000-4000-8000-000000000001";
const previewToken = "P".repeat(43);
const idempotencyKey = "55000000-0000-4000-8000-000000000001";
const counts = {
  accounts: 2,
  categories: 3,
  transactions: 4,
  budgets: 1,
  goals: 1,
  goalContributions: 0,
  debts: 1,
  debtPayments: 2,
};
const previewPayload = {
  backupDigest: `sha256:${"a".repeat(64)}`,
  expiresAt: "2026-07-16T12:10:00Z",
  confirmationToken: previewToken,
  budgetMonth: "2026-07-01",
  counts,
  totals: {
    incomeCents: "100000",
    expenseCents: "25000",
    transferCents: "5000",
    householdBalanceCents: "75000",
  },
  warnings: [{ code: "legacy_owner_not_linked", count: 1 }],
};
const confirmPayload = {
  importRunId: "66000000-0000-4000-8000-000000000001",
  status: "completed",
  policyVersion: "backup-v5-import/1",
  completedAt: "2026-07-16T12:05:00Z",
  counts,
  warningCounts: {
    legacyOwnerNotLinked: 1,
    archiveTimeApproximated: 0,
    goalExceedsTarget: 0,
    debtOverpaid: 0,
    systemResourcePreserved: 0,
    budgetMonthExplicitChoice: 1,
  },
};

function sessionProvider(tokens = ["current-session-token"]) {
  let index = 0;
  return {
    auth: {
      getSession: vi.fn(async () => {
        const accessToken = tokens[Math.min(index, tokens.length - 1)];
        index += 1;
        return { data: { session: accessToken ? { access_token: accessToken } : null }, error: null };
      }),
    },
  };
}

function jsonResponse(payload: unknown, status: number, extraHeaders: Record<string, string> = {}) {
  return new Response(JSON.stringify(payload), {
    status,
    headers: { "Content-Type": "application/json; charset=utf-8", ...extraHeaders },
  });
}

afterEach(() => {
  vi.useRealTimers();
  vi.restoreAllMocks();
});

describe("backup import response parsers", () => {
  it("accepts exact allowlisted preview and confirm schemas", () => {
    expect(parseBackupPreview(previewPayload)).toEqual(previewPayload);
    expect(parseBackupConfirm(confirmPayload)).toEqual(confirmPayload);
  });

  it.each([
    { ...previewPayload, confirmationToken: "" },
    { ...previewPayload, privateName: "secret" },
    { ...previewPayload, totals: { ...previewPayload.totals, incomeCents: 100000 } },
    { ...previewPayload, warnings: [{ code: "unknown_warning", count: 1 }] },
    { ...previewPayload, warnings: [{ code: "debt_overpaid", count: 1 }, { code: "debt_overpaid", count: 1 }] },
  ])("rejects unsafe or malformed preview data", (payload) => {
    expect(parseBackupPreview(payload)).toBeNull();
  });

  it.each([
    { ...confirmPayload, digest: `sha256:${"b".repeat(64)}` },
    { ...confirmPayload, importRunId: "not-a-uuid" },
    { ...confirmPayload, status: "pending" },
    { ...confirmPayload, counts: { ...counts, accounts: -1 } },
    { ...confirmPayload, warningCounts: { ...confirmPayload.warningCounts, extra: 1 } },
  ])("rejects unsafe or malformed confirm data", (payload) => {
    expect(parseBackupConfirm(payload)).toBeNull();
  });
});

describe("BackupImportAPI transport", () => {
  it("sends the same raw Blob with exact preview headers and a fresh official session", async () => {
    const body = new Blob(['{"version":5,"marker":"exact bytes"}'], { type: "application/json" });
    const sessions = sessionProvider(["rotated-preview-token"]);
    const fetcher = vi.fn<typeof fetch>(async (_input, init) => {
      expect(init?.body).toBe(body);
      expect(await (init?.body as Blob).text()).toBe('{"version":5,"marker":"exact bytes"}');
      expect(init?.method).toBe("POST");
      expect(init?.credentials).toBe("omit");
      const headers = new Headers(init?.headers);
      expect(Object.fromEntries(headers.entries())).toEqual({
        accept: "application/json",
        authorization: "Bearer rotated-preview-token",
        "content-type": "application/json",
        "import-budget-month": "2026-07-01",
      });
      expect(headers.has("Content-Encoding")).toBe(false);
      expect(headers.has("Import-Preview-Token")).toBe(false);
      expect(headers.has("Idempotency-Key")).toBe(false);
      expect(headers.has("X-Request-ID")).toBe(false);
      return jsonResponse(previewPayload, 200);
    });
    const api = new BackupImportAPI(new APIClient({ apiBaseUrl: "https://api.example.test", sessionProvider: sessions, fetcher }));

    await expect(api.preview(householdID, body, "2026-07-01")).resolves.toEqual(previewPayload);
    expect(sessions.auth.getSession).toHaveBeenCalledOnce();
    expect(fetcher.mock.calls[0]?.[0]).toBe(
      `https://api.example.test/api/v1/households/${householdID}/imports/backup-v5/preview`,
    );
  });

  it("uses the current session for every request and exact confirm headers", async () => {
    const body = new Blob(["raw"]);
    const sessions = sessionProvider(["preview-session", "confirm-session"]);
    const authorizations: string[] = [];
    const fetcher = vi.fn<typeof fetch>(async (_input, init) => {
      const headers = new Headers(init?.headers);
      authorizations.push(headers.get("Authorization") ?? "");
      if (authorizations.length === 1) return jsonResponse(previewPayload, 200);
      expect(init?.body).toBe(body);
      expect(headers.get("Import-Budget-Month")).toBe("2026-07-01");
      expect(headers.get("Import-Preview-Token")).toBe(previewToken);
      expect(headers.get("Idempotency-Key")).toBe(idempotencyKey);
      expect(headers.has("X-Request-ID")).toBe(false);
      expect(headers.has("Content-Encoding")).toBe(false);
      return jsonResponse(confirmPayload, 201);
    });
    const api = new BackupImportAPI(new APIClient({ apiBaseUrl: "", sessionProvider: sessions, fetcher }));

    await api.preview(householdID, body, "2026-07-01");
    await expect(api.confirm(householdID, body, "2026-07-01", previewToken, idempotencyKey)).resolves.toEqual({
      response: confirmPayload,
      replayed: false,
    });
    expect(authorizations).toEqual(["Bearer preview-session", "Bearer confirm-session"]);
  });

  it("accepts only the exact first/replay status and header combinations", async () => {
    const responses = [
      jsonResponse(confirmPayload, 200, { "Idempotency-Replayed": "true" }),
      jsonResponse(confirmPayload, 200),
      jsonResponse(confirmPayload, 201, { "Idempotency-Replayed": "true" }),
      jsonResponse(confirmPayload, 200, { "Idempotency-Replayed": "false" }),
    ];
    const api = new BackupImportAPI(new APIClient({
      apiBaseUrl: "",
      sessionProvider: sessionProvider(["one", "two", "three", "four"]),
      fetcher: vi.fn(async () => responses.shift() ?? jsonResponse({}, 500)),
    }));
    const body = new Blob(["raw"]);

    await expect(api.confirm(householdID, body, "2026-07-01", previewToken, idempotencyKey)).resolves.toMatchObject({ replayed: true });
    await expect(api.confirm(householdID, body, "2026-07-01", previewToken, idempotencyKey)).rejects.toMatchObject({ kind: "invalid_response" });
    await expect(api.confirm(householdID, body, "2026-07-01", previewToken, idempotencyKey)).rejects.toMatchObject({ kind: "invalid_response" });
    await expect(api.confirm(householdID, body, "2026-07-01", previewToken, idempotencyKey)).rejects.toMatchObject({ kind: "invalid_response" });
  });

  it.each([
    "text/plain",
    "application/jsonp",
    "application/json; foo=bar",
    "application/json; charset=utf-8; foo=bar",
  ])("rejects noncanonical response Content-Type %s", async (contentType) => {
    const api = new BackupImportAPI(new APIClient({
      apiBaseUrl: "",
      sessionProvider: sessionProvider(),
      fetcher: vi.fn(async () => new Response(JSON.stringify(previewPayload), {
        status: 200,
        headers: { "Content-Type": contentType },
      })),
    }));
    await expect(api.preview(householdID, new Blob(["raw"]), "2026-07-01")).rejects.toMatchObject({ kind: "invalid_response" });
  });

  it("uses the bounded import timeout and redacts token/key/raw server details", async () => {
    vi.useFakeTimers();
    const consoleLog = vi.spyOn(console, "log").mockImplementation(() => undefined);
    const fetcher = vi.fn<typeof fetch>((_input, init) => new Promise<Response>((_resolve, reject) => {
      init?.signal?.addEventListener("abort", () => reject(new DOMException("aborted", "AbortError")), { once: true });
    }));
    const api = new BackupImportAPI(new APIClient({ apiBaseUrl: "", sessionProvider: sessionProvider(), fetcher }));
    const promise = api.confirm(
      householdID,
      new Blob(["raw-backup-secret"]),
      "2026-07-01",
      previewToken,
      idempotencyKey,
    ).catch((error: unknown) => error);

    await vi.advanceTimersByTimeAsync(backupImportTimeoutMilliseconds);
    const error = await promise;
    expect(error).toBeInstanceOf(APIError);
    expect(error).toMatchObject({ kind: "timeout" });
    for (const secret of [previewToken, idempotencyKey, "raw-backup-secret"]) {
      expect(String(error)).not.toContain(secret);
      expect((error as APIError).code ?? "").not.toContain(secret);
    }
    expect(consoleLog).not.toHaveBeenCalled();
  });

  it.each([64_999, 120_001])("rejects an import timeout outside the hard bound: %d", async (timeoutMilliseconds) => {
    const fetcher = vi.fn<typeof fetch>();
    const client = new APIClient({ apiBaseUrl: "", sessionProvider: sessionProvider(), fetcher });
    await expect(client.requestBackupImportJSON(
      "/api/v1/import",
      parseBackupPreview,
      { body: new Blob(["raw"]), budgetMonth: "2026-07-01", timeoutMilliseconds },
    )).rejects.toMatchObject({ kind: "invalid_response" });
    expect(fetcher).not.toHaveBeenCalled();
  });
});
