const state = {
  accounts: [],
  categories: [],
  transactions: [],
  goals: [],
  debts: [],
  debtPayments: [],
  accountHistoryId: null,
  accountHistoryPeriod: "all",
  editingAccountId: null,
  editingTransactionId: null,
  editingCategoryId: null,
  month: currentMonth(),
  theme: localStorage.getItem("finance-theme") || "light",
  filters: {
    account: localStorage.getItem("finance-account") || "all",
    type: "all",
    category: "all",
    query: "",
  },
};

const els = {};
const money = new Intl.NumberFormat("ru-RU", {
  style: "currency",
  currency: "RUB",
  minimumFractionDigits: 0,
  maximumFractionDigits: 2,
});
const compactMoney = new Intl.NumberFormat("ru-RU", {
  notation: "compact",
  compactDisplay: "short",
  maximumFractionDigits: 1,
});
const dateFormat = new Intl.DateTimeFormat("ru-RU", {
  day: "2-digit",
  month: "short",
  year: "numeric",
});

document.addEventListener("DOMContentLoaded", init);

async function init() {
  cacheElements();
  applyTheme(state.theme);
  setDefaultDates();
  bindEvents();
  await refreshData();
}

function cacheElements() {
  els.monthFilter = document.querySelector("#monthFilter");
  els.transactionForm = document.querySelector("#transactionForm");
  els.transactionSubmitButton = document.querySelector("#transactionSubmitButton");
  els.cancelTransactionEdit = document.querySelector("#cancelTransactionEdit");
  els.categoryForm = document.querySelector("#categoryForm");
  els.categorySubmitButton = document.querySelector("#categorySubmitButton");
  els.cancelCategoryEdit = document.querySelector("#cancelCategoryEdit");
  els.balanceForm = document.querySelector("#balanceForm");
  els.balanceAccountSelect = document.querySelector("#balanceAccountSelect");
  els.targetBalanceInput = document.querySelector("#targetBalanceInput");
  els.balanceDateInput = document.querySelector("#balanceDateInput");
  els.balanceNoteInput = document.querySelector("#balanceNoteInput");
  els.goalForm = document.querySelector("#goalForm");
  els.goalName = document.querySelector("#goalName");
  els.goalTarget = document.querySelector("#goalTarget");
  els.goalSaved = document.querySelector("#goalSaved");
  els.goalDate = document.querySelector("#goalDate");
  els.goalColor = document.querySelector("#goalColor");
  els.goalList = document.querySelector("#goalList");
  els.debtForm = document.querySelector("#debtForm");
  els.debtPerson = document.querySelector("#debtPerson");
  els.debtDirection = document.querySelector("#debtDirection");
  els.debtAmount = document.querySelector("#debtAmount");
  els.debtDueDate = document.querySelector("#debtDueDate");
  els.debtNote = document.querySelector("#debtNote");
  els.debtList = document.querySelector("#debtList");
  els.debtSummary = document.querySelector("#debtSummary");
  els.accountSelect = document.querySelector("#accountSelect");
  els.accountSelectLabel = document.querySelector("#accountSelectLabel");
  els.toAccountSelect = document.querySelector("#toAccountSelect");
  els.toAccountField = document.querySelector("#toAccountField");
  els.accountFilter = document.querySelector("#accountFilter");
  els.accountGrid = document.querySelector("#accountGrid");
  els.accountScopeLabel = document.querySelector("#accountScopeLabel");
  els.selectedAccountHistory = document.querySelector("#selectedAccountHistory");
  els.totalAccountsBalance = document.querySelector("#totalAccountsBalance");
  els.totalSavingsBalance = document.querySelector("#totalSavingsBalance");
  els.savingsAccountsHint = document.querySelector("#savingsAccountsHint");
  els.bankTotals = document.querySelector("#bankTotals");
  els.accountSettings = document.querySelector("#accountSettings");
  els.accountForm = document.querySelector("#accountForm");
  els.accountName = document.querySelector("#accountName");
  els.accountKind = document.querySelector("#accountKind");
  els.accountBank = document.querySelector("#accountBank");
  els.accountOwner = document.querySelector("#accountOwner");
  els.accountColor = document.querySelector("#accountColor");
  els.accountInitialBalance = document.querySelector("#accountInitialBalance");
  els.accountInitialBalanceField = document.querySelector("#accountInitialBalanceField");
  els.accountSubmitButton = document.querySelector("#accountSubmitButton");
  els.cancelAccountEdit = document.querySelector("#cancelAccountEdit");
  els.accountManagementList = document.querySelector("#accountManagementList");
  els.accountHistoryDialog = document.querySelector("#accountHistoryDialog");
  els.accountHistorySwatch = document.querySelector("#accountHistorySwatch");
  els.accountHistoryTitle = document.querySelector("#accountHistoryTitle");
  els.accountHistoryMeta = document.querySelector("#accountHistoryMeta");
  els.accountHistoryBalanceLabel = document.querySelector("#accountHistoryBalanceLabel");
  els.accountHistoryBalance = document.querySelector("#accountHistoryBalance");
  els.accountHistoryPeriod = document.querySelector("#accountHistoryPeriod");
  els.accountHistoryIncome = document.querySelector("#accountHistoryIncome");
  els.accountHistoryExpense = document.querySelector("#accountHistoryExpense");
  els.accountHistoryCount = document.querySelector("#accountHistoryCount");
  els.accountHistoryList = document.querySelector("#accountHistoryList");
  els.closeAccountHistory = document.querySelector("#closeAccountHistory");
  els.historySetBalance = document.querySelector("#historySetBalance");
  els.categorySelect = document.querySelector("#categorySelect");
  els.categoryField = document.querySelector("#categoryField");
  els.categoryFilter = document.querySelector("#categoryFilter");
  els.categoryList = document.querySelector("#categoryList");
  els.categoryType = document.querySelector("#categoryType");
  els.categoryBudget = document.querySelector("#categoryBudget");
  els.categoryColor = document.querySelector("#categoryColor");
  els.transactionList = document.querySelector("#transactionList");
  els.typeFilter = document.querySelector("#typeFilter");
  els.searchInput = document.querySelector("#searchInput");
  els.exportButton = document.querySelector("#exportButton");
  els.exportFormat = document.querySelector("#exportFormat");
  els.themeToggle = document.querySelector("#themeToggle");
  els.importFile = document.querySelector("#importFile");
  els.quickCategoryForm = document.querySelector("#quickCategoryForm");
  els.quickCategoryName = document.querySelector("#quickCategoryName");
  els.quickCategoryColor = document.querySelector("#quickCategoryColor");
  els.quickCategoryBudget = document.querySelector("#quickCategoryBudget");
  els.quickBudgetField = document.querySelector("#quickBudgetField");
  els.quickCategoryTypeBadge = document.querySelector("#quickCategoryTypeBadge");
  els.trendChart = document.querySelector("#trendChart");
  els.insightList = document.querySelector("#insightList");
  els.accountReportList = document.querySelector("#accountReportList");
  els.categoryBreakdown = document.querySelector("#categoryBreakdown");
  els.budgetBreakdown = document.querySelector("#budgetBreakdown");
  els.toast = document.querySelector("#toast");

  els.summaryBalance = document.querySelector("#summaryBalance");
  els.summaryIncome = document.querySelector("#summaryIncome");
  els.summaryExpense = document.querySelector("#summaryExpense");
  els.summaryBudget = document.querySelector("#summaryBudget");
  els.balanceHint = document.querySelector("#balanceHint");
  els.expenseHint = document.querySelector("#expenseHint");
  els.budgetHint = document.querySelector("#budgetHint");
  els.savingsRate = document.querySelector("#savingsRate");
}

function setDefaultDates() {
  els.monthFilter.value = state.month;
  document.querySelector("#dateInput").value = todayInputValue();
  els.balanceDateInput.value = todayInputValue();
}

function bindEvents() {
  els.monthFilter.addEventListener("change", () => {
    state.month = els.monthFilter.value || currentMonth();
    render();
  });

  els.themeToggle.addEventListener("click", () => {
    applyTheme(state.theme === "dark" ? "light" : "dark");
  });

  els.transactionForm.addEventListener("submit", handleTransactionSubmit);
  els.accountForm.addEventListener("submit", handleAccountSubmit);
  els.cancelAccountEdit.addEventListener("click", resetAccountForm);
  els.cancelTransactionEdit.addEventListener("click", resetTransactionForm);
  els.categoryForm.addEventListener("submit", handleCategorySubmit);
  els.cancelCategoryEdit.addEventListener("click", resetCategoryForm);
  els.quickCategoryForm.addEventListener("submit", handleQuickCategorySubmit);
  els.balanceForm.addEventListener("submit", handleBalanceSubmit);
  els.goalForm.addEventListener("submit", handleGoalSubmit);
  els.debtForm.addEventListener("submit", handleDebtSubmit);

  els.transactionForm.querySelectorAll("input[name='type']").forEach((input) => {
    input.addEventListener("change", () => {
      syncTransactionMode();
    });
  });
  els.accountSelect.addEventListener("change", () => {
    if (getSelectedTransactionType() === "transfer") {
      updateToAccountOptions();
    }
  });

  els.categoryType.addEventListener("change", () => {
    const isIncome = els.categoryType.value === "income";
    els.categoryBudget.disabled = isIncome;
    els.categoryBudget.placeholder = isIncome ? "Не нужен" : "0";
  });

  els.typeFilter.addEventListener("change", () => {
    state.filters.type = els.typeFilter.value;
    renderTransactions();
  });

  els.accountFilter.addEventListener("change", () => {
    setAccountFilter(els.accountFilter.value);
  });

  els.selectedAccountHistory.addEventListener("click", () => {
    if (state.filters.account !== "all") {
      openAccountHistory(state.filters.account);
    }
  });

  els.accountGrid.addEventListener("click", (event) => {
    const button = event.target.closest("[data-account-filter]");
    if (!button) return;
    setAccountFilter(button.dataset.accountFilter);
  });
  els.accountManagementList.addEventListener("click", async (event) => {
    const historyButton = event.target.closest("[data-history-account]");
    if (historyButton) {
      openAccountHistory(historyButton.dataset.historyAccount);
      return;
    }
    const balanceButton = event.target.closest("[data-balance-account]");
    if (balanceButton) {
      openBalanceEditor(balanceButton.dataset.balanceAccount);
      return;
    }
    const editButton = event.target.closest("[data-edit-account]");
    if (editButton) {
      startAccountEdit(editButton.dataset.editAccount);
      return;
    }
    const deleteButton = event.target.closest("[data-delete-account]");
    if (deleteButton) {
      await deleteAccount(deleteButton.dataset.deleteAccount);
    }
  });
  els.accountHistoryPeriod.addEventListener("change", () => {
    state.accountHistoryPeriod = els.accountHistoryPeriod.value;
    renderAccountHistory();
  });
  els.closeAccountHistory.addEventListener("click", closeAccountHistory);
  els.historySetBalance.addEventListener("click", () => {
    const accountId = state.accountHistoryId;
    closeAccountHistory();
    if (accountId) openBalanceEditor(accountId);
  });
  els.accountHistoryDialog.addEventListener("click", (event) => {
    if (event.target === els.accountHistoryDialog) closeAccountHistory();
  });
  els.accountHistoryDialog.addEventListener("close", () => {
    state.accountHistoryId = null;
    state.accountHistoryPeriod = "all";
  });
  els.accountReportList.addEventListener("click", (event) => {
    const button = event.target.closest("[data-account-report]");
    if (!button) return;
    setAccountFilter(button.dataset.accountReport);
  });

  els.categoryFilter.addEventListener("change", () => {
    state.filters.category = els.categoryFilter.value;
    renderTransactions();
  });

  els.searchInput.addEventListener("input", () => {
    state.filters.query = els.searchInput.value.trim().toLowerCase();
    renderTransactions();
  });

  els.transactionList.addEventListener("click", async (event) => {
    const editButton = event.target.closest("[data-edit-transaction]");
    if (editButton) {
      startTransactionEdit(editButton.dataset.editTransaction);
      return;
    }
    const deleteButton = event.target.closest("[data-delete-transaction]");
    if (!deleteButton) return;
    await deleteTransaction(deleteButton.dataset.deleteTransaction);
  });

  els.categoryList.addEventListener("click", async (event) => {
    const editButton = event.target.closest("[data-edit-category]");
    if (editButton) {
      startCategoryEdit(editButton.dataset.editCategory);
      return;
    }
    const deleteButton = event.target.closest("[data-delete-category]");
    if (!deleteButton) return;
    await deleteCategory(deleteButton.dataset.deleteCategory);
  });

  els.goalList.addEventListener("submit", handleGoalActionSubmit);
  els.goalList.addEventListener("click", async (event) => {
    const archiveButton = event.target.closest("[data-archive-goal]");
    if (archiveButton) {
      await archiveGoal(archiveButton.dataset.archiveGoal);
      return;
    }
    const deleteButton = event.target.closest("[data-delete-goal]");
    if (deleteButton) {
      await deleteGoal(deleteButton.dataset.deleteGoal);
    }
  });

  els.debtList.addEventListener("submit", handleDebtPaymentSubmit);
  els.debtList.addEventListener("click", async (event) => {
    const archiveButton = event.target.closest("[data-archive-debt]");
    if (archiveButton) {
      await archiveDebt(archiveButton.dataset.archiveDebt);
      return;
    }
    const deleteButton = event.target.closest("[data-delete-debt]");
    if (deleteButton) {
      await deleteDebt(deleteButton.dataset.deleteDebt);
    }
  });

  els.exportButton.addEventListener("click", handleExport);
  els.importFile.addEventListener("change", importBackup);
  window.addEventListener("resize", debounce(renderTrendChart, 120));
}

