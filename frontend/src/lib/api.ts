export interface UserProfile {
  id: string;
  displayName: string;
  usageMode: UsageMode;
  onboardingCompleted: boolean;
  primaryCurrencyCode: "RUB";
}

export type UsageMode = "personal" | "couple" | "family" | "custom";

export interface UserProfilePatch {
  displayName?: string;
  usageMode?: UsageMode;
  onboardingCompleted?: boolean;
  primaryCurrencyCode?: "RUB";
}

export type HouseholdRole = "owner" | "admin" | "member";

export interface Household {
  id: string;
  name: string;
  currencyCode: string;
  role: HouseholdRole;
}

export interface BootstrapResponse {
  user: UserProfile;
  households: Household[];
}

export type APIErrorKind = "session_expired" | "offline" | "timeout" | "aborted" | "invalid_response" | "request_failed";

export class APIError extends Error {
  public constructor(
    public readonly kind: APIErrorKind,
    public readonly status?: number,
    public readonly code?: string,
  ) {
    super(safeErrorMessage(kind));
    this.name = "APIError";
  }
}

interface SessionProvider {
  auth: {
    getSession: () => Promise<{
      data: { session: { access_token: string } | null };
      error: unknown;
    }>;
  };
}

interface APIClientOptions {
  apiBaseUrl: string;
  sessionProvider: SessionProvider;
  fetcher?: typeof fetch;
  timeoutMilliseconds?: number;
}

export interface APIRequestOptions {
  method: "GET" | "POST" | "PATCH";
  body?: object;
  idempotencyKey?: string;
  ifMatch?: string;
}

export interface APIResponse<T> {
  data: T;
  etag: string | null;
  replayed: boolean;
}

export interface BackupImportRequest {
  body: Blob;
  budgetMonth: string;
  previewToken?: string;
  idempotencyKey?: string;
  timeoutMilliseconds: number;
}

export interface BackupImportAPIResponse<T> {
  data: T;
  status: number;
  idempotencyReplayed: string | null;
}

