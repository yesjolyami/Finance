"use strict";

const assert = require("node:assert");
const fs = require("node:fs");
const path = require("node:path");
const vm = require("node:vm");

const appPath = path.join(__dirname, "..", "app.js");
const source = fs.readFileSync(appPath, "utf8");
const regressionChecks = `
state.accounts = [{ id: "account-a" }, { id: "account-b" }];
state.transactions = [
  { id: "income-a", type: "income", accountId: "account-a", toAccountId: null,
    amountCents: 100000, date: "2026-01-05", isBalanceAdjustment: false },
  { id: "expense-a", type: "expense", accountId: "account-a", toAccountId: null,
    amountCents: 30000, date: "2026-01-06", isBalanceAdjustment: false },
  { id: "transfer", type: "transfer", accountId: "account-a", toAccountId: "account-b",
    amountCents: 20000, date: "2026-01-07", isBalanceAdjustment: false },
  { id: "income-b", type: "income", accountId: "account-b", toAccountId: null,
    amountCents: 5000, date: "2026-01-08", isBalanceAdjustment: false },
  { id: "expense-b", type: "expense", accountId: "account-b", toAccountId: null,
    amountCents: 1000, date: "2026-01-09", isBalanceAdjustment: false },
  { id: "adjustment-a", type: "expense", accountId: "account-a", toAccountId: null,
    amountCents: 500, date: "2026-01-10", isBalanceAdjustment: true },
  { id: "future", type: "income", accountId: "account-a", toAccountId: null,
    amountCents: 999999, date: "2026-02-01", isBalanceAdjustment: false },
];

const january = transactionsForMonth("2026-01");
assert.strictEqual(january.length, 6);
assert.strictEqual(
  JSON.stringify(accountSummary(january)),
  JSON.stringify({ income: 105000, expense: 31000, transfers: 0, balance: 74000 })
);
assert.strictEqual(
  JSON.stringify(accountSummary(january, "account-a")),
  JSON.stringify({ income: 100000, expense: 30000, transfers: -20000, balance: 50000 })
);
assert.strictEqual(
  JSON.stringify(accountSummary(january, "account-b")),
  JSON.stringify({ income: 5000, expense: 1000, transfers: 20000, balance: 24000 })
);
assert.strictEqual(accountBalanceThroughMonth("account-a", "2026-01"), 49500);
assert.strictEqual(accountBalanceThroughMonth("account-b", "2026-01"), 24000);
assert.strictEqual(totalAccountBalanceThroughMonth("2026-01"), 73500);
assert.strictEqual(amountForAccount(state.transactions[2], "account-a"), -20000);
assert.strictEqual(amountForAccount(state.transactions[2], "account-b"), 20000);
assert.strictEqual(endOfMonthValue("2024-02"), "2024-02-29");
assert.strictEqual(parseAmountToCents("1 234,56"), 123456);
assert.strictEqual(parseBalanceToCents("-10,05"), -1005);
`;

const context = vm.createContext({
  assert,
  console,
  document: { addEventListener() {} },
  localStorage: { getItem() { return null; } },
  Intl,
  Date,
  URL,
  Blob,
  setTimeout,
  clearTimeout,
});

vm.runInContext(`${source}\n${regressionChecks}`, context, { filename: appPath });
console.log("Frontend financial calculation regressions: OK");