async function refreshData() {
  try {
    const [accountsPayload, categoriesPayload, transactionsPayload, goalsPayload, debtsPayload] = await Promise.all([
      api("/api/accounts"),
      api("/api/categories"),
      api("/api/transactions"),
      api("/api/goals"),
      api("/api/debts"),
    ]);
    state.accounts = accountsPayload.accounts;
    state.categories = categoriesPayload.categories;
    state.transactions = transactionsPayload.transactions;
    state.goals = goalsPayload.goals;
    state.debts = debtsPayload.debts;
    state.debtPayments = debtsPayload.payments;
    render();
  } catch (error) {
    showToast(error.message || "Не удалось загрузить данные", "error");
  }
}

async function api(path, options = {}) {
  const response = await fetch(path, {
    headers: { "Content-Type": "application/json", ...(options.headers || {}) },
    ...options,
  });
  const payload = await response.json().catch(() => ({}));
  if (!response.ok) {
    throw new Error(payload.error || "Ошибка запроса");
  }
  return payload;
}

async function handleAccountSubmit(event) {
  event.preventDefault();
  const form = new FormData(els.accountForm);
  const payload = {
    name: String(form.get("name") || "").trim(),
    kind: form.get("kind"),
    bank: String(form.get("bank") || "").trim(),
    owner: String(form.get("owner") || "").trim(),
    color: form.get("color"),
  };

  try {
    if (state.editingAccountId) {
      await api(`/api/accounts/${encodeURIComponent(state.editingAccountId)}`, {
        method: "PATCH",
        body: JSON.stringify(payload),
      });
      resetAccountForm();
      await refreshData();
      showToast("Счет обновлен");
      return;
    }

    const result = await api("/api/accounts", {
      method: "POST",
      body: JSON.stringify(payload),
    });
    const initialBalanceValue = String(form.get("initialBalance") || "").trim();
    if (initialBalanceValue) {
      const balanceCents = parseBalanceToCents(initialBalanceValue);
      if (!Number.isFinite(balanceCents)) {
        throw new Error("Счет создан, но начальный баланс указан неверно");
      }
      const date = todayInputValue();
      await api("/api/balance-adjustment", {
        method: "POST",
        body: JSON.stringify({
          accountId: result.account.id,
          month: date.slice(0, 7),
          balanceCents,
          date,
          note: "Начальный баланс",
        }),
      });
    }
    resetAccountForm();
    await refreshData();
    showToast(payload.kind === "savings" ? "Накопительный счет добавлен" : "Счет добавлен");
  } catch (error) {
    await refreshData();
    showToast(error.message, "error");
  }
}

function startAccountEdit(id) {
  const account = findAccount(id);
  if (!account) return;
  state.editingAccountId = id;
  els.accountSettings.open = true;
  els.accountName.value = account.name;
  els.accountKind.value = account.kind || "regular";
  els.accountBank.value = account.bank || "";
  els.accountOwner.value = account.owner || "";
  els.accountColor.value = account.color;
  els.accountInitialBalanceField.hidden = true;
  els.accountInitialBalance.disabled = true;
  els.accountSubmitButton.textContent = "Сохранить счет";
  els.cancelAccountEdit.hidden = false;
  els.accountName.focus();
}

function resetAccountForm() {
  state.editingAccountId = null;
  els.accountForm.reset();
  els.accountColor.value = "#5d704d";
  els.accountKind.value = "regular";
  els.accountInitialBalanceField.hidden = false;
  els.accountInitialBalance.disabled = false;
  els.accountSubmitButton.textContent = "Добавить счет";
  els.cancelAccountEdit.hidden = true;
}

async function deleteAccount(id) {
  const account = findAccount(id);
  if (!account || !window.confirm(`Удалить счет «${account.name}»?`)) return;
  try {
    await api(`/api/accounts/${encodeURIComponent(id)}`, { method: "DELETE" });
    if (state.editingAccountId === id) resetAccountForm();
    await refreshData();
    showToast("Счет удален");
  } catch (error) {
    showToast(error.message, "error");
  }
}

function openBalanceEditor(id) {
  updateBalanceAccountOptions(id);
  els.balanceForm.scrollIntoView({ behavior: "smooth", block: "center" });
  window.setTimeout(() => els.targetBalanceInput.focus(), 320);
}

function openAccountHistory(id) {
  const account = findAccount(id);
  if (!account) return;
  state.accountHistoryId = id;
  state.accountHistoryPeriod = "all";
  updateAccountHistoryPeriodOptions(id);
  renderAccountHistory();
  if (typeof els.accountHistoryDialog.showModal === "function") {
    if (!els.accountHistoryDialog.open) els.accountHistoryDialog.showModal();
  } else {
    els.accountHistoryDialog.setAttribute("open", "");
  }
}

function closeAccountHistory() {
  if (typeof els.accountHistoryDialog.close === "function" && els.accountHistoryDialog.open) {
    els.accountHistoryDialog.close();
    return;
  }
  els.accountHistoryDialog.removeAttribute("open");
  state.accountHistoryId = null;
  state.accountHistoryPeriod = "all";
}

function updateAccountHistoryPeriodOptions(accountId) {
  const months = Array.from(
    new Set(transactionsForAccount(accountId).map((transaction) => transaction.date.slice(0, 7)))
  ).sort((a, b) => b.localeCompare(a));
  const options = [new Option("За всё время", "all")].concat(
    months.map((month) => new Option(formatMonthLong(month), month))
  );
  els.accountHistoryPeriod.replaceChildren(...options);
  els.accountHistoryPeriod.value = state.accountHistoryPeriod;
}

async function handleTransactionSubmit(event) {
  event.preventDefault();
  const form = new FormData(els.transactionForm);
  const type = form.get("type");
  const accountId = form.get("accountId");
  const toAccountId = form.get("toAccountId");
  const amountCents = parseAmountToCents(form.get("amount"));
  const categoryId = type === "transfer" ? null : form.get("categoryId");
  const date = form.get("date");
  const note = String(form.get("note") || "").trim();

  if (!amountCents) {
    showToast("Введите сумму больше нуля", "error");
    return;
  }

  try {
    const payload = { type, accountId, amountCents, date, note };
    if (type === "transfer") {
      payload.toAccountId = toAccountId;
    } else {
      payload.categoryId = categoryId;
    }
    const wasEditing = Boolean(state.editingTransactionId);
    const path = state.editingTransactionId
      ? `/api/transactions/${encodeURIComponent(state.editingTransactionId)}`
      : "/api/transactions";
    await api(path, {
      method: state.editingTransactionId ? "PATCH" : "POST",
      body: JSON.stringify(payload),
    });
    resetTransactionForm();
    updateAccountOptions(accountId);
    syncTransactionMode();
    await refreshData();
    showToast(wasEditing ? "Операция обновлена" : type === "transfer" ? "Перевод добавлен" : "Операция добавлена");
  } catch (error) {
    showToast(error.message, "error");
  }
}

async function handleCategorySubmit(event) {
  event.preventDefault();
  const form = new FormData(els.categoryForm);
  const type = form.get("type");
  const payload = {
    type,
    name: String(form.get("name") || "").trim(),
    color: form.get("color"),
    budgetCents: type === "expense" ? parseAmountToCents(form.get("budget")) || 0 : 0,
  };

  try {
    if (state.editingCategoryId) {
      await api(`/api/categories/${encodeURIComponent(state.editingCategoryId)}`, {
        method: "PATCH",
        body: JSON.stringify(payload),
      });
      resetCategoryForm();
      await refreshData();
      showToast("Категория обновлена");
      return;
    }
    await createCategory(payload);
    resetCategoryForm();
    syncQuickCategoryState();
    showToast("Категория создана");
  } catch (error) {
    showToast(error.message, "error");
  }
}

async function handleBalanceSubmit(event) {
  event.preventDefault();
  const form = new FormData(els.balanceForm);
  const balanceCents = parseBalanceToCents(form.get("balance"));
  if (!Number.isFinite(balanceCents)) {
    showToast("Введите баланс числом", "error");
    return;
  }
  try {
    const date = form.get("date");
    const result = await api("/api/balance-adjustment", {
      method: "POST",
      body: JSON.stringify({
        accountId: form.get("accountId"),
        month: String(date).slice(0, 7),
        balanceCents,
        date,
        note: String(form.get("note") || "").trim(),
      }),
    });
    els.balanceForm.reset();
    els.balanceDateInput.value = todayInputValue();
    updateBalanceAccountOptions();
    await refreshData();
    showToast(result.skipped ? "Баланс уже совпадает" : "Баланс скорректирован");
  } catch (error) {
    showToast(error.message, "error");
  }
}

