import { describe, expect, it, vi } from "vitest";

import { APIClient } from "./api";
import { FinanceAPI, financeParsers } from "./financeApi";

const householdID = "22000000-0000-4000-8000-000000000001";
const accountID = "33000000-0000-4000-8000-000000000001";
const categoryID = "44000000-0000-4000-8000-000000000001";

const account = {
  id: accountID,
  name: "Основной",
  color: "#5F714D",
  sortOrder: 10,
  accountType: "regular",
  bankLabel: "",
  legacyOwnerLabel: "",
  ownerUserId: null,
  currencyCode: "RUB",
  isSystem: false,
  version: "1",
  createdAt: "2026-07-15T09:00:00Z",
  updatedAt: "2026-07-15T09:00:00Z",
  archivedAt: null,
};

const category = {
  id: categoryID,
  type: "expense",
  name: "Дом",
  color: "#934952",
  sortOrder: 20,
  isSystem: false,
  version: "1",
  createdAt: "2026-07-15T09:00:00Z",
  updatedAt: "2026-07-15T09:00:00Z",
  archivedAt: null,
};

function sessionProvider() {
  return { auth: { getSession: vi.fn(async () => ({ data: { session: { access_token: "safe-test-token" } }, error: null })) } };
}

function jsonResponse(payload: unknown, status = 200, headers: Record<string, string> = {}) {
  return new Response(JSON.stringify(payload), { status, headers: { "Content-Type": "application/json", ...headers } });
}

describe("finance response parsers", () => {
  it("accepts canonical string money/version and non-null empty pages", () => {
    expect(financeParsers.accountPage({ accounts: [], nextCursor: null })).toEqual({ accounts: [], nextCursor: null });
    expect(financeParsers.account(account)).toEqual(account);
    expect(financeParsers.summary({
      from: "2026-07-01",
      to: "2026-07-31",
      householdTotalCents: "-123",
      cashFlow: { incomeCents: "500", expenseCents: "623" },
    })).not.toBeNull();
  });

  it("rejects JSON numbers, unsafe versions, invalid dates and malformed cursors", () => {
    expect(financeParsers.account({ ...account, version: 1 })).toBeNull();
    expect(financeParsers.account({ ...account, version: "9223372036854775808" })).toBeNull();
    expect(financeParsers.transaction({
      id: "55000000-0000-4000-8000-000000000001",
      type: "expense",
      accountId: accountID,
      toAccountId: null,
      categoryId: categoryID,
      amountCents: 100,
      eventDate: "2026-02-30",
      note: "",
      isBalanceAdjustment: false,
      source: "manual",
      createdByUserId: null,
      createdAt: "2026-07-15T09:00:00Z",
      updatedAt: "2026-07-15T09:00:00Z",
      deletedAt: null,
      deletionReason: null,
      version: "1",
    })).toBeNull();
    expect(financeParsers.categoryPage({ categories: [category], nextCursor: "x".repeat(513) })).toBeNull();
  });
});

describe("FinanceAPI", () => {
  it("builds bounded filters and preserves opaque cursors", async () => {
    const fetcher = vi.fn<typeof fetch>(async () => jsonResponse({ accounts: [], nextCursor: "next/page" }));
    const api = new FinanceAPI(new APIClient({ apiBaseUrl: "", sessionProvider: sessionProvider(), fetcher }));
    await expect(api.listAccounts(householdID, { state: "archived", limit: 25, cursor: "cursor+value" })).resolves.toEqual({ accounts: [], nextCursor: "next/page" });
    expect(String(fetcher.mock.calls[0]?.[0])).toBe(`/api/v1/households/${householdID}/finance/accounts?state=archived&limit=25&cursor=cursor%2Bvalue`);
  });

  it("sends stable idempotency and validates replay/ETag metadata", async () => {
    const fetcher = vi.fn<typeof fetch>(async () => jsonResponse(account, 200, { ETag: '"v1"', "Idempotency-Replayed": "true" }));
    const api = new FinanceAPI(new APIClient({ apiBaseUrl: "", sessionProvider: sessionProvider(), fetcher }));
    await expect(api.createAccount(householdID, {
      name: "Основной",
      color: "#5F714D",
      sortOrder: 10,
      accountType: "regular",
      bankLabel: "",
      legacyOwnerLabel: "",
      ownerUserId: null,
    }, "stable-key")).resolves.toEqual({ resource: account, replayed: true });
    expect(new Headers(fetcher.mock.calls[0]?.[1]?.headers).get("Idempotency-Key")).toBe("stable-key");
  });

  it("sends If-Match and strict empty action bodies", async () => {
    const fetcher = vi.fn<typeof fetch>(async () => jsonResponse({ ...account, version: "2", archivedAt: "2026-07-15T10:00:00Z" }, 200, { ETag: '"v2"' }));
    const api = new FinanceAPI(new APIClient({ apiBaseUrl: "", sessionProvider: sessionProvider(), fetcher }));
    await api.setAccountArchived(householdID, accountID, "1", true);
    const init = fetcher.mock.calls[0]?.[1];
    expect(new Headers(init?.headers).get("If-Match")).toBe('"v1"');
    expect(init?.body).toBe("{}");
  });

  it("fails closed when mutation ETag does not match the version", async () => {
    const api = new FinanceAPI(new APIClient({
      apiBaseUrl: "",
      sessionProvider: sessionProvider(),
      fetcher: vi.fn(async () => jsonResponse(account, 201, { ETag: '"v2"' })),
    }));
    await expect(api.createAccount(householdID, {
      name: "Основной",
      color: "#5F714D",
      sortOrder: 0,
      accountType: "regular",
      bankLabel: "",
      legacyOwnerLabel: "",
      ownerUserId: null,
    }, "key")).rejects.toMatchObject({ kind: "invalid_response" });
  });
});
