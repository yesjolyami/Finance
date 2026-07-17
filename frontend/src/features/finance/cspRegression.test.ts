import { readFileSync } from "node:fs";

import { describe, expect, it } from "vitest";

const sourceFiles = [
  "src/features/finance/DashboardPanel.tsx",
  "src/features/finance/DirectoryPanel.tsx",
  "src/features/finance/FinanceWorkspace.tsx",
  "src/features/finance/TransactionsPanel.tsx",
  "src/features/finance/BackupImportPanel.tsx",
];

describe("production CSP compatibility", () => {
  it("does not use inline styles or executable HTML escape hatches", () => {
    for (const path of sourceFiles) {
      const source = readFileSync(path, "utf8");
      expect(source, path).not.toMatch(/\bstyle\s*=\s*\{\{/);
      expect(source, path).not.toContain("dangerouslySetInnerHTML");
      expect(source, path).not.toMatch(/\beval\s*\(/);
      expect(source, path).not.toContain("new Function");
    }
  });

  it("uses semantic progress and allowlisted data colors", () => {
    const dashboard = readFileSync(sourceFiles[0]!, "utf8");
    const directory = readFileSync(sourceFiles[1]!, "utf8");
    expect(dashboard).toContain("<progress");
    expect(directory).toContain('type="color"');
    expect(directory).toContain("data-color=");
    expect(directory).not.toContain("style=");
  });
});