async function handleGoalSubmit(event) {
  event.preventDefault();
  const form = new FormData(els.goalForm);
  const targetCents = parseAmountToCents(form.get("target"));
  if (!targetCents) {
    showToast("Введите сумму цели", "error");
    return;
  }
  try {
    await api("/api/goals", {
      method: "POST",
      body: JSON.stringify({
        name: String(form.get("name") || "").trim(),
        targetCents,
        savedCents: parseAmountToCents(form.get("saved")) || 0,
        targetDate: form.get("targetDate") || "",
        color: form.get("color"),
      }),
    });
    els.goalForm.reset();
    els.goalColor.value = "#5d704d";
    await refreshData();
    showToast("Цель добавлена");
  } catch (error) {
    showToast(error.message, "error");
  }
}

async function handleGoalActionSubmit(event) {
  const form = event.target.closest("[data-goal-form]");
  if (!form) return;
  event.preventDefault();
  const goal = state.goals.find((item) => item.id === form.dataset.goalForm);
  if (!goal) return;
  const amount = parseAmountToCents(new FormData(form).get("amount"));
  if (!amount) {
    showToast("Введите сумму пополнения", "error");
    return;
  }
  try {
    await api(`/api/goals/${encodeURIComponent(goal.id)}`, {
      method: "PATCH",
      body: JSON.stringify({ savedCents: goal.savedCents + amount }),
    });
    await refreshData();
    showToast("Цель пополнена");
  } catch (error) {
    showToast(error.message, "error");
  }
}

async function archiveGoal(id) {
  const goal = state.goals.find((item) => item.id === id);
  if (!goal) return;
  try {
    await api(`/api/goals/${encodeURIComponent(id)}`, {
      method: "PATCH",
      body: JSON.stringify({ archived: !goal.archived }),
    });
    await refreshData();
    showToast(goal.archived ? "Цель возвращена" : "Цель закрыта");
  } catch (error) {
    showToast(error.message, "error");
  }
}

async function deleteGoal(id) {
  if (!window.confirm("Удалить цель?")) return;
  try {
    await api(`/api/goals/${encodeURIComponent(id)}`, { method: "DELETE" });
    await refreshData();
    showToast("Цель удалена");
  } catch (error) {
    showToast(error.message, "error");
  }
}

async function handleDebtSubmit(event) {
  event.preventDefault();
  const form = new FormData(els.debtForm);
  const amountCents = parseAmountToCents(form.get("amount"));
  if (!amountCents) {
    showToast("Введите сумму долга", "error");
    return;
  }
  try {
    await api("/api/debts", {
      method: "POST",
      body: JSON.stringify({
        person: String(form.get("person") || "").trim(),
        direction: form.get("direction"),
        amountCents,
        dueDate: form.get("dueDate") || "",
        note: String(form.get("note") || "").trim(),
      }),
    });
    els.debtForm.reset();
    await refreshData();
    showToast("Долг добавлен");
  } catch (error) {
    showToast(error.message, "error");
  }
}

async function handleDebtPaymentSubmit(event) {
  const form = event.target.closest("[data-debt-payment-form]");
  if (!form) return;
  event.preventDefault();
  const amountCents = parseAmountToCents(new FormData(form).get("amount"));
  if (!amountCents) {
    showToast("Введите сумму возврата", "error");
    return;
  }
  try {
    await api("/api/debt-payments", {
      method: "POST",
      body: JSON.stringify({
        debtId: form.dataset.debtPaymentForm,
        amountCents,
        date: todayInputValue(),
        note: "Возврат",
      }),
    });
    await refreshData();
    showToast("Возврат записан");
  } catch (error) {
    showToast(error.message, "error");
  }
}

async function archiveDebt(id) {
  const debt = state.debts.find((item) => item.id === id);
  if (!debt) return;
  try {
    await api(`/api/debts/${encodeURIComponent(id)}`, {
      method: "PATCH",
      body: JSON.stringify({ archived: !debt.archived }),
    });
    await refreshData();
    showToast(debt.archived ? "Долг возвращен" : "Долг закрыт");
  } catch (error) {
    showToast(error.message, "error");
  }
}

async function deleteDebt(id) {
  if (!window.confirm("Удалить долг и возвраты по нему?")) return;
  try {
    await api(`/api/debts/${encodeURIComponent(id)}`, { method: "DELETE" });
    await refreshData();
    showToast("Долг удален");
  } catch (error) {
    showToast(error.message, "error");
  }
}

async function handleQuickCategorySubmit(event) {
  event.preventDefault();
  const type = getSelectedTransactionType();
  const form = new FormData(els.quickCategoryForm);
  const payload = {
    type,
    name: String(form.get("name") || "").trim(),
    color: form.get("color"),
    budgetCents: type === "expense" ? parseAmountToCents(form.get("budget")) || 0 : 0,
  };

  try {
    const category = await createCategory(payload);
    els.quickCategoryForm.reset();
    els.quickCategoryColor.value = "#5d704d";
    syncQuickCategoryState();
    updateTransactionCategoryOptions(type);
    els.categorySelect.value = category.id;
    document.querySelector("#noteInput").focus();
    showToast("Категория добавлена и выбрана");
  } catch (error) {
    showToast(error.message, "error");
  }
}

async function createCategory(payload) {
  if (!payload.name) {
    throw new Error("Введите название категории");
  }
  const result = await api("/api/categories", {
    method: "POST",
    body: JSON.stringify(payload),
  });
  await refreshData();
  return result.category;
}

async function deleteTransaction(id) {
  if (!window.confirm("Удалить эту операцию?")) return;
  try {
    await api(`/api/transactions/${encodeURIComponent(id)}`, { method: "DELETE" });
    await refreshData();
    showToast("Операция удалена");
  } catch (error) {
    showToast(error.message, "error");
  }
}

async function deleteCategory(id) {
  if (
    !window.confirm(
      "Удалить категорию? Операции из нее будут перенесены в категорию «Другое» или ближайшую доступную."
    )
  ) {
    return;
  }
  try {
    await api(`/api/categories/${encodeURIComponent(id)}`, { method: "DELETE" });
    await refreshData();
    showToast("Категория удалена");
  } catch (error) {
    showToast(error.message, "error");
  }
}

function startTransactionEdit(id) {
  const transaction = state.transactions.find((item) => item.id === id);
  if (!transaction) return;
  state.editingTransactionId = id;
  els.transactionSubmitButton.textContent = "Сохранить";
  els.cancelTransactionEdit.hidden = false;
  els.transactionForm.querySelector(`input[name="type"][value="${transaction.type}"]`).checked = true;
  document.querySelector("#amountInput").value = formatAmountInput(transaction.amountCents);
  document.querySelector("#dateInput").value = transaction.date;
  document.querySelector("#noteInput").value = transaction.note || "";
  updateAccountOptions(transaction.accountId);
  syncTransactionMode();
  if (transaction.type === "transfer") {
    updateToAccountOptions(transaction.toAccountId);
  } else {
    updateTransactionCategoryOptions(transaction.type);
    els.categorySelect.value = transaction.categoryId;
  }
  document.querySelector("#entry").scrollIntoView({ behavior: "smooth", block: "start" });
}

function resetTransactionForm() {
  state.editingTransactionId = null;
  els.transactionSubmitButton.textContent = "Добавить";
  els.cancelTransactionEdit.hidden = true;
  els.transactionForm.reset();
  document.querySelector("input[name='type'][value='expense']").checked = true;
  document.querySelector("#dateInput").value = todayInputValue();
  syncTransactionMode();
}

function startCategoryEdit(id) {
  const category = state.categories.find((item) => item.id === id);
  if (!category) return;
  state.editingCategoryId = id;
  els.categorySubmitButton.textContent = "Сохранить";
  els.cancelCategoryEdit.hidden = false;
  document.querySelector("#categoryName").value = category.name;
  els.categoryType.value = category.type;
  els.categoryColor.value = category.color;
  els.categoryBudget.value = category.type === "expense" && category.budgetCents
    ? formatAmountInput(category.budgetCents)
    : "";
  els.categoryBudget.disabled = category.type === "income";
  els.categoryBudget.placeholder = category.type === "income" ? "Не нужен" : "0";
  document.querySelector("#categories").scrollIntoView({ behavior: "smooth", block: "start" });
}

function resetCategoryForm() {
  state.editingCategoryId = null;
  els.categorySubmitButton.textContent = "Создать категорию";
  els.cancelCategoryEdit.hidden = true;
  els.categoryForm.reset();
  els.categoryColor.value = "#5d704d";
  els.categoryBudget.disabled = false;
  els.categoryBudget.placeholder = "0";
}

async function handleExport() {
  const format = els.exportFormat.value;
  if (format === "csv") {
    exportCsv();
    return;
  }
  if (format === "png") {
    exportPngReport();
    return;
  }
  await exportJsonBackup();
}

async function exportJsonBackup() {
  try {
    const payload = await api("/api/export");
    const blob = new Blob([JSON.stringify(payload, null, 2)], {
      type: "application/json;charset=utf-8",
    });
    downloadBlob(blob, `finance-backup-${new Date().toISOString().slice(0, 10)}.json`);
    showToast("JSON-бэкап скачан");
  } catch (error) {
    showToast(error.message, "error");
  }
}

function exportCsv() {
  const rows = scopedTransactionsForMonth(state.month).sort((a, b) =>
    `${a.date}${a.createdAt}`.localeCompare(`${b.date}${b.createdAt}`)
  );
  const header = ["Дата", "Счет", "На счет", "Тип", "Категория", "Сумма", "Комментарий"];
  const csvRows = rows.map((transaction) => {
    const account = findAccount(transaction.accountId);
    const toAccount = findAccount(transaction.toAccountId);
    const category = findCategory(transaction.categoryId);
    const amount = amountForScope(transaction) / 100;
    return [
      transaction.date,
      account?.name || "Без счета",
      toAccount?.name || "",
      transactionTypeLabel(transaction.type, transaction.isBalanceAdjustment),
      category?.name || "Без категории",
      amount.toFixed(2).replace(".", ","),
      transaction.note,
    ];
  });
  const csv = "\ufeff" + [header, ...csvRows].map((row) => row.map(csvCell).join(";")).join("\n");
  downloadBlob(
    new Blob([csv], { type: "text/csv;charset=utf-8" }),
    `finance-${state.month}${accountFileSuffix()}.csv`
  );
  showToast(rows.length ? "CSV скачан" : "CSV скачан, операций за месяц нет");
}

