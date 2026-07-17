import { renderToStaticMarkup } from "react-dom/server";
import { describe, expect, it } from "vitest";

import type { Household } from "../../lib/api";
import type { FinanceAPI } from "../../lib/financeApi";
import { BackupImportPanel } from "./BackupImportPanel";
import { DirectoryPanel } from "./DirectoryPanel";
import { financeSectionsForRole, FinanceWorkspace } from "./FinanceWorkspace";
import { TransactionsPanel } from "./TransactionsPanel";

const owner: Household = { id: "22000000-0000-4000-8000-000000000001", name: "Дом", currencyCode: "RUB", role: "owner" };
const member: Household = { ...owner, role: "member" };
const admin: Household = { ...owner, role: "admin" };
const finance = {} as FinanceAPI;
const api = {} as never;

describe("financial responsive shell", () => {
  it("renders semantic keyboard-friendly navigation and bounded dashboard region", () => {
    const markup = renderToStaticMarkup(<FinanceWorkspace api={api} household={owner} offline={false} onSessionExpired={() => undefined} />);
    expect(markup).toContain('<nav class="finance-tabs" aria-label="Финансовые разделы">');
    expect(markup).toContain('aria-current="page"');
    expect(markup).toContain('aria-labelledby="dashboard-title"');
    expect(markup).toContain('type="date"');
    expect(markup).not.toContain("access_token");
  });

  it("hides account mutations from members while preserving the readable panel", () => {
    const memberMarkup = renderToStaticMarkup(<DirectoryPanel kind="accounts" finance={finance} household={member} offline={false} onSessionExpired={() => undefined} />);
    const ownerMarkup = renderToStaticMarkup(<DirectoryPanel kind="accounts" finance={finance} household={owner} offline={false} onSessionExpired={() => undefined} />);
    expect(memberMarkup).toContain("Просмотр доступен");
    expect(memberMarkup).not.toContain("Добавить счёт");
    expect(ownerMarkup).toContain("Добавить счёт");
    expect(ownerMarkup).toContain("<form");
  });

  it("keeps transaction mutation controls available to a member with accessible labels", () => {
    const markup = renderToStaticMarkup(<TransactionsPanel finance={finance} household={member} offline={false} onSessionExpired={() => undefined} />);
    expect(markup).toContain("Добавить операцию");
    expect(markup).toContain('aria-label="Фильтры операций"');
    expect(markup).toContain("Сумма, ₽");
    expect(markup).toContain("Корректировка баланса");
  });

  it("exposes backup import only to owners", () => {
    const ownerMarkup = renderToStaticMarkup(<FinanceWorkspace api={api} household={owner} offline={false} onSessionExpired={() => undefined} />);
    const adminMarkup = renderToStaticMarkup(<FinanceWorkspace api={api} household={admin} offline={false} onSessionExpired={() => undefined} />);
    const memberMarkup = renderToStaticMarkup(<FinanceWorkspace api={api} household={member} offline={false} onSessionExpired={() => undefined} />);

    expect(ownerMarkup).toContain(">Импорт</button>");
    expect(adminMarkup).not.toContain(">Импорт</button>");
    expect(memberMarkup).not.toContain(">Импорт</button>");
    expect(financeSectionsForRole("owner").some((section) => section.id === "import")).toBe(true);
    expect(financeSectionsForRole("admin").some((section) => section.id === "import")).toBe(false);
    expect(financeSectionsForRole("member").some((section) => section.id === "import")).toBe(false);
  });

  it("renders a safe accessible import form without secret material", () => {
    const markup = renderToStaticMarkup(
      <BackupImportPanel
        api={api}
        householdID={owner.id}
        offline={false}
        onSessionExpired={() => undefined}
        onImported={() => undefined}
        onOpenDashboard={() => undefined}
      />,
    );
    expect(markup).toContain('type="file"');
    expect(markup).toContain('type="month"');
    expect(markup).toContain("Только для полностью пустого пространства");
    expect(markup).toContain("действие необратимо");
    expect(markup).not.toContain("Import-Preview-Token");
    expect(markup).not.toContain("Idempotency-Key");
    expect(markup).not.toContain("confirmationToken");
    expect(markup).not.toContain("access_token");
  });
});