const uuidPattern = /^[0-9a-f]{8}-[0-9a-f]{4}-[1-5][0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$/i;
const minimumImportTimeoutMilliseconds = 65_000;
const maximumImportTimeoutMilliseconds = 120_000;

function safeErrorMessage(kind: APIErrorKind): string {
  switch (kind) {
    case "session_expired":
      return "Сессия завершилась. Войдите снова.";
    case "offline":
      return "Не удалось связаться с API.";
    case "timeout":
      return "API не ответил вовремя.";
    case "aborted":
      return "Запрос отменён.";
    case "invalid_response":
      return "API вернул неожиданный ответ.";
    case "request_failed":
      return "Не удалось выполнить запрос.";
  }
}

function isRecord(value: unknown): value is Record<string, unknown> {
  return typeof value === "object" && value !== null && !Array.isArray(value);
}

function parseUser(value: unknown): UserProfile | null {
  if (
    !isRecord(value) || typeof value.id !== "string" || !uuidPattern.test(value.id) ||
    typeof value.displayName !== "string" ||
    (value.usageMode !== "personal" && value.usageMode !== "couple" && value.usageMode !== "family" && value.usageMode !== "custom") ||
    typeof value.onboardingCompleted !== "boolean" || value.primaryCurrencyCode !== "RUB"
  ) {
    return null;
  }
  return {
    id: value.id,
    displayName: value.displayName,
    usageMode: value.usageMode,
    onboardingCompleted: value.onboardingCompleted,
    primaryCurrencyCode: value.primaryCurrencyCode,
  };
}

function parseHousehold(value: unknown): Household | null {
  if (
    !isRecord(value) ||
    typeof value.id !== "string" ||
    !uuidPattern.test(value.id) ||
    typeof value.name !== "string" ||
    typeof value.currencyCode !== "string" ||
    (value.role !== "owner" && value.role !== "admin" && value.role !== "member")
  ) {
    return null;
  }
  return { id: value.id, name: value.name, currencyCode: value.currencyCode, role: value.role };
}

function parseHouseholds(value: unknown): Household[] | null {
  if (!Array.isArray(value)) return null;
  const households = value.map(parseHousehold);
  return households.every((household): household is Household => household !== null) ? households : null;
}

function parseBootstrap(value: unknown): BootstrapResponse | null {
  if (!isRecord(value)) return null;
  const user = parseUser(value.user);
  const households = parseHouseholds(value.households);
  return user && households ? { user, households } : null;
}

function parseHouseholdList(value: unknown): Household[] | null {
  return isRecord(value) ? parseHouseholds(value.households) : null;
}

function parseErrorCode(value: unknown): string | undefined {
  if (!isRecord(value) || !isRecord(value.error) || typeof value.error.code !== "string") return undefined;
  return value.error.code;
}

function isStrictJSONContentType(contentType: string): boolean {
  const parts = contentType.split(";").map((part) => part.trim());
  if (parts[0]?.toLowerCase() !== "application/json") return false;
  if (parts.length === 1) return true;
  if (parts.length !== 2) return false;
  const parameter = parts[1]?.split("=").map((part) => part.trim());
  return parameter?.length === 2 && parameter[0]?.toLowerCase() === "charset" && parameter[1]?.toLowerCase() === "utf-8";
}

function linkedAbortController(signal: AbortSignal | undefined, timeoutMilliseconds: number): {
  controller: AbortController;
  timedOut: () => boolean;
  cleanup: () => void;
} {
  const controller = new AbortController();
  let timeout = false;
  const abort = () => controller.abort();
  if (signal?.aborted) controller.abort();
  else signal?.addEventListener("abort", abort, { once: true });
  const timer = globalThis.setTimeout(() => {
    timeout = true;
    controller.abort();
  }, timeoutMilliseconds);
  return {
    controller,
    timedOut: () => timeout,
    cleanup: () => {
      globalThis.clearTimeout(timer);
      signal?.removeEventListener("abort", abort);
    },
  };
}

export class APIClient {
  private readonly fetcher: typeof fetch;
  private readonly timeoutMilliseconds: number;

  public constructor(private readonly options: APIClientOptions) {
    this.fetcher = options.fetcher ?? globalThis.fetch.bind(globalThis);
    this.timeoutMilliseconds = options.timeoutMilliseconds ?? 8_000;
  }

  public async bootstrap(signal?: AbortSignal): Promise<BootstrapResponse> {
    return (await this.requestJSON("/api/v1/session/bootstrap", parseBootstrap, { method: "POST", body: {} }, signal)).data;
  }

  public async listHouseholds(signal?: AbortSignal): Promise<Household[]> {
    return (await this.requestJSON("/api/v1/households", parseHouseholdList, { method: "GET" }, signal)).data;
  }

  public async updateProfile(patch: UserProfilePatch, signal?: AbortSignal): Promise<UserProfile> {
    return (await this.requestJSON("/api/v1/me", parseUser, { method: "PATCH", body: patch }, signal)).data;
  }

  public async createHousehold(name: string, idempotencyKey: string, signal?: AbortSignal): Promise<Household> {
    return (await this.requestJSON(
      "/api/v1/households",
      parseHousehold,
      { method: "POST", body: { name }, idempotencyKey },
      signal,
    )).data;
  }

  public async requestJSON<T>(
    path: string,
    parser: (value: unknown) => T | null,
    request: APIRequestOptions,
    signal?: AbortSignal,
  ): Promise<APIResponse<T>> {
    const sessionResult = await this.options.sessionProvider.auth.getSession();
    const token = sessionResult.data.session?.access_token;
    if (sessionResult.error || !token) throw new APIError("session_expired", 401);

    const linked = linkedAbortController(signal, this.timeoutMilliseconds);
    try {
      const headers = new Headers({ Accept: "application/json", Authorization: `Bearer ${token}` });
      if (request.body) headers.set("Content-Type", "application/json");
      if (request.idempotencyKey) headers.set("Idempotency-Key", request.idempotencyKey);
      if (request.ifMatch) headers.set("If-Match", request.ifMatch);
      const init: RequestInit = {
        method: request.method,
        headers,
        credentials: "omit",
        signal: linked.controller.signal,
      };
      if (request.body) init.body = JSON.stringify(request.body);
      const response = await this.fetcher(`${this.options.apiBaseUrl}${path}`, init);
      const contentType = response.headers.get("Content-Type") ?? "";
      if (!isStrictJSONContentType(contentType)) {
        if (response.status === 401) throw new APIError("session_expired", 401);
        throw new APIError("invalid_response", response.status);
      }
      let payload: unknown;
      try {
        payload = await response.json();
      } catch {
        throw new APIError("invalid_response", response.status);
      }
      if (!response.ok) {
        if (response.status === 401) throw new APIError("session_expired", 401);
        throw new APIError("request_failed", response.status, parseErrorCode(payload));
      }
      const parsed = parser(payload);
      if (parsed === null) throw new APIError("invalid_response", response.status);
      return {
        data: parsed,
        etag: response.headers.get("ETag"),
        replayed: response.headers.get("Idempotency-Replayed")?.toLowerCase() === "true",
      };
    } catch (error) {
      if (error instanceof APIError) throw error;
      if (linked.controller.signal.aborted) {
        throw new APIError(linked.timedOut() ? "timeout" : "aborted");
      }
      throw new APIError("offline");
    } finally {
      linked.cleanup();
    }
  }

  public async requestBackupImportJSON<T>(
    path: string,
    parser: (value: unknown) => T | null,
    request: BackupImportRequest,
    signal?: AbortSignal,
  ): Promise<BackupImportAPIResponse<T>> {
    if (
      request.timeoutMilliseconds < minimumImportTimeoutMilliseconds ||
      request.timeoutMilliseconds > maximumImportTimeoutMilliseconds
    ) {
      throw new APIError("invalid_response");
    }

    const sessionResult = await this.options.sessionProvider.auth.getSession();
    const token = sessionResult.data.session?.access_token;
    if (sessionResult.error || !token) throw new APIError("session_expired", 401);

    const linked = linkedAbortController(signal, request.timeoutMilliseconds);
    try {
      const headers = new Headers({
        Accept: "application/json",
        Authorization: `Bearer ${token}`,
        "Content-Type": "application/json",
        "Import-Budget-Month": request.budgetMonth,
      });
      if (request.previewToken !== undefined) headers.set("Import-Preview-Token", request.previewToken);
      if (request.idempotencyKey !== undefined) headers.set("Idempotency-Key", request.idempotencyKey);

      const response = await this.fetcher(`${this.options.apiBaseUrl}${path}`, {
        method: "POST",
        headers,
        credentials: "omit",
        body: request.body,
        signal: linked.controller.signal,
      });
      if (!isStrictJSONContentType(response.headers.get("Content-Type") ?? "")) {
        if (response.status === 401) throw new APIError("session_expired", 401);
        throw new APIError("invalid_response", response.status);
      }

      let payload: unknown;
      try {
        payload = await response.json();
      } catch {
        throw new APIError("invalid_response", response.status);
      }
      if (!response.ok) {
        if (response.status === 401) throw new APIError("session_expired", 401);
        throw new APIError("request_failed", response.status, parseErrorCode(payload));
      }

      const parsed = parser(payload);
      if (parsed === null) throw new APIError("invalid_response", response.status);
      return {
        data: parsed,
        status: response.status,
        idempotencyReplayed: response.headers.get("Idempotency-Replayed"),
      };
    } catch (error) {
      if (error instanceof APIError) throw error;
      if (linked.controller.signal.aborted) {
        throw new APIError(linked.timedOut() ? "timeout" : "aborted");
      }
      throw new APIError("offline");
    } finally {
      linked.cleanup();
    }
  }
}