function exportPngReport() {
  const monthTransactions = scopedTransactionsForMonth(state.month);
  const income = sumByType(monthTransactions, "income");
  const expense = sumByType(monthTransactions, "expense");
  const balance = balanceForScope(monthTransactions);
  const bgColor = getThemeColor("--bg");
  const surfaceColor = getThemeColor("--surface");
  const inkColor = getThemeColor("--ink");
  const mutedColor = getThemeColor("--muted");
  const lineColor = getThemeColor("--line");
  const incomeColor = getThemeColor("--accent");
  const expenseColor = getThemeColor("--rose");
  const expenses = monthTransactions.filter(
    (item) => item.type === "expense" && !item.isBalanceAdjustment
  );
  const totalExpense = expenses.reduce((sum, item) => sum + item.amountCents, 0);
  const accountRows = state.accounts.map((account) => ({
    account,
    summary: accountSummary(transactionsForMonth(state.month), account.id),
  }));
  const categories = groupByCategory(expenses)
    .map(({ category, amount }) => ({
      name: category?.name || "Без категории",
      color: category?.color || "#6b7280",
      amount,
      percent: totalExpense ? amount / totalExpense : 0,
    }))
    .sort((a, b) => b.amount - a.amount)
    .slice(0, 8);

  const width = 1200;
  const accountSectionHeight = accountRows.length ? 64 + accountRows.length * 46 : 90;
  const categoriesStartY = 370 + accountSectionHeight;
  const height = Math.max(860, categoriesStartY + 170 + categories.length * 54);
  const canvas = document.createElement("canvas");
  const dpr = 2;
  canvas.width = width * dpr;
  canvas.height = height * dpr;
  canvas.style.width = `${width}px`;
  canvas.style.height = `${height}px`;
  const ctx = canvas.getContext("2d");
  ctx.setTransform(dpr, 0, 0, dpr, 0, 0);

  ctx.fillStyle = bgColor;
  ctx.fillRect(0, 0, width, height);
  drawReportText(ctx, "Мои финансы", 64, 70, 22, mutedColor, 800);
  drawReportText(ctx, `Отчет за ${formatMonthLong(state.month)}`, 64, 118, 48, inkColor, 900);
  drawReportText(ctx, getAccountScopeLabel(), 64, 150, 20, mutedColor, 700);

  drawReportCard(
    ctx,
    64,
    170,
    330,
    150,
    "Баланс",
    formatMoney(balance),
    balance >= 0 ? incomeColor : expenseColor,
    { surfaceColor, lineColor, mutedColor }
  );
  drawReportCard(ctx, 434, 170, 330, 150, "Доходы", formatMoney(income), incomeColor, {
    surfaceColor,
    lineColor,
    mutedColor,
  });
  drawReportCard(ctx, 804, 170, 330, 150, "Расходы", formatMoney(expense), expenseColor, {
    surfaceColor,
    lineColor,
    mutedColor,
  });

  drawReportText(ctx, "По счетам", 64, 370, 28, inkColor, 900);
  if (!accountRows.length) {
    drawReportText(ctx, "Счетов пока нет.", 64, 418, 22, mutedColor, 600);
  } else {
    accountRows.forEach((item, index) => {
      const y = 418 + index * 46;
      ctx.fillStyle = item.account.color || incomeColor;
      roundRect(ctx, 64, y - 16, 10, 32, 5);
      ctx.fill();
      drawReportText(ctx, item.account.name, 88, y, 18, inkColor, 800);
      drawReportText(ctx, formatMoney(item.summary.income), 420, y, 18, incomeColor, 800, "right");
      drawReportText(ctx, formatMoney(item.summary.expense), 620, y, 18, expenseColor, 800, "right");
      drawReportText(ctx, formatSignedMoney(item.summary.transfers), 820, y, 18, mutedColor, 800, "right");
      drawReportText(
        ctx,
        formatMoney(item.summary.balance),
        1134,
        y,
        18,
        item.summary.balance >= 0 ? incomeColor : expenseColor,
        900,
        "right"
      );
    });
    drawReportText(ctx, "Доход", 420, 395, 14, mutedColor, 700, "right");
    drawReportText(ctx, "Расход", 620, 395, 14, mutedColor, 700, "right");
    drawReportText(ctx, "Переводы", 820, 395, 14, mutedColor, 700, "right");
    drawReportText(ctx, "Итог", 1134, 395, 14, mutedColor, 700, "right");
  }

  drawReportText(ctx, "Расходы по категориям", 64, categoriesStartY, 28, inkColor, 900);
  if (!categories.length) {
    drawReportText(
      ctx,
      "За выбранный месяц расходов пока нет.",
      64,
      categoriesStartY + 48,
      22,
      mutedColor,
      600
    );
  } else {
    categories.forEach((item, index) => {
      const y = categoriesStartY + 48 + index * 54;
      const barWidth = 760 * item.percent;
      ctx.fillStyle = lineColor;
      roundRect(ctx, 64, y + 22, 760, 12, 6);
      ctx.fill();
      ctx.fillStyle = item.color;
      roundRect(ctx, 64, y + 22, Math.max(8, barWidth), 12, 6);
      ctx.fill();
      drawReportText(ctx, item.name, 64, y, 20, inkColor, 800);
      drawReportText(ctx, formatMoney(item.amount), 1040, y, 20, inkColor, 900, "right");
    });
  }

  const generated = new Intl.DateTimeFormat("ru-RU", {
    day: "2-digit",
    month: "2-digit",
    year: "numeric",
    hour: "2-digit",
    minute: "2-digit",
  }).format(new Date());
  drawReportText(ctx, `Сформировано: ${generated}`, 64, height - 54, 18, mutedColor, 600);

  canvas.toBlob((blob) => {
    if (!blob) {
      showToast("Не удалось создать PNG", "error");
      return;
    }
    downloadBlob(blob, `finance-report-${state.month}${accountFileSuffix()}.png`);
    showToast("PNG-отчет скачан");
  }, "image/png");
}

async function importBackup(event) {
  const file = event.target.files?.[0];
  if (!file) return;
  try {
    const payload = JSON.parse(await file.text());
    if (!window.confirm("Импорт заменит текущую базу. Продолжить?")) return;
    await api("/api/import", {
      method: "POST",
      body: JSON.stringify(payload),
    });
    await refreshData();
    showToast("База импортирована");
  } catch (error) {
    showToast(error.message || "Не удалось импортировать файл", "error");
  } finally {
    event.target.value = "";
  }
}

function render() {
  updateAccountOptions();
  updateBalanceAccountOptions();
  syncTransactionMode();
  updateAccountFilterOptions();
  updateFilterCategoryOptions();
  syncQuickCategoryState();
  renderAccounts();
  renderAccountOverview();
  renderAccountManagement();
  renderSummary();
  renderGoals();
  renderDebts();
  renderInsights();
  renderAccountReport();
  renderCategoryBreakdown();
  renderBudgets();
  renderCategories();
  renderTransactions();
  renderTrendChart();
  if (state.accountHistoryId) renderAccountHistory();
}

function renderSummary() {
  const monthTransactions = scopedTransactionsForMonth(state.month);
  const income = sumByType(monthTransactions, "income");
  const expense = sumByType(monthTransactions, "expense");
  const balance = balanceForScope(monthTransactions);
  const budget = state.categories
    .filter((category) => category.type === "expense")
    .reduce((sum, category) => sum + category.budgetCents, 0);
  const budgetPercent = budget ? Math.round((expense / budget) * 100) : 0;
  const dailyAverage = expense ? Math.round(expense / daysInSelectedMonth()) : 0;
  const savingsPercent = income ? Math.round((balance / income) * 100) : 0;

  els.summaryBalance.textContent = formatMoney(balance);
  els.summaryIncome.textContent = formatMoney(income);
  els.summaryExpense.textContent = formatMoney(expense);
  els.summaryBudget.textContent = budget ? `${budgetPercent}%` : "Нет";
  els.balanceHint.textContent =
    state.filters.account === "all" ? "Все счета вместе" : getAccountScopeLabel();
  els.expenseHint.textContent = `В среднем: ${formatMoney(dailyAverage)} в день`;
  els.budgetHint.textContent = budget
    ? `${formatMoney(expense)} из ${formatMoney(budget)}`
    : "Лимиты пока не заданы";
  els.savingsRate.textContent = income
    ? `${Number.isFinite(savingsPercent) ? savingsPercent : 0}% осталось`
    : "Нет доходов";
}

function renderGoals() {
  els.goalList.replaceChildren();
  const active = state.goals.filter((goal) => !goal.archived);
  const archived = state.goals.filter((goal) => goal.archived);
  const goals = [...active, ...archived];
  if (!goals.length) {
    els.goalList.append(emptyState("Целей пока нет"));
    return;
  }
  goals.forEach((goal) => {
    const percent = Math.min((goal.savedCents / goal.targetCents) * 100, 100);
    const left = Math.max(goal.targetCents - goal.savedCents, 0);
    const item = document.createElement("article");
    item.className = `goal-card ${goal.archived ? "is-muted" : ""}`;
    item.style.setProperty("--item-color", goal.color);

    const head = document.createElement("div");
    head.className = "goal-head";
    const title = document.createElement("div");
    const name = document.createElement("strong");
    name.textContent = goal.name;
    const meta = document.createElement("small");
    meta.textContent = goal.targetDate
      ? `До ${formatDate(goal.targetDate)} · осталось ${formatMoney(left)}`
      : `Осталось ${formatMoney(left)}`;
    title.append(name, meta);
    const badge = document.createElement("span");
    badge.className = "goal-percent";
    badge.textContent = `${Math.round(percent)}%`;
    head.append(title, badge);

    const track = document.createElement("div");
    track.className = "track";
    const fill = document.createElement("span");
    fill.style.setProperty("--value", `${Math.max(2, percent)}%`);
    fill.style.setProperty("--bar-color", goal.color);
    track.append(fill);

    const footer = document.createElement("div");
    footer.className = "goal-footer";
    const amount = document.createElement("span");
    amount.textContent = `${formatMoney(goal.savedCents)} из ${formatMoney(goal.targetCents)}`;
    const actions = document.createElement("div");
    actions.className = "transaction-actions";
    const archive = document.createElement("button");
    archive.className = "button ghost";
    archive.type = "button";
    archive.dataset.archiveGoal = goal.id;
    archive.textContent = goal.archived ? "Вернуть" : "Закрыть";
    const remove = document.createElement("button");
    remove.className = "button danger";
    remove.type = "button";
    remove.dataset.deleteGoal = goal.id;
    remove.textContent = "Удалить";
    actions.append(archive, remove);
    footer.append(amount, actions);

    const form = document.createElement("form");
    form.className = "inline-money-form";
    form.dataset.goalForm = goal.id;
    const input = document.createElement("input");
    input.name = "amount";
    input.type = "text";
    input.inputMode = "decimal";
    input.placeholder = "Пополнить на";
    input.autocomplete = "off";
    input.disabled = goal.archived;
    const button = document.createElement("button");
    button.className = "button secondary";
    button.type = "submit";
    button.textContent = "+";
    button.setAttribute("aria-label", `Пополнить цель ${goal.name}`);
    button.disabled = goal.archived;
    form.append(input, button);

    item.append(head, track, footer, form);
    els.goalList.append(item);
  });
}

