export interface FrontendConfig {
  apiBaseUrl: string;
  supabaseUrl: string;
  supabasePublishableKey: string;
}

export type FrontendConfigResult =
  | { ok: true; config: FrontendConfig }
  | { ok: false; missingOrInvalid: readonly string[] };

type Environment = Record<string, string | boolean | undefined>;

const placeholderPattern = /(your[-_ ]?project|your[-_ ]?key|placeholder|change[-_ ]?me)/i;

function normalizedOrigin(raw: string, allowLoopbackHTTP: boolean): string | null {
  try {
    const parsed = new URL(raw);
    const loopback = parsed.hostname === "127.0.0.1" || parsed.hostname === "localhost" || parsed.hostname === "[::1]";
    if (
      parsed.username !== "" ||
      parsed.password !== "" ||
      parsed.search !== "" ||
      parsed.hash !== "" ||
      (parsed.pathname !== "" && parsed.pathname !== "/") ||
      (parsed.protocol !== "https:" && !(allowLoopbackHTTP && loopback && parsed.protocol === "http:"))
    ) {
      return null;
    }
    return parsed.origin;
  } catch {
    return null;
  }
}

function decodeLegacyKeyRole(key: string): string | null {
  const payload = key.split(".")[1];
  if (!payload) return null;
  try {
    const base64 = payload.replace(/-/g, "+").replace(/_/g, "/").padEnd(Math.ceil(payload.length / 4) * 4, "=");
    const parsed: unknown = JSON.parse(atob(base64));
    if (typeof parsed !== "object" || parsed === null) return null;
    const role = (parsed as Record<string, unknown>).role;
    return typeof role === "string" ? role : null;
  } catch {
    return null;
  }
}

function isSafePublishableKey(key: string): boolean {
  if (key.length < 24 || placeholderPattern.test(key)) return false;
  if (key.startsWith("sb_secret_") || /service[_-]?role/i.test(key)) return false;
  if (key.startsWith("sb_publishable_")) return true;
  return key.split(".").length === 3 && decodeLegacyKeyRole(key) === "anon";
}

function normalizeAPIBaseURL(raw: string): string | null {
  const value = raw.trim();
  if (value === "") return "";
  if (placeholderPattern.test(value)) return null;
  try {
    const parsed = new URL(value);
    const loopback = parsed.hostname === "127.0.0.1" || parsed.hostname === "localhost" || parsed.hostname === "[::1]";
    if (
      (parsed.protocol !== "https:" && !(loopback && parsed.protocol === "http:")) ||
      parsed.username !== "" ||
      parsed.password !== "" ||
      parsed.search !== "" ||
      parsed.hash !== ""
    ) {
      return null;
    }
    return value.replace(/\/+$/, "");
  } catch {
    return null;
  }
}

export function loadFrontendConfig(environment: Environment): FrontendConfigResult {
  const supabaseUrlRaw = typeof environment.VITE_SUPABASE_URL === "string" ? environment.VITE_SUPABASE_URL.trim() : "";
  const publishableKey =
    typeof environment.VITE_SUPABASE_PUBLISHABLE_KEY === "string"
      ? environment.VITE_SUPABASE_PUBLISHABLE_KEY.trim()
      : "";
  const apiBaseRaw = typeof environment.VITE_API_BASE_URL === "string" ? environment.VITE_API_BASE_URL : "";

  const supabaseUrl = placeholderPattern.test(supabaseUrlRaw) ? null : normalizedOrigin(supabaseUrlRaw, true);
  const apiBaseUrl = normalizeAPIBaseURL(apiBaseRaw);
  const invalid: string[] = [];
  if (!supabaseUrl) invalid.push("VITE_SUPABASE_URL");
  if (!isSafePublishableKey(publishableKey)) invalid.push("VITE_SUPABASE_PUBLISHABLE_KEY");
  if (apiBaseUrl === null) invalid.push("VITE_API_BASE_URL");

  if (invalid.length > 0 || !supabaseUrl || apiBaseUrl === null) {
    return { ok: false, missingOrInvalid: invalid };
  }
  return {
    ok: true,
    config: { apiBaseUrl, supabaseUrl, supabasePublishableKey: publishableKey },
  };
}

export const frontendConfig = loadFrontendConfig(import.meta.env);
