import { useEffect, useMemo, useState } from "react";

import type { Household } from "../../lib/api";
import { FinanceAPI } from "../../lib/financeApi";
import type { APIClient } from "../../lib/api";
import { DashboardPanel } from "./DashboardPanel";
import { DirectoryPanel } from "./DirectoryPanel";
import { TransactionsPanel } from "./TransactionsPanel";
import { BackupImportPanel } from "./BackupImportPanel";
import { nextFinanceRevision } from "./backupImportModel";

type FinanceSection = "dashboard" | "transactions" | "accounts" | "categories" | "import";

interface FinanceWorkspaceProps {
  api: APIClient;
  household: Household;
  offline: boolean;
  onSessionExpired: () => void;
  initialSection?: FinanceSection;
}

const sections: ReadonlyArray<{ id: FinanceSection; label: string; short: string; ownerOnly?: boolean }> = [
  { id: "dashboard", label: "Обзор", short: "Обзор" },
  { id: "transactions", label: "Операции", short: "Операции" },
  { id: "accounts", label: "Счета", short: "Счета" },
  { id: "categories", label: "Категории", short: "Категории" },
  { id: "import", label: "Импорт backup v5", short: "Импорт", ownerOnly: true },
];

export function financeSectionsForRole(role: Household["role"]): ReadonlyArray<{ id: FinanceSection; label: string; short: string; ownerOnly?: boolean }> {
  return sections.filter((item) => !item.ownerOnly || role === "owner");
}

export function FinanceWorkspace({ api, household, offline, onSessionExpired, initialSection = "dashboard" }: FinanceWorkspaceProps) {
  const finance = useMemo(() => new FinanceAPI(api), [api]);
  const [section, setSection] = useState<FinanceSection>(initialSection);
  const [financeRevision, setFinanceRevision] = useState(0);
  const visibleSections = financeSectionsForRole(household.role);

  useEffect(() => {
    if (section === "import" && household.role !== "owner") setSection("dashboard");
  }, [household.role, section]);

  return (
    <div className="finance-workspace">
      <div className="finance-heading">
        <div>
          <p className="eyebrow">{household.currencyCode} · {household.role === "owner" ? "Владелец" : household.role === "admin" ? "Администратор" : "Участник"}</p>
          <h2>{household.name}</h2>
        </div>
        <span className="live-indicator"><i aria-hidden="true" />Финансовый контур</span>
      </div>

      <nav className="finance-tabs" aria-label="Финансовые разделы">
        {visibleSections.map((item) => (
          <button
            key={item.id}
            type="button"
            aria-current={section === item.id ? "page" : undefined}
            onClick={() => setSection(item.id)}
          >
            {item.short}
          </button>
        ))}
      </nav>

      {section === "dashboard" && <DashboardPanel key={`dashboard-${financeRevision}`} finance={finance} householdID={household.id} offline={offline} onSessionExpired={onSessionExpired} />}
      {section === "transactions" && <TransactionsPanel key={`transactions-${financeRevision}`} finance={finance} household={household} offline={offline} onSessionExpired={onSessionExpired} />}
      {section === "accounts" && <DirectoryPanel key={`accounts-${financeRevision}`} kind="accounts" finance={finance} household={household} offline={offline} onSessionExpired={onSessionExpired} />}
      {section === "categories" && <DirectoryPanel key={`categories-${financeRevision}`} kind="categories" finance={finance} household={household} offline={offline} onSessionExpired={onSessionExpired} />}
      {section === "import" && household.role === "owner" && (
        <BackupImportPanel
          api={api}
          householdID={household.id}
          offline={offline}
          onSessionExpired={onSessionExpired}
          onImported={() => setFinanceRevision((current) => nextFinanceRevision(current))}
          onOpenDashboard={() => setSection("dashboard")}
        />
      )}
    </div>
  );
}