function renderDebts() {
  els.debtList.replaceChildren();
  const activeDebts = state.debts.filter((debt) => !debt.archived && debt.leftCents > 0);
  const oweMe = activeDebts
    .filter((debt) => debt.direction === "owe_me")
    .reduce((sum, debt) => sum + debt.leftCents, 0);
  const iOwe = activeDebts
    .filter((debt) => debt.direction === "i_owe")
    .reduce((sum, debt) => sum + debt.leftCents, 0);
  els.debtSummary.textContent = `${formatMoney(oweMe)} / ${formatMoney(iOwe)}`;

  const debts = [
    ...activeDebts,
    ...state.debts.filter((debt) => debt.archived || debt.leftCents <= 0),
  ];
  if (!debts.length) {
    els.debtList.append(emptyState("Долгов пока нет"));
    return;
  }

  debts.forEach((debt) => {
    const percent = Math.min((debt.paidCents / debt.amountCents) * 100, 100);
    const closed = debt.archived || debt.leftCents <= 0;
    const item = document.createElement("article");
    item.className = `debt-card ${closed ? "is-muted" : ""}`;
    item.style.setProperty("--item-color", debt.direction === "owe_me" ? "var(--accent)" : "var(--rose)");

    const head = document.createElement("div");
    head.className = "goal-head";
    const title = document.createElement("div");
    const name = document.createElement("strong");
    name.textContent = debt.person;
    const meta = document.createElement("small");
    const direction = debt.direction === "owe_me" ? "Мне должны" : "Я должен";
    const due = debt.dueDate ? ` · до ${formatDate(debt.dueDate)}` : "";
    meta.textContent = `${direction}${due}${debt.note ? ` · ${debt.note}` : ""}`;
    title.append(name, meta);
    const amount = document.createElement("span");
    amount.className = `debt-amount ${debt.direction}`;
    amount.textContent = formatMoney(debt.leftCents);
    head.append(title, amount);

    const track = document.createElement("div");
    track.className = "track";
    const fill = document.createElement("span");
    fill.style.setProperty("--value", `${Math.max(2, percent)}%`);
    fill.style.setProperty("--bar-color", debt.direction === "owe_me" ? "var(--accent)" : "var(--rose)");
    track.append(fill);

    const footer = document.createElement("div");
    footer.className = "goal-footer";
    const paid = document.createElement("span");
    paid.textContent = `Возвращено ${formatMoney(debt.paidCents)} из ${formatMoney(debt.amountCents)}`;
    const actions = document.createElement("div");
    actions.className = "transaction-actions";
    const archive = document.createElement("button");
    archive.className = "button ghost";
    archive.type = "button";
    archive.dataset.archiveDebt = debt.id;
    archive.textContent = closed ? "Вернуть" : "Закрыть";
    const remove = document.createElement("button");
    remove.className = "button danger";
    remove.type = "button";
    remove.dataset.deleteDebt = debt.id;
    remove.textContent = "Удалить";
    actions.append(archive, remove);
    footer.append(paid, actions);

    const form = document.createElement("form");
    form.className = "inline-money-form";
    form.dataset.debtPaymentForm = debt.id;
    const input = document.createElement("input");
    input.name = "amount";
    input.type = "text";
    input.inputMode = "decimal";
    input.placeholder = "Возврат";
    input.autocomplete = "off";
    input.disabled = closed;
    const button = document.createElement("button");
    button.className = "button secondary";
    button.type = "submit";
    button.textContent = "+";
    button.setAttribute("aria-label", `Записать возврат по долгу ${debt.person}`);
    button.disabled = closed;
    form.append(input, button);

    item.append(head, track, footer, form);
    els.debtList.append(item);
  });
}

function renderInsights() {
  const current = scopedTransactionsForMonth(state.month);
  const previousMonth = lastMonths(state.month, 2)[0];
  const previous = scopedTransactionsForMonth(previousMonth);
  const currentExpense = sumByType(current, "expense");
  const previousExpense = sumByType(previous, "expense");
  const currentIncome = sumByType(current, "income");
  const diff = currentExpense - previousExpense;
  const daysPassed = daysPassedInSelectedMonth();
  const forecast = daysPassed ? Math.round((currentExpense / daysPassed) * daysInSelectedMonth()) : currentExpense;
  const expenses = current.filter(
    (item) => item.type === "expense" && !item.isBalanceAdjustment
  );
  const biggest = expenses.sort((a, b) => b.amountCents - a.amountCents)[0];
  const topCategory = groupByCategory(expenses).sort((a, b) => b.amount - a.amount)[0];
  const activeGoals = state.goals.filter((goal) => !goal.archived);
  const goalNeed = activeGoals.reduce(
    (sum, goal) => sum + Math.max(goal.targetCents - goal.savedCents, 0),
    0
  );
  const activeDebtLeft = state.debts
    .filter((debt) => !debt.archived)
    .reduce((sum, debt) => sum + debt.leftCents * (debt.direction === "owe_me" ? 1 : -1), 0);

  const rows = [
    {
      title: "Прогноз расходов",
      value: formatMoney(forecast),
      note: `если тратить как сейчас`,
    },
    {
      title: "К прошлому месяцу",
      value: `${diff >= 0 ? "+" : ""}${formatMoney(diff)}`,
      note: previousExpense ? `прошлый: ${formatMoney(previousExpense)}` : "пока нет прошлого месяца",
    },
    {
      title: "Самая большая трата",
      value: biggest ? formatMoney(biggest.amountCents) : "Нет",
      note: biggest ? biggest.note || findCategory(biggest.categoryId)?.name || "Операция" : "за месяц",
    },
    {
      title: "Топ категория",
      value: topCategory ? formatMoney(topCategory.amount) : "Нет",
      note: topCategory ? topCategory.category?.name || "Без категории" : "за месяц",
    },
    {
      title: "На цели осталось",
      value: formatMoney(goalNeed),
      note: activeGoals.length ? `${activeGoals.length} активн.` : "целей пока нет",
    },
    {
      title: "Баланс долгов",
      value: formatSignedMoney(activeDebtLeft),
      note: "плюс — должны тебе",
    },
    {
      title: "Свободно за месяц",
      value: formatMoney(currentIncome - currentExpense),
      note: "доходы минус расходы",
    },
  ];

  els.insightList.replaceChildren();
  rows.forEach((row) => {
    const item = document.createElement("article");
    item.className = "insight-item";
    const title = document.createElement("span");
    title.textContent = row.title;
    const value = document.createElement("strong");
    value.textContent = row.value;
    const note = document.createElement("small");
    note.textContent = row.note;
    item.append(title, value, note);
    els.insightList.append(item);
  });
}

function renderAccountReport() {
  const monthTransactions = transactionsForMonth(state.month);
  els.accountReportList.replaceChildren();
  if (!state.accounts.length) {
    els.accountReportList.append(emptyState("Счетов пока нет"));
    return;
  }
  state.accounts.forEach((account) => {
    const summary = accountSummary(monthTransactions, account.id);
    const row = document.createElement("button");
    row.className = "account-report-row";
    row.type = "button";
    row.dataset.accountReport = account.id;
    row.style.setProperty("--account-color", account.color);
    row.setAttribute("aria-pressed", String(state.filters.account === account.id));

    const head = document.createElement("span");
    head.className = "account-report-head";
    const swatch = document.createElement("span");
    swatch.className = "account-report-swatch";
    swatch.setAttribute("aria-hidden", "true");
    const name = document.createElement("strong");
    name.textContent = account.name;
    head.append(swatch, name);

    const income = reportMetric("Доход", formatMoney(summary.income), "income");
    const expense = reportMetric("Расход", formatMoney(summary.expense), "expense");
    const transfers = reportMetric("Переводы", formatSignedMoney(summary.transfers), "transfer");
    const balance = reportMetric("Итог", formatMoney(summary.balance), "balance");

    row.append(head, income, expense, transfers, balance);
    els.accountReportList.append(row);
  });
}

function renderCategoryBreakdown() {
  const expenses = scopedTransactionsForMonth(state.month).filter(
    (item) => item.type === "expense" && !item.isBalanceAdjustment
  );
  const total = expenses.reduce((sum, item) => sum + item.amountCents, 0);
  const grouped = groupByCategory(expenses)
    .map(({ category, amount }) => ({ category, amount, percent: total ? (amount / total) * 100 : 0 }))
    .sort((a, b) => b.amount - a.amount);

  els.categoryBreakdown.replaceChildren();
  if (!grouped.length) {
    els.categoryBreakdown.append(emptyState("Расходов за месяц пока нет"));
    return;
  }
  grouped.forEach((item) => {
    els.categoryBreakdown.append(
      createBarRow(
        item.category?.name || "Без категории",
        formatMoney(item.amount),
        item.percent,
        item.category?.color || "#6b7280"
      )
    );
  });
}

function renderBudgets() {
  const monthTransactions = scopedTransactionsForMonth(state.month).filter(
    (item) => item.type === "expense" && !item.isBalanceAdjustment
  );
  const budgets = state.categories
    .filter((category) => category.type === "expense" && category.budgetCents > 0)
    .map((category) => {
      const spent = monthTransactions
        .filter((item) => item.categoryId === category.id)
        .reduce((sum, item) => sum + item.amountCents, 0);
      return {
        category,
        spent,
        percent: Math.min((spent / category.budgetCents) * 100, 100),
      };
    })
    .sort((a, b) => b.spent - a.spent);

  els.budgetBreakdown.replaceChildren();
  if (!budgets.length) {
    els.budgetBreakdown.append(emptyState("Лимиты можно задать при создании категории"));
    return;
  }
  budgets.forEach((item) => {
    const label = `${formatMoney(item.spent)} из ${formatMoney(item.category.budgetCents)}`;
    const color = item.spent > item.category.budgetCents ? "#cf3f61" : item.category.color;
    els.budgetBreakdown.append(createBarRow(item.category.name, label, item.percent, color));
  });
}

function renderAccounts() {
  const monthTransactions = transactionsForMonth(state.month);
  const accountCards = [
    {
      id: "all",
      name: "Все счета",
      color: "#302f2b",
      actualBalance: totalAccountBalanceThroughMonth(state.month),
      summary: accountSummary(monthTransactions),
    },
    ...state.accounts.map((account) => ({
      ...account,
      actualBalance: accountBalanceThroughMonth(account.id, state.month),
      summary: accountSummary(monthTransactions, account.id),
    })),
  ];

  els.accountGrid.replaceChildren();
  accountCards.forEach((account) => {
    const { income, expense, transfers } = account.summary;
    const button = document.createElement("button");
    button.className = "account-card";
    button.type = "button";
    button.dataset.accountFilter = account.id;
    button.style.setProperty("--account-color", account.color);
    button.setAttribute("aria-pressed", String(state.filters.account === account.id));

    const top = document.createElement("span");
    top.className = "account-card-top";
    const name = document.createElement("span");
    name.className = "account-name";
    name.textContent = account.name;
    top.append(name);
    if (account.id !== "all") {
      const kind = document.createElement("span");
      kind.className = `account-kind ${account.kind === "savings" ? "is-savings" : ""}`;
      kind.textContent = account.kind === "savings" ? "Накопления" : "Основной";
      top.append(kind);
    }

    const amount = document.createElement("strong");
    amount.textContent = formatMoney(account.actualBalance);

    const details = document.createElement("small");
    details.className = "account-details";
    if (account.id === "all") {
      details.textContent = "Фактический остаток на конец месяца";
    } else {
      details.textContent = [account.bank || "Банк не указан", account.owner]
        .filter(Boolean)
        .join(" · ");
    }

    const meta = document.createElement("small");
    meta.className = "account-activity";
    meta.textContent =
      transfers && account.id !== "all"
        ? `${formatMoney(income)} доход · ${formatMoney(expense)} расход · ${formatSignedMoney(
            transfers
          )} переводы`
        : `${formatMoney(income)} доход · ${formatMoney(expense)} расход`;

    button.append(top, amount, details, meta);
    els.accountGrid.append(button);
  });

  els.accountScopeLabel.textContent = getAccountScopeLabel();
  const selectedAccount = findAccount(state.filters.account);
  els.selectedAccountHistory.hidden = !selectedAccount;
  if (selectedAccount) {
    els.selectedAccountHistory.setAttribute(
      "aria-label",
      `Открыть историю счета ${selectedAccount.name}`
    );
  }
}

