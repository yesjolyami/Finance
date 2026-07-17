import { describe, expect, it } from "vitest";

import { loadFrontendConfig } from "./config";

const publishableKey = `sb_publishable_${"a".repeat(32)}`;

function environment(overrides: Record<string, string> = {}) {
  return {
    VITE_SUPABASE_URL: "https://finance.supabase.co",
    VITE_SUPABASE_PUBLISHABLE_KEY: publishableKey,
    VITE_API_BASE_URL: "",
    ...overrides,
  };
}

describe("loadFrontendConfig", () => {
  it("fails safely for missing, placeholder and privileged Supabase configuration", () => {
    expect(loadFrontendConfig({}).ok).toBe(false);
    expect(loadFrontendConfig(environment({ VITE_SUPABASE_URL: "https://your-project-ref.supabase.co" })).ok).toBe(false);
    expect(loadFrontendConfig(environment({ VITE_SUPABASE_PUBLISHABLE_KEY: "sb_publishable_your_key" })).ok).toBe(false);
    expect(loadFrontendConfig(environment({ VITE_SUPABASE_PUBLISHABLE_KEY: `sb_secret_${"x".repeat(32)}` })).ok).toBe(false);
    expect(loadFrontendConfig(environment({ VITE_SUPABASE_PUBLISHABLE_KEY: `service_role_${"x".repeat(32)}` })).ok).toBe(false);
  });

  it.each([
    "https://api.example.test",
    "http://127.0.0.1:8080",
    "http://localhost:8080",
    "http://[::1]:8080",
    "",
  ])("accepts a transport-safe API base URL: %s", (apiBaseUrl) => {
    expect(loadFrontendConfig(environment({ VITE_API_BASE_URL: apiBaseUrl })).ok).toBe(true);
  });

  it.each([
    "http://api.example.test",
    "https://user:password@api.example.test",
    "https://api.example.test?token=x",
    "https://api.example.test#fragment",
    "https://your-api-placeholder.example.test",
  ])("rejects an API base URL that could leak or misroute Bearer credentials: %s", (apiBaseUrl) => {
    const result = loadFrontendConfig(environment({ VITE_API_BASE_URL: apiBaseUrl }));
    expect(result.ok).toBe(false);
    if (!result.ok) expect(result.missingOrInvalid).toContain("VITE_API_BASE_URL");
  });
});
