const state = {
  accounts: [],
  categories: [],
  transactions: [],
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
  els.categoryForm = document.querySelector("#categoryForm");
  els.accountSelect = document.querySelector("#accountSelect");
  els.accountFilter = document.querySelector("#accountFilter");
  els.accountGrid = document.querySelector("#accountGrid");
  els.accountScopeLabel = document.querySelector("#accountScopeLabel");
  els.categorySelect = document.querySelector("#categorySelect");
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
  els.categoryForm.addEventListener("submit", handleCategorySubmit);
  els.quickCategoryForm.addEventListener("submit", handleQuickCategorySubmit);

  els.transactionForm.querySelectorAll("input[name='type']").forEach((input) => {
    input.addEventListener("change", () => {
      updateTransactionCategoryOptions(input.value);
      syncQuickCategoryState();
    });
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

  els.accountGrid.addEventListener("click", (event) => {
    const button = event.target.closest("[data-account-filter]");
    if (!button) return;
    setAccountFilter(button.dataset.accountFilter);
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
    const button = event.target.closest("[data-delete-transaction]");
    if (!button) return;
    await deleteTransaction(button.dataset.deleteTransaction);
  });

  els.categoryList.addEventListener("click", async (event) => {
    const button = event.target.closest("[data-delete-category]");
    if (!button) return;
    await deleteCategory(button.dataset.deleteCategory);
  });

  els.exportButton.addEventListener("click", handleExport);
  els.importFile.addEventListener("change", importBackup);
  window.addEventListener("resize", debounce(renderTrendChart, 120));
}

async function refreshData() {
  try {
    const [accountsPayload, categoriesPayload, transactionsPayload] = await Promise.all([
      api("/api/accounts"),
      api("/api/categories"),
      api("/api/transactions"),
    ]);
    state.accounts = accountsPayload.accounts;
    state.categories = categoriesPayload.categories;
    state.transactions = transactionsPayload.transactions;
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

async function handleTransactionSubmit(event) {
  event.preventDefault();
  const form = new FormData(els.transactionForm);
  const type = form.get("type");
  const accountId = form.get("accountId");
  const amountCents = parseAmountToCents(form.get("amount"));
  const categoryId = form.get("categoryId");
  const date = form.get("date");
  const note = String(form.get("note") || "").trim();

  if (!amountCents) {
    showToast("Введите сумму больше нуля", "error");
    return;
  }

  try {
    await api("/api/transactions", {
      method: "POST",
      body: JSON.stringify({ type, accountId, amountCents, categoryId, date, note }),
    });
    els.transactionForm.reset();
    document.querySelector("input[name='type'][value='expense']").checked = true;
    document.querySelector("#dateInput").value = todayInputValue();
    updateAccountOptions(accountId);
    updateTransactionCategoryOptions("expense");
    await refreshData();
    showToast("Операция добавлена");
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
    await createCategory(payload);
    els.categoryForm.reset();
    els.categoryColor.value = "#5d704d";
    els.categoryBudget.disabled = false;
    syncQuickCategoryState();
    showToast("Категория создана");
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
  const header = ["Дата", "Счет", "Тип", "Категория", "Сумма", "Комментарий"];
  const csvRows = rows.map((transaction) => {
    const account = findAccount(transaction.accountId);
    const category = findCategory(transaction.categoryId);
    const amount = (transaction.amountCents / 100) * (transaction.type === "expense" ? -1 : 1);
    return [
      transaction.date,
      account?.name || "Без счета",
      transaction.type === "income" ? "Доход" : "Расход",
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
  const balance = income - expense;
  const bgColor = getThemeColor("--bg");
  const surfaceColor = getThemeColor("--surface");
  const inkColor = getThemeColor("--ink");
  const mutedColor = getThemeColor("--muted");
  const lineColor = getThemeColor("--line");
  const incomeColor = getThemeColor("--accent");
  const expenseColor = getThemeColor("--rose");
  const expenses = monthTransactions.filter((item) => item.type === "expense");
  const totalExpense = expenses.reduce((sum, item) => sum + item.amountCents, 0);
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
  const height = Math.max(760, 560 + categories.length * 54);
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

  drawReportText(ctx, "Расходы по категориям", 64, 390, 28, inkColor, 900);
  if (!categories.length) {
    drawReportText(ctx, "За выбранный месяц расходов пока нет.", 64, 438, 22, mutedColor, 600);
  } else {
    categories.forEach((item, index) => {
      const y = 438 + index * 54;
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
  drawReportText(ctx, `Сформировано локально: ${generated}`, 64, height - 54, 18, mutedColor, 600);

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
  updateTransactionCategoryOptions(getSelectedTransactionType());
  updateAccountFilterOptions();
  updateFilterCategoryOptions();
  syncQuickCategoryState();
  renderAccounts();
  renderSummary();
  renderCategoryBreakdown();
  renderBudgets();
  renderCategories();
  renderTransactions();
  renderTrendChart();
}

function renderSummary() {
  const monthTransactions = scopedTransactionsForMonth(state.month);
  const income = sumByType(monthTransactions, "income");
  const expense = sumByType(monthTransactions, "expense");
  const balance = income - expense;
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
    state.filters.account === "all"
      ? "Все счета за месяц"
      : `${getAccountScopeLabel()} за месяц`;
  els.expenseHint.textContent = `Средний расход: ${formatMoney(dailyAverage)} в день`;
  els.budgetHint.textContent = budget
    ? `${formatMoney(expense)} из ${formatMoney(budget)}`
    : "Добавьте лимиты в категориях";
  els.savingsRate.textContent = `${Number.isFinite(savingsPercent) ? savingsPercent : 0}% сохранено`;
}

function renderCategoryBreakdown() {
  const expenses = scopedTransactionsForMonth(state.month).filter((item) => item.type === "expense");
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
    (item) => item.type === "expense"
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
  const accountCards = [
    {
      id: "all",
      name: "Все счета",
      color: "#302f2b",
      transactions: transactionsForMonth(state.month),
    },
    ...state.accounts.map((account) => ({
      ...account,
      transactions: transactionsForMonth(state.month).filter(
        (transaction) => transaction.accountId === account.id
      ),
    })),
  ];

  els.accountGrid.replaceChildren();
  accountCards.forEach((account) => {
    const income = sumByType(account.transactions, "income");
    const expense = sumByType(account.transactions, "expense");
    const balance = income - expense;
    const button = document.createElement("button");
    button.className = "account-card";
    button.type = "button";
    button.dataset.accountFilter = account.id;
    button.style.setProperty("--account-color", account.color);
    button.setAttribute("aria-pressed", String(state.filters.account === account.id));

    const name = document.createElement("span");
    name.className = "account-name";
    name.textContent = account.name;

    const amount = document.createElement("strong");
    amount.textContent = formatMoney(balance);

    const meta = document.createElement("small");
    meta.textContent = `${formatMoney(income)} доход · ${formatMoney(expense)} расход`;

    button.append(name, amount, meta);
    els.accountGrid.append(button);
  });

  els.accountScopeLabel.textContent = getAccountScopeLabel();
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

    item.append(swatch, content, button);
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
    title.textContent = transaction.note || category?.name || "Операция";
    const meta = document.createElement("span");
    meta.className = "transaction-meta";
    meta.textContent = `${formatDate(transaction.date)} · ${account?.name || "Без счета"} · ${
      category?.name || "Без категории"
    }`;
    content.append(title, meta);

    const actions = document.createElement("div");
    actions.className = "transaction-actions";

    const amount = document.createElement("strong");
    amount.className = `transaction-amount ${transaction.type}`;
    amount.textContent = `${transaction.type === "income" ? "+" : "-"}${formatMoney(
      transaction.amountCents
    )}`;

    const button = document.createElement("button");
    button.className = "button danger";
    button.type = "button";
    button.dataset.deleteTransaction = transaction.id;
    button.textContent = "Удалить";
    button.setAttribute("aria-label", "Удалить операцию");

    actions.append(amount, button);
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
    ...state.accounts.map((account) => new Option(account.name, account.id))
  );
  if (state.accounts.some((account) => account.id === current)) {
    els.accountSelect.value = current;
    return;
  }
  if (state.accounts.length) {
    els.accountSelect.value = state.accounts[0].id;
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
    state.accounts.map((account) => new Option(account.name, account.id))
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
  const isIncome = type === "income";
  els.quickCategoryTypeBadge.textContent = isIncome ? "Для доходов" : "Для расходов";
  els.quickBudgetField.hidden = isIncome;
  els.quickCategoryBudget.disabled = isIncome;
}

function setAccountFilter(accountId) {
  state.filters.account = accountId;
  localStorage.setItem("finance-account", accountId);
  els.accountFilter.value = accountId;
  renderAccounts();
  renderSummary();
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
    els.themeToggle.textContent = isDark ? "Светлая тема" : "Темная тема";
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

function scopedTransactionsForMonth(month) {
  return transactionsForMonth(month).filter(
    (transaction) =>
      state.filters.account === "all" || transaction.accountId === state.filters.account
  );
}

function sumByType(transactions, type) {
  return transactions
    .filter((transaction) => transaction.type === type)
    .reduce((sum, transaction) => sum + transaction.amountCents, 0);
}

function findAccount(id) {
  return state.accounts.find((account) => account.id === id);
}

function findCategory(id) {
  return state.categories.find((category) => category.id === id);
}

function getAccountScopeLabel() {
  if (state.filters.account === "all") {
    return "Показаны все счета";
  }
  return findAccount(state.filters.account)?.name || "Выбранный счет";
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

function formatMoney(cents) {
  return money.format(cents / 100);
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