function renderAccountOverview() {
  const balances = state.accounts.map((account) => ({
    account,
    balance: accountBalanceThroughMonth(account.id, state.month),
  }));
  const total = balances.reduce((sum, item) => sum + item.balance, 0);
  const savings = balances
    .filter((item) => item.account.kind === "savings")
    .reduce((sum, item) => sum + item.balance, 0);
  const savingsCount = state.accounts.filter((account) => account.kind === "savings").length;

  els.totalAccountsBalance.textContent = formatMoney(total);
  els.totalSavingsBalance.textContent = formatMoney(savings);
  els.savingsAccountsHint.textContent = savingsCount
    ? `${savingsCount} ${pluralizeAccounts(savingsCount)}`
    : "Добавьте накопительный счет";

  const banks = new Map();
  balances.forEach(({ account, balance }) => {
    const bank = account.bank || "Банк не указан";
    const current = banks.get(bank) || { balance: 0, accounts: 0 };
    current.balance += balance;
    current.accounts += 1;
    banks.set(bank, current);
  });

  els.bankTotals.replaceChildren();
  Array.from(banks.entries())
    .sort((a, b) => b[1].balance - a[1].balance)
    .forEach(([bank, item]) => {
      const row = document.createElement("article");
      row.className = "bank-total-item";
      const copy = document.createElement("span");
      const name = document.createElement("strong");
      name.textContent = bank;
      const count = document.createElement("small");
      count.textContent = `${item.accounts} ${pluralizeAccounts(item.accounts)}`;
      copy.append(name, count);
      const amount = document.createElement("strong");
      amount.textContent = formatMoney(item.balance);
      row.append(copy, amount);
      els.bankTotals.append(row);
    });
}

function renderAccountManagement() {
  els.accountManagementList.replaceChildren();
  state.accounts.forEach((account) => {
    const item = document.createElement("article");
    item.className = "account-management-item";
    item.style.setProperty("--account-color", account.color);

    const swatch = document.createElement("span");
    swatch.className = "account-management-swatch";
    swatch.setAttribute("aria-hidden", "true");

    const copy = document.createElement("div");
    const title = document.createElement("strong");
    title.textContent = account.name;
    const meta = document.createElement("small");
    meta.textContent = [
      account.kind === "savings" ? "Накопительный" : "Основной",
      account.bank || "банк не указан",
      account.owner,
    ]
      .filter(Boolean)
      .join(" · ");
    copy.append(title, meta);

    const balance = document.createElement("strong");
    balance.className = "account-management-balance";
    balance.textContent = formatMoney(accountBalanceThroughMonth(account.id, state.month));

    const actions = document.createElement("div");
    actions.className = "transaction-actions account-management-actions";
    const history = document.createElement("button");
    history.className = "button ghost";
    history.type = "button";
    history.dataset.historyAccount = account.id;
    history.textContent = "История";
    history.setAttribute("aria-label", `Открыть историю счета ${account.name}`);
    const setBalance = document.createElement("button");
    setBalance.className = "button ghost";
    setBalance.type = "button";
    setBalance.dataset.balanceAccount = account.id;
    setBalance.textContent = "Баланс";
    const edit = document.createElement("button");
    edit.className = "button ghost";
    edit.type = "button";
    edit.dataset.editAccount = account.id;
    edit.textContent = "Изменить";
    actions.append(history, setBalance, edit);
    if (!account.system) {
      const remove = document.createElement("button");
      remove.className = "button danger";
      remove.type = "button";
      remove.dataset.deleteAccount = account.id;
      remove.textContent = "Удалить";
      actions.append(remove);
    }

    item.append(swatch, copy, balance, actions);
    els.accountManagementList.append(item);
  });
}

function renderAccountHistory() {
  const account = findAccount(state.accountHistoryId);
  if (!account) {
    closeAccountHistory();
    return;
  }

  const allTransactions = transactionsForAccount(account.id).sort((a, b) =>
    `${b.date}${b.createdAt}`.localeCompare(`${a.date}${a.createdAt}`)
  );
  const periodTransactions =
    state.accountHistoryPeriod === "all"
      ? allTransactions
      : allTransactions.filter((transaction) =>
          transaction.date.startsWith(state.accountHistoryPeriod)
        );
  const cashFlowTransactions = periodTransactions.filter(
    (transaction) => !transaction.isBalanceAdjustment
  );
  const incoming = cashFlowTransactions.reduce((sum, transaction) => {
    const amount = amountForAccount(transaction, account.id);
    return amount > 0 ? sum + amount : sum;
  }, 0);
  const outgoing = cashFlowTransactions.reduce((sum, transaction) => {
    const amount = amountForAccount(transaction, account.id);
    return amount < 0 ? sum + Math.abs(amount) : sum;
  }, 0);

  els.accountHistorySwatch.style.setProperty("--account-color", account.color);
  els.accountHistoryTitle.textContent = account.name;
  els.accountHistoryMeta.textContent = [
    account.kind === "savings" ? "Накопительный счет" : "Основной счет",
    account.bank || "банк не указан",
    account.owner,
  ]
    .filter(Boolean)
    .join(" · ");
  els.accountHistoryBalanceLabel.textContent = `Баланс на конец месяца · ${formatMonthLong(
    state.month
  )}`;
  els.accountHistoryBalance.textContent = formatMoney(
    accountBalanceThroughMonth(account.id, state.month)
  );
  els.accountHistoryIncome.textContent = formatMoney(incoming);
  els.accountHistoryExpense.textContent = formatMoney(outgoing);
  els.accountHistoryCount.textContent = String(periodTransactions.length);

  els.accountHistoryList.replaceChildren();
  if (!periodTransactions.length) {
    els.accountHistoryList.append(
      emptyState(
        state.accountHistoryPeriod === "all"
          ? "У этого счета пока нет операций"
          : "В выбранном месяце операций нет"
      )
    );
    return;
  }

  periodTransactions.forEach((transaction) => {
    const amount = amountForAccount(transaction, account.id);
    const category = findCategory(transaction.categoryId);
    const item = document.createElement("article");
    item.className = "account-history-item";
    item.style.setProperty(
      "--item-color",
      transaction.isBalanceAdjustment ? account.color : category?.color || account.color
    );

    const swatch = document.createElement("span");
    swatch.className = "account-history-item-swatch";
    swatch.setAttribute("aria-hidden", "true");

    const copy = document.createElement("div");
    const title = document.createElement("strong");
    title.textContent = accountHistoryTransactionTitle(transaction, account.id);
    const meta = document.createElement("small");
    meta.textContent = accountHistoryTransactionMeta(transaction, account.id);
    copy.append(title, meta);

    const value = document.createElement("strong");
    value.className = `account-history-item-amount ${amount >= 0 ? "positive" : "negative"}`;
    value.textContent = `${amount > 0 ? "+" : amount < 0 ? "−" : ""}${formatMoney(
      Math.abs(amount)
    )}`;

    item.append(swatch, copy, value);
    els.accountHistoryList.append(item);
  });
}

function renderCategories() {
  const grouped = ["expense", "income"].flatMap((type) =>
    state.categories
      .filter((category) => category.type === type)
      .map((category) => ({ ...category, group: type === "expense" ? "Расход" : "Доход" }))
  );

  els.categoryList.replaceChildren();
  grouped.forEach((category) => {
    const item = document.createElement("article");
    item.className = "category-item";
    item.style.setProperty("--item-color", category.color);

    const swatch = document.createElement("span");
    swatch.className = "category-swatch";
    swatch.setAttribute("aria-hidden", "true");

    const content = document.createElement("div");
    const title = document.createElement("strong");
    title.textContent = category.name;
    const meta = document.createElement("small");
    meta.textContent =
      category.type === "expense" && category.budgetCents
        ? `${category.group}, лимит ${formatMoney(category.budgetCents)}`
        : category.group;
    content.append(title, meta);

    const button = document.createElement("button");
    button.className = "button danger";
    button.type = "button";
    button.dataset.deleteCategory = category.id;
    button.textContent = "Удалить";
    button.setAttribute("aria-label", `Удалить категорию ${category.name}`);

    const editButton = document.createElement("button");
    editButton.className = "button ghost";
    editButton.type = "button";
    editButton.dataset.editCategory = category.id;
    editButton.textContent = "Изменить";
    editButton.setAttribute("aria-label", `Изменить категорию ${category.name}`);

    const actions = document.createElement("div");
    actions.className = "transaction-actions";
    actions.append(editButton, button);

    item.append(swatch, content, actions);
    els.categoryList.append(item);
  });
}

function renderTransactions() {
  const filtered = scopedTransactionsForMonth(state.month)
    .filter((item) => state.filters.type === "all" || item.type === state.filters.type)
    .filter((item) => state.filters.category === "all" || item.categoryId === state.filters.category)
    .filter((item) => !state.filters.query || item.note.toLowerCase().includes(state.filters.query))
    .sort((a, b) => `${b.date}${b.createdAt}`.localeCompare(`${a.date}${a.createdAt}`));

  els.transactionList.replaceChildren();
  if (!filtered.length) {
    els.transactionList.append(emptyState("Операций за выбранный период нет"));
    return;
  }

  filtered.forEach((transaction) => {
    const account = findAccount(transaction.accountId);
    const toAccount = findAccount(transaction.toAccountId);
    const category = findCategory(transaction.categoryId);
    const item = document.createElement("article");
    item.className = "transaction-item";
    item.style.setProperty("--item-color", category?.color || "#6b7280");

    const swatch = document.createElement("span");
    swatch.className = "transaction-swatch";
    swatch.setAttribute("aria-hidden", "true");

    const content = document.createElement("div");
    const title = document.createElement("strong");
    title.className = "transaction-title";
    title.textContent =
      transaction.note ||
      (transaction.isBalanceAdjustment
        ? "Коррекция баланса"
        : transaction.type === "transfer"
          ? "Перевод между счетами"
          : category?.name || "Операция");
    const meta = document.createElement("span");
    meta.className = "transaction-meta";
    meta.textContent =
      transaction.type === "transfer"
        ? `${formatDate(transaction.date)} · ${account?.name || "Без счета"} → ${
            toAccount?.name || "Без счета"
          }`
        : transaction.isBalanceAdjustment
          ? `${formatDate(transaction.date)} · ${account?.name || "Без счета"} · ручной баланс`
        : `${formatDate(transaction.date)} · ${account?.name || "Без счета"} · ${
            category?.name || "Без категории"
          }`;
    content.append(title, meta);

    const actions = document.createElement("div");
    actions.className = "transaction-actions";

    const amount = document.createElement("strong");
    amount.className = `transaction-amount ${
      transaction.isBalanceAdjustment ? "adjustment" : transaction.type
    }`;
    if (transaction.type === "transfer") {
      amount.textContent = `↔ ${formatMoney(transaction.amountCents)}`;
    } else {
      amount.textContent = `${transaction.type === "income" ? "+" : "-"}${formatMoney(
        transaction.amountCents
      )}`;
    }

    const button = document.createElement("button");
    button.className = "button danger";
    button.type = "button";
    button.dataset.deleteTransaction = transaction.id;
    button.textContent = "Удалить";
    button.setAttribute("aria-label", "Удалить операцию");

    actions.append(amount);
    if (!transaction.isBalanceAdjustment) {
      const editButton = document.createElement("button");
      editButton.className = "button ghost";
      editButton.type = "button";
      editButton.dataset.editTransaction = transaction.id;
      editButton.textContent = "Изменить";
      editButton.setAttribute("aria-label", "Изменить операцию");
      actions.append(editButton);
    }
    actions.append(button);
    item.append(swatch, content, actions);
    els.transactionList.append(item);
  });
}

