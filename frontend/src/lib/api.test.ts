import { describe, expect, it, vi } from "vitest";

import { APIClient, APIError } from "./api";

const user = { id: "11000000-0000-4000-8000-000000000001", displayName: "Пользователь" };
const household = {
  id: "22000000-0000-4000-8000-000000000001",
  name: "Дом",
  currencyCode: "RUB",
  role: "owner",
};

function jsonResponse(payload: unknown, status = 200, contentType = "application/json; charset=utf-8") {
  return new Response(JSON.stringify(payload), { status, headers: { "Content-Type": contentType } });
}

function sessionProvider(tokens: string[]) {
  let calls = 0;
  return {
    auth: {
      getSession: vi.fn(async () => {
        const token = tokens[Math.min(calls, tokens.length - 1)];
        calls += 1;
        return { data: { session: token ? { access_token: token } : null }, error: null };
      }),
    },
  };
}

function abortAwarePendingFetch(): typeof fetch {
  return vi.fn((_input: RequestInfo | URL, init?: RequestInit) => new Promise<Response>((_resolve, reject) => {
    const abort = () => reject(new DOMException("aborted", "AbortError"));
    if (init?.signal?.aborted) abort();
    else init?.signal?.addEventListener("abort", abort, { once: true });
  }));
}

describe("APIClient", () => {
  it("reads the current official session before every request and omits ambient credentials", async () => {
    const sessions = sessionProvider(["rotated-token-one", "rotated-token-two"]);
    const authorizations: string[] = [];
    const credentials: Array<RequestCredentials | undefined> = [];
    const fetcher = vi.fn<typeof fetch>(async (_input, init) => {
      authorizations.push(new Headers(init?.headers).get("Authorization") ?? "");
      credentials.push(init?.credentials);
      return jsonResponse({ households: [household] });
    });
    const client = new APIClient({ apiBaseUrl: "https://api.example.test", sessionProvider: sessions, fetcher });

    await client.listHouseholds();
    await client.listHouseholds();

    expect(sessions.auth.getSession).toHaveBeenCalledTimes(2);
    expect(authorizations).toEqual(["Bearer rotated-token-one", "Bearer rotated-token-two"]);
    expect(credentials).toEqual(["omit", "omit"]);
  });

  it("strictly parses bootstrap, list and create responses", async () => {
    const sessions = sessionProvider(["token"]);
    const responses = [
      jsonResponse({ user, households: [household] }),
      jsonResponse({ households: [household] }),
      jsonResponse(household, 201),
    ];
    const fetcher = vi.fn<typeof fetch>(async () => responses.shift() ?? jsonResponse({}, 500));
    const client = new APIClient({ apiBaseUrl: "", sessionProvider: sessions, fetcher });

    await expect(client.bootstrap()).resolves.toEqual({ user, households: [household] });
    await expect(client.listHouseholds()).resolves.toEqual([household]);
    await expect(client.createHousehold("Дом", "request-key")).resolves.toEqual(household);

    expect(fetcher.mock.calls[0]?.[1]?.body).toBe("{}");
    expect(new Headers(fetcher.mock.calls[2]?.[1]?.headers).get("Idempotency-Key")).toBe("request-key");
  });

  it.each(["text/plain", "text/application/json-evil", "application/jsonp"])(
    "rejects non-JSON media type %s",
    async (contentType) => {
      const client = new APIClient({
        apiBaseUrl: "",
        sessionProvider: sessionProvider(["token"]),
        fetcher: vi.fn(async () => jsonResponse({ households: [] }, 200, contentType)),
      });
      await expect(client.listHouseholds()).rejects.toMatchObject({ kind: "invalid_response" });
    },
  );

  it("maps 401, offline, abort and timeout without leaking token or underlying details", async () => {
    const secretToken = "secret-access-token-never-expose";
    const consoleLog = vi.spyOn(console, "log").mockImplementation(() => undefined);
    const unauthorized = new APIClient({
      apiBaseUrl: "",
      sessionProvider: sessionProvider([secretToken]),
      fetcher: vi.fn(async () => jsonResponse({ error: { code: "unauthorized", message: secretToken } }, 401)),
    });
    const unauthorizedError = await unauthorized.listHouseholds().catch((error: unknown) => error);
    expect(unauthorizedError).toMatchObject({ kind: "session_expired", status: 401 });

    const offline = new APIClient({
      apiBaseUrl: "",
      sessionProvider: sessionProvider([secretToken]),
      fetcher: vi.fn(async () => { throw new Error(`network ${secretToken}`); }),
    });
    const offlineError = await offline.listHouseholds().catch((error: unknown) => error);
    expect(offlineError).toMatchObject({ kind: "offline" });

    const controller = new AbortController();
    controller.abort();
    const aborted = new APIClient({
      apiBaseUrl: "",
      sessionProvider: sessionProvider([secretToken]),
      fetcher: abortAwarePendingFetch(),
    });
    const abortError = await aborted.listHouseholds(controller.signal).catch((error: unknown) => error);
    expect(abortError).toMatchObject({ kind: "aborted" });

    const timed = new APIClient({
      apiBaseUrl: "",
      sessionProvider: sessionProvider([secretToken]),
      fetcher: abortAwarePendingFetch(),
      timeoutMilliseconds: 5,
    });
    const timeoutError = await timed.listHouseholds().catch((error: unknown) => error);
    expect(timeoutError).toMatchObject({ kind: "timeout" });

    for (const error of [unauthorizedError, offlineError, abortError, timeoutError]) {
      expect(error).toBeInstanceOf(APIError);
      expect(String(error)).not.toContain(secretToken);
      expect((error as APIError).code ?? "").not.toContain(secretToken);
    }
    expect(consoleLog).not.toHaveBeenCalled();
    consoleLog.mockRestore();
  });

  it("rejects a missing session before fetch", async () => {
    const fetcher = vi.fn<typeof fetch>();
    const client = new APIClient({ apiBaseUrl: "", sessionProvider: sessionProvider([]), fetcher });
    await expect(client.bootstrap()).rejects.toMatchObject({ kind: "session_expired" });
    expect(fetcher).not.toHaveBeenCalled();
  });
});
