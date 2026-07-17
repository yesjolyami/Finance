import { describe, expect, it } from "vitest";

import {
  canManageDirectories,
  centsToRubles,
  financeErrorMessage,
  formatMoney,
  maximumMoneyCents,
  runeLength,
  rublesToCents,
  transactionPayloadKey,
  validateTransactionDraft,
} from "./financeModel";

const baseDraft = {
  type: "expense" as const,
  accountId: "33000000-0000-4000-8000-000000000001",
  toAccountId: "",
  categoryId: "44000000-0000-4000-8000-000000000001",
  amountRubles: "123,45",
  eventDate: "2026-07-15",
  note: "  Продукты  ",
  isBalanceAdjustment: false,
};

describe("finance form model", () => {
  it("converts money without JavaScript Number precision", () => {
    expect(rublesToCents("123,45")).toBe("12345");
    expect(maximumMoneyCents).toBe(9_000_000_000_000_000n);
    expect(rublesToCents("90000000000000.00")).toBe("9000000000000000");
    expect(rublesToCents("90000000000000.01")).toBeNull();
    expect(rublesToCents("92233720368547758.07")).toBeNull();
    expect(rublesToCents("0")).toBeNull();
    expect(rublesToCents("01.00")).toBeNull();
    expect(centsToRubles("-12345")).toBe("-123.45");
    expect(formatMoney("123456789")).toBe("1 234 567,89 ₽");
  });

  it("enforces income/expense/transfer shapes", () => {
    expect(validateTransactionDraft(baseDraft)).toEqual({
      input: {
        type: "expense",
        accountId: baseDraft.accountId,
        toAccountId: null,
        categoryId: baseDraft.categoryId,
        amountCents: "12345",
        eventDate: "2026-07-15",
        note: "Продукты",
        isBalanceAdjustment: false,
      },
      error: null,
    });
    expect(validateTransactionDraft({ ...baseDraft, type: "transfer", categoryId: "", toAccountId: baseDraft.accountId }).error).toContain("отличаться");
    expect(validateTransactionDraft({ ...baseDraft, type: "income", categoryId: "" }).error).toContain("категорию");
  });

  it("counts Unicode code points for notes and directory names", () => {
    const thousandEmoji = "🙂".repeat(1_000);
    expect(thousandEmoji.length).toBe(2_000);
    expect(runeLength(thousandEmoji)).toBe(1_000);
    expect(runeLength("🙂".repeat(120))).toBe(120);
    expect(validateTransactionDraft({ ...baseDraft, note: thousandEmoji }).input?.note).toBe(thousandEmoji);
    expect(validateTransactionDraft({ ...baseDraft, note: `${thousandEmoji}🙂` }).error).toContain("1000");
  });

  it("exposes directory controls only to owner/admin", () => {
    expect(canManageDirectories("owner")).toBe(true);
    expect(canManageDirectories("admin")).toBe(true);
    expect(canManageDirectories("member")).toBe(false);
  });

  it("keeps deterministic payload identity and safe conflict messages", () => {
    const input = validateTransactionDraft(baseDraft).input;
    expect(input).not.toBeNull();
    expect(transactionPayloadKey(input!)).toBe(transactionPayloadKey({ ...input! }));
    expect(financeErrorMessage(409, "version_conflict")).toContain("другой сессии");
    expect(financeErrorMessage(403, "forbidden")).not.toContain("SQL");
  });
});