function renderTrendChart() {
  const canvas = els.trendChart;
  if (!canvas) return;
  const rect = canvas.getBoundingClientRect();
  if (!rect.width || !rect.height) return;

  const dpr = window.devicePixelRatio || 1;
  canvas.width = Math.floor(rect.width * dpr);
  canvas.height = Math.floor(rect.height * dpr);
  const ctx = canvas.getContext("2d");
  const lineColor = getThemeColor("--line");
  const mutedColor = getThemeColor("--muted");
  const inkColor = getThemeColor("--ink");
  const incomeColor = getThemeColor("--accent");
  const expenseColor = getThemeColor("--rose");
  ctx.setTransform(dpr, 0, 0, dpr, 0, 0);
  ctx.clearRect(0, 0, rect.width, rect.height);

  const months = lastMonths(state.month, 6);
  const rows = months.map((month) => {
    const items = scopedTransactionsForMonth(month);
    return {
      month,
      income: sumByType(items, "income"),
      expense: sumByType(items, "expense"),
    };
  });
  const maxValue = Math.max(...rows.flatMap((row) => [row.income, row.expense]), 1);
  const pad = { top: 18, right: 16, bottom: 36, left: 54 };
  const chartWidth = rect.width - pad.left - pad.right;
  const chartHeight = rect.height - pad.top - pad.bottom;
  const groupWidth = chartWidth / rows.length;
  const barWidth = Math.min(24, Math.max(10, groupWidth * 0.22));

  ctx.lineWidth = 1;
  ctx.strokeStyle = lineColor;
  ctx.fillStyle = mutedColor;
  ctx.font = "12px Inter, system-ui, sans-serif";
  ctx.textAlign = "right";
  ctx.textBaseline = "middle";

  [0, 0.5, 1].forEach((step) => {
    const y = pad.top + chartHeight - chartHeight * step;
    ctx.beginPath();
    ctx.moveTo(pad.left, y);
    ctx.lineTo(rect.width - pad.right, y);
    ctx.stroke();
    ctx.fillText(compactMoney.format((maxValue * step) / 100), pad.left - 8, y);
  });

  rows.forEach((row, index) => {
    const center = pad.left + groupWidth * index + groupWidth / 2;
    const incomeHeight = (row.income / maxValue) * chartHeight;
    const expenseHeight = (row.expense / maxValue) * chartHeight;
    const base = pad.top + chartHeight;

    drawRoundedBar(ctx, center - barWidth - 3, base - incomeHeight, barWidth, incomeHeight, incomeColor);
    drawRoundedBar(ctx, center + 3, base - expenseHeight, barWidth, expenseHeight, expenseColor);

    ctx.fillStyle = mutedColor;
    ctx.textAlign = "center";
    ctx.textBaseline = "top";
    ctx.fillText(formatMonthShort(row.month), center, base + 12);
  });

  ctx.fillStyle = incomeColor;
  ctx.fillRect(rect.width - 134, 12, 10, 10);
  ctx.fillStyle = inkColor;
  ctx.textAlign = "left";
  ctx.textBaseline = "middle";
  ctx.fillText("Доход", rect.width - 118, 17);
  ctx.fillStyle = expenseColor;
  ctx.fillRect(rect.width - 66, 12, 10, 10);
  ctx.fillStyle = inkColor;
  ctx.fillText("Расход", rect.width - 50, 17);
}

function drawRoundedBar(ctx, x, y, width, height, color) {
  const safeHeight = Math.max(height, 2);
  const radius = Math.min(4, width / 2, safeHeight / 2);
  ctx.fillStyle = color;
  ctx.beginPath();
  ctx.moveTo(x + radius, y);
  ctx.lineTo(x + width - radius, y);
  ctx.quadraticCurveTo(x + width, y, x + width, y + radius);
  ctx.lineTo(x + width, y + safeHeight);
  ctx.lineTo(x, y + safeHeight);
  ctx.lineTo(x, y + radius);
  ctx.quadraticCurveTo(x, y, x + radius, y);
  ctx.fill();
}

function drawReportCard(ctx, x, y, width, height, label, value, color, theme) {
  ctx.fillStyle = theme.surfaceColor;
  roundRect(ctx, x, y, width, height, 8);
  ctx.fill();
  ctx.strokeStyle = theme.lineColor;
  ctx.lineWidth = 1;
  roundRect(ctx, x, y, width, height, 8);
  ctx.stroke();
  drawReportText(ctx, label, x + 26, y + 42, 20, theme.mutedColor, 700);
  drawReportText(ctx, value, x + 26, y + 96, 36, color, 900);
}

function drawReportText(ctx, text, x, y, size, color, weight = 600, align = "left") {
  ctx.fillStyle = color;
  ctx.font = `${weight} ${size}px Inter, Arial, sans-serif`;
  ctx.textAlign = align;
  ctx.textBaseline = "alphabetic";
  ctx.fillText(text, x, y);
}

function roundRect(ctx, x, y, width, height, radius) {
  const safeRadius = Math.min(radius, width / 2, height / 2);
  ctx.beginPath();
  ctx.moveTo(x + safeRadius, y);
  ctx.lineTo(x + width - safeRadius, y);
  ctx.quadraticCurveTo(x + width, y, x + width, y + safeRadius);
  ctx.lineTo(x + width, y + height - safeRadius);
  ctx.quadraticCurveTo(x + width, y + height, x + width - safeRadius, y + height);
  ctx.lineTo(x + safeRadius, y + height);
  ctx.quadraticCurveTo(x, y + height, x, y + height - safeRadius);
  ctx.lineTo(x, y + safeRadius);
  ctx.quadraticCurveTo(x, y, x + safeRadius, y);
}

function updateTransactionCategoryOptions(type) {
  const current = els.categorySelect.value;
  const categories = state.categories.filter((category) => category.type === type);
  els.categorySelect.replaceChildren(
    ...categories.map((category) => new Option(category.name, category.id))
  );
  if (categories.some((category) => category.id === current)) {
    els.categorySelect.value = current;
  }
}

function updateAccountOptions(preferredId) {
  const current = preferredId || els.accountSelect.value || "account-shared";
  els.accountSelect.replaceChildren(
    ...state.accounts.map((account) => new Option(accountOptionLabel(account), account.id))
  );
  if (state.accounts.some((account) => account.id === current)) {
    els.accountSelect.value = current;
    updateToAccountOptions();
    return;
  }
  if (state.accounts.length) {
    els.accountSelect.value = state.accounts[0].id;
  }
  updateToAccountOptions();
}

function updateToAccountOptions(preferredId) {
  const fromAccountId = els.accountSelect.value;
  const available = state.accounts.filter((account) => account.id !== fromAccountId);
  const current = preferredId || els.toAccountSelect.value;
  els.toAccountSelect.replaceChildren(
    ...available.map((account) => new Option(accountOptionLabel(account), account.id))
  );
  if (available.some((account) => account.id === current)) {
    els.toAccountSelect.value = current;
    return;
  }
  if (available.length) {
    els.toAccountSelect.value = available[0].id;
  }
}

function updateBalanceAccountOptions(preferredId) {
  const current = preferredId || els.balanceAccountSelect.value || state.filters.account;
  els.balanceAccountSelect.replaceChildren(
    ...state.accounts.map((account) => new Option(accountOptionLabel(account), account.id))
  );
  if (state.accounts.some((account) => account.id === current)) {
    els.balanceAccountSelect.value = current;
    return;
  }
  if (state.accounts.length) {
    els.balanceAccountSelect.value = state.accounts[0].id;
  }
}

function updateAccountFilterOptions() {
  const current = state.accounts.some((account) => account.id === state.filters.account)
    ? state.filters.account
    : "all";
  if (current !== state.filters.account) {
    state.filters.account = current;
    localStorage.setItem("finance-account", current);
  }
  const options = [new Option("Все счета", "all")].concat(
    state.accounts.map((account) => new Option(accountOptionLabel(account), account.id))
  );
  els.accountFilter.replaceChildren(...options);
  els.accountFilter.value = current;
}

function updateFilterCategoryOptions() {
  const current = els.categoryFilter.value;
  const options = [new Option("Все категории", "all")].concat(
    state.categories.map((category) => {
      const label = `${category.type === "income" ? "Доход" : "Расход"} · ${category.name}`;
      return new Option(label, category.id);
    })
  );
  els.categoryFilter.replaceChildren(...options);
  els.categoryFilter.value = options.some((option) => option.value === current) ? current : "all";
}

function syncQuickCategoryState() {
  const type = getSelectedTransactionType();
  const isTransfer = type === "transfer";
  const isIncome = type === "income";
  els.quickCategoryForm.hidden = isTransfer;
  els.quickCategoryTypeBadge.textContent = isIncome ? "Для доходов" : "Для расходов";
  els.quickBudgetField.hidden = isIncome || isTransfer;
  els.quickCategoryBudget.disabled = isIncome || isTransfer;
}

function syncTransactionMode() {
  const type = getSelectedTransactionType();
  const isTransfer = type === "transfer";
  els.accountSelectLabel.textContent = isTransfer ? "Со счета" : "Счет";
  els.categoryField.hidden = isTransfer;
  els.categorySelect.disabled = isTransfer;
  els.toAccountField.hidden = !isTransfer;
  els.toAccountSelect.disabled = !isTransfer;
  if (!isTransfer) {
    updateTransactionCategoryOptions(type);
  }
  updateToAccountOptions();
  syncQuickCategoryState();
}

function setAccountFilter(accountId) {
  state.filters.account = accountId;
  localStorage.setItem("finance-account", accountId);
  els.accountFilter.value = accountId;
  renderAccounts();
  renderSummary();
  renderAccountReport();
  renderInsights();
  renderCategoryBreakdown();
  renderBudgets();
  renderTransactions();
  renderTrendChart();
}

function applyTheme(theme) {
  state.theme = theme === "dark" ? "dark" : "light";
  document.documentElement.dataset.theme = state.theme;
  localStorage.setItem("finance-theme", state.theme);
  if (els.themeToggle) {
    const isDark = state.theme === "dark";
    els.themeToggle.textContent = isDark ? "Светлая" : "Темная";
    els.themeToggle.setAttribute("aria-pressed", String(isDark));
  }
}

