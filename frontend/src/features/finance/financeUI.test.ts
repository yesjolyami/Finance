import { describe, expect, it } from "vitest";

import type { FinanceTransaction } from "../../lib/financeApi";
import { matchesDirectoryState, matchesTransactionFilter, mergeUniqueByID, reconcileByID } from "./financeUI";

const baseTransaction: FinanceTransaction = {
  id: "55000000-0000-4000-8000-000000000001",
  type: "expense",
  accountId: "33000000-0000-4000-8000-000000000001",
  toAccountId: null,
  categoryId: "44000000-0000-4000-8000-000000000001",
  amountCents: "10000",
  eventDate: "2026-07-15",
  note: "",
  isBalanceAdjustment: false,
  source: "manual",
  createdByUserId: null,
  createdAt: "2026-07-15T10:00:00Z",
  updatedAt: "2026-07-15T10:00:00Z",
  deletedAt: null,
  deletionReason: null,
  version: "1",
};

const filters = { from: "2026-07-01", to: "2026-07-31", type: "" as const, accountId: "", categoryId: "", state: "active" as const };

describe("finance collection reconciliation", () => {
  it("merges cursor pages without duplicate IDs", () => {
    expect(mergeUniqueByID([{ id: "a" }, { id: "b" }], [{ id: "b" }, { id: "c" }])).toEqual([{ id: "a" }, { id: "b" }, { id: "c" }]);
  });

  it("removes resources that leave the active state and restores immutable order", () => {
    expect(matchesDirectoryState({ archivedAt: null }, "active")).toBe(true);
    expect(matchesDirectoryState({ archivedAt: "2026-07-15T10:00:00Z" }, "active")).toBe(false);
    const older = { id: "a", createdAt: "2026-07-14T10:00:00Z" };
    const newer = { id: "b", createdAt: "2026-07-15T10:00:00Z" };
    expect(reconcileByID([older], newer, true)).toEqual([newer, older]);
    expect(reconcileByID([newer, older], newer, false)).toEqual([older]);
  });

  it("keeps transaction filters correct after edit/delete/restore races", () => {
    expect(matchesTransactionFilter(baseTransaction, filters)).toBe(true);
    expect(matchesTransactionFilter({ ...baseTransaction, eventDate: "2026-08-01" }, filters)).toBe(false);
    expect(matchesTransactionFilter({ ...baseTransaction, deletedAt: "2026-07-15T11:00:00Z" }, filters)).toBe(false);
    expect(matchesTransactionFilter({ ...baseTransaction, toAccountId: "66000000-0000-4000-8000-000000000001", type: "transfer", categoryId: null }, { ...filters, accountId: "66000000-0000-4000-8000-000000000001" })).toBe(true);
  });
});