function createBarRow(title, value, percent, color) {
  const row = document.createElement("div");
  row.className = "bar-row";

  const meta = document.createElement("div");
  meta.className = "bar-meta";
  const name = document.createElement("strong");
  name.textContent = title;
  const amount = document.createElement("span");
  amount.textContent = value;
  meta.append(name, amount);

  const track = document.createElement("div");
  track.className = "track";
  const fill = document.createElement("span");
  fill.style.setProperty("--value", `${Math.max(2, Math.min(percent, 100))}%`);
  fill.style.setProperty("--bar-color", color);
  track.append(fill);

  row.append(meta, track);
  return row;
}

function reportMetric(label, value, type) {
  const node = document.createElement("span");
  node.className = `account-report-metric ${type}`;
  const caption = document.createElement("small");
  caption.textContent = label;
  const amount = document.createElement("strong");
  amount.textContent = value;
  node.append(caption, amount);
  return node;
}

function emptyState(message) {
  const node = document.createElement("div");
  node.className = "empty-state";
  node.textContent = message;
  return node;
}

function groupByCategory(transactions) {
  const grouped = new Map();
  transactions.forEach((transaction) => {
    grouped.set(
      transaction.categoryId,
      (grouped.get(transaction.categoryId) || 0) + transaction.amountCents
    );
  });
  return Array.from(grouped, ([categoryId, amount]) => ({
    category: findCategory(categoryId),
    amount,
  }));
}

function transactionsForMonth(month) {
  return state.transactions.filter((transaction) => transaction.date.startsWith(month));
}

function transactionsForAccount(accountId) {
  return state.transactions.filter(
    (transaction) =>
      transaction.accountId === accountId || transaction.toAccountId === accountId
  );
}

function amountForAccount(transaction, accountId) {
  if (transaction.type === "income" && transaction.accountId === accountId) {
    return transaction.amountCents;
  }
  if (transaction.type === "expense" && transaction.accountId === accountId) {
    return -transaction.amountCents;
  }
  if (transaction.type === "transfer") {
    if (transaction.accountId === accountId) return -transaction.amountCents;
    if (transaction.toAccountId === accountId) return transaction.amountCents;
  }
  return 0;
}

function accountHistoryTransactionTitle(transaction, accountId) {
  if (transaction.isBalanceAdjustment) {
    return transaction.note || "Ручная корректировка баланса";
  }
  if (transaction.note) return transaction.note;
  if (transaction.type === "transfer") {
    return transaction.toAccountId === accountId ? "Входящий перевод" : "Исходящий перевод";
  }
  return findCategory(transaction.categoryId)?.name || "Операция";
}

function accountHistoryTransactionMeta(transaction, accountId) {
  const date = formatDate(transaction.date);
  if (transaction.isBalanceAdjustment) {
    return `${date} · Ручная установка баланса`;
  }
  if (transaction.type === "transfer") {
    const otherAccount =
      transaction.toAccountId === accountId
        ? findAccount(transaction.accountId)
        : findAccount(transaction.toAccountId);
    const direction = transaction.toAccountId === accountId ? "С" : "На";
    return `${date} · ${direction} ${otherAccount?.name || "другой счет"}`;
  }
  const category = findCategory(transaction.categoryId);
  return `${date} · ${transaction.type === "income" ? "Доход" : "Расход"} · ${
    category?.name || "Без категории"
  }`;
}

function scopedTransactionsForMonth(month) {
  return transactionsForMonth(month).filter(
    (transaction) =>
      state.filters.account === "all" ||
      transaction.accountId === state.filters.account ||
      transaction.toAccountId === state.filters.account
  );
}

function sumByType(transactions, type) {
  return transactions
    .filter(
      (transaction) => transaction.type === type && !transaction.isBalanceAdjustment
    )
    .reduce((sum, transaction) => sum + transaction.amountCents, 0);
}

function accountSummary(transactions, accountId = "all") {
  return transactions.reduce(
    (summary, transaction) => {
      if (transaction.isBalanceAdjustment) return summary;
      if (transaction.type === "income") {
        if (accountId === "all" || transaction.accountId === accountId) {
          summary.income += transaction.amountCents;
          summary.balance += transaction.amountCents;
        }
        return summary;
      }
      if (transaction.type === "expense") {
        if (accountId === "all" || transaction.accountId === accountId) {
          summary.expense += transaction.amountCents;
          summary.balance -= transaction.amountCents;
        }
        return summary;
      }
      if (transaction.type === "transfer" && accountId !== "all") {
        if (transaction.accountId === accountId) {
          summary.transfers -= transaction.amountCents;
          summary.balance -= transaction.amountCents;
        }
        if (transaction.toAccountId === accountId) {
          summary.transfers += transaction.amountCents;
          summary.balance += transaction.amountCents;
        }
      }
      return summary;
    },
    { income: 0, expense: 0, transfers: 0, balance: 0 }
  );
}

function accountBalanceThroughMonth(accountId, month) {
  const cutoff = endOfMonthValue(month);
  return state.transactions.reduce((balance, transaction) => {
    if (transaction.date > cutoff) return balance;
    if (transaction.type === "income" && transaction.accountId === accountId) {
      return balance + transaction.amountCents;
    }
    if (transaction.type === "expense" && transaction.accountId === accountId) {
      return balance - transaction.amountCents;
    }
    if (transaction.type === "transfer") {
      if (transaction.accountId === accountId) balance -= transaction.amountCents;
      if (transaction.toAccountId === accountId) balance += transaction.amountCents;
    }
    return balance;
  }, 0);
}

function totalAccountBalanceThroughMonth(month) {
  return state.accounts.reduce(
    (sum, account) => sum + accountBalanceThroughMonth(account.id, month),
    0
  );
}

function endOfMonthValue(month) {
  const [year, monthNumber] = month.split("-").map(Number);
  const lastDay = new Date(year, monthNumber, 0).getDate();
  return `${month}-${String(lastDay).padStart(2, "0")}`;
}

function balanceForScope(transactions) {
  return accountSummary(transactions, state.filters.account).balance;
}

function amountForScope(transaction) {
  if (transaction.type === "income") return transaction.amountCents;
  if (transaction.type === "expense") return -transaction.amountCents;
  if (state.filters.account === "all") return 0;
  if (transaction.accountId === state.filters.account) return -transaction.amountCents;
  if (transaction.toAccountId === state.filters.account) return transaction.amountCents;
  return 0;
}

function findAccount(id) {
  return state.accounts.find((account) => account.id === id);
}

function findCategory(id) {
  return state.categories.find((category) => category.id === id);
}

function getAccountScopeLabel() {
  if (state.filters.account === "all") {
    return "Все счета";
  }
  return findAccount(state.filters.account)?.name || "Выбранный счет";
}

function accountOptionLabel(account) {
  return account.bank ? `${account.name} · ${account.bank}` : account.name;
}

function accountFileSuffix() {
  if (state.filters.account === "all") {
    return "";
  }
  return `-${slugify(getAccountScopeLabel())}`;
}

function getSelectedTransactionType() {
  return els.transactionForm.querySelector("input[name='type']:checked")?.value || "expense";
}

function parseAmountToCents(value) {
  const normalized = String(value || "")
    .replace(/\s/g, "")
    .replace(",", ".");
  const amount = Number(normalized);
  if (!Number.isFinite(amount) || amount <= 0) return 0;
  return Math.round(amount * 100);
}

function parseBalanceToCents(value) {
  const normalized = String(value || "")
    .replace(/\s/g, "")
    .replace(",", ".");
  if (!normalized) return NaN;
  const amount = Number(normalized);
  if (!Number.isFinite(amount)) return NaN;
  return Math.round(amount * 100);
}

function formatAmountInput(cents) {
  const value = cents / 100;
  return Number.isInteger(value) ? String(value) : String(value).replace(".", ",");
}

function formatMoney(cents) {
  return money.format(cents / 100);
}

function formatSignedMoney(cents) {
  const sign = cents > 0 ? "+" : "";
  return `${sign}${formatMoney(cents)}`;
}

function transactionTypeLabel(type, isBalanceAdjustment = false) {
  if (isBalanceAdjustment) return "Коррекция баланса";
  if (type === "income") return "Доход";
  if (type === "expense") return "Расход";
  return "Перевод";
}

function pluralizeAccounts(count) {
  const lastTwo = count % 100;
  const last = count % 10;
  if (lastTwo >= 11 && lastTwo <= 14) return "счетов";
  if (last === 1) return "счет";
  if (last >= 2 && last <= 4) return "счета";
  return "счетов";
}

function getThemeColor(name) {
  return getComputedStyle(document.documentElement).getPropertyValue(name).trim();
}

function formatDate(value) {
  const date = new Date(`${value}T00:00:00`);
  return dateFormat.format(date);
}

function formatMonthShort(value) {
  const [year, month] = value.split("-").map(Number);
  return new Intl.DateTimeFormat("ru-RU", { month: "short" }).format(
    new Date(year, month - 1, 1)
  );
}

function formatMonthLong(value) {
  const [year, month] = value.split("-").map(Number);
  return new Intl.DateTimeFormat("ru-RU", {
    month: "long",
    year: "numeric",
  }).format(new Date(year, month - 1, 1));
}

function csvCell(value) {
  return `"${String(value ?? "").replace(/"/g, '""')}"`;
}

function slugify(value) {
  return String(value)
    .toLowerCase()
    .trim()
    .replace(/[^a-zа-я0-9]+/gi, "-")
    .replace(/^-+|-+$/g, "");
}

function downloadBlob(blob, filename) {
  const link = document.createElement("a");
  link.href = URL.createObjectURL(blob);
  link.download = filename;
  link.click();
  URL.revokeObjectURL(link.href);
}

function currentMonth() {
  return todayInputValue().slice(0, 7);
}

function todayInputValue() {
  const now = new Date();
  const local = new Date(now.getTime() - now.getTimezoneOffset() * 60000);
  return local.toISOString().slice(0, 10);
}

function daysInSelectedMonth() {
  const [year, month] = state.month.split("-").map(Number);
  return new Date(year, month, 0).getDate();
}

function daysPassedInSelectedMonth() {
  const now = new Date();
  const current = currentMonth();
  if (state.month < current) {
    return daysInSelectedMonth();
  }
  if (state.month > current) {
    return 1;
  }
  return Math.max(1, Math.min(now.getDate(), daysInSelectedMonth()));
}

function lastMonths(selectedMonth, count) {
  const [year, month] = selectedMonth.split("-").map(Number);
  return Array.from({ length: count }, (_, index) => {
    const date = new Date(year, month - 1 - (count - 1 - index), 1);
    return `${date.getFullYear()}-${String(date.getMonth() + 1).padStart(2, "0")}`;
  });
}

function debounce(callback, delay) {
  let timer;
  return (...args) => {
    window.clearTimeout(timer);
    timer = window.setTimeout(() => callback(...args), delay);
  };
}

let toastTimer;
function showToast(message, type = "success") {
  window.clearTimeout(toastTimer);
  els.toast.textContent = message;
  els.toast.className = `toast visible ${type === "error" ? "error" : ""}`;
  toastTimer = window.setTimeout(() => {
    els.toast.className = "toast";
  }, 3200);
}
