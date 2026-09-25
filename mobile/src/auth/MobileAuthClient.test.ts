import type { FetchLike } from "../api/VitlaneApiClient";
import { MobileAuthClient } from "./MobileAuthClient";
import type { AuthSessionStore, NativeAuthSession } from "./types";

const verifier = "v".repeat(43);
const challenge = "c".repeat(43);
const handoffCode = "h".repeat(43);
const now = Date.parse("2026-09-24T00:00:00Z");

const session: NativeAuthSession = {
  sessionToken: "s".repeat(43),
  expiresAt: "2026-10-01T00:00:00Z",
  user: {
    id: "d5338e1f-e667-4240-bfa3-d8d75506808a",
    email: "person@example.com",
    displayName: "Person",
    createdAt: "2026-09-01T00:00:00Z",
    marketingAdmin: false,
    phase5Operator: false,
  },
};

const response = (status: number, body: unknown): Response => ({
  status,
  ok: status >= 200 && status < 300,
  json: async () => body,
}) as Response;

function memoryStore(initial: NativeAuthSession | null = null): AuthSessionStore & {
  load: jest.Mock;
  save: jest.Mock;
  clear: jest.Mock;
} {
  let value = initial;
  return {
    load: jest.fn(async () => value),
    save: jest.fn(async (next: NativeAuthSession) => { value = next; }),
    clear: jest.fn(async () => { value = null; }),
  };
}

describe("MobileAuthClient", () => {
  it("rejects cleartext auth origins unless local development is explicit", () => {
    expect(() => new MobileAuthClient({
      baseUrl: "http://api.example.test",
      platform: "android",
    })).toThrow("must use HTTPS");
    expect(() => new MobileAuthClient({
      baseUrl: "http://127.0.0.1:8080",
      platform: "android",
      allowInsecureLocalhost: true,
    })).not.toThrow();
  });

  it("uses PKCE, exchanges only the one-time callback code, and stores the session", async () => {
    const store = memoryStore();
    const fetcher = jest.fn<ReturnType<FetchLike>, Parameters<FetchLike>>(
      async () => response(201, session),
    );
    const browser = {
      openAuthSessionAsync: jest.fn(async (_url: string, _redirectUrl: string) => ({
        type: "success" as const,
        url: `vitlane://auth/callback?code=${handoffCode}`,
      })),
    };
    const client = new MobileAuthClient({
      baseUrl: "https://api.example.test",
      fetch: fetcher,
      store,
      browser,
      pkce: async () => ({ verifier, challenge }),
      platform: "ios",
      now: () => now,
    });

    await expect(client.signInWithGoogle()).resolves.toEqual(session);

    const [startUrl, redirectUri] = browser.openAuthSessionAsync.mock.calls[0] ?? [];
    const parsedStart = new URL(startUrl ?? "");
    expect(parsedStart.origin + parsedStart.pathname).toBe(
      "https://api.example.test/api/v1/auth/mobile/google/start",
    );
    expect(parsedStart.searchParams.get("redirect_uri")).toBe(
      "vitlane://auth/callback",
    );
    expect(parsedStart.searchParams.get("code_challenge")).toBe(challenge);
    expect(redirectUri).toBe("vitlane://auth/callback");

    const [exchangeUrl, exchangeInit] = fetcher.mock.calls[0] ?? [];
    expect(exchangeUrl).toBe("https://api.example.test/api/v1/auth/mobile/exchange");
    expect(exchangeInit?.credentials).toBe("omit");
    expect(exchangeInit?.body).toBe(JSON.stringify({ code: handoffCode, verifier }));
    expect(String(exchangeInit?.body)).not.toContain(session.sessionToken);
    expect(store.save).toHaveBeenCalledWith(session);
  });

  it("rejects a callback for another deep-link target before exchange", async () => {
    const fetcher = jest.fn<ReturnType<FetchLike>, Parameters<FetchLike>>(
      async () => response(201, session),
    );
    const client = new MobileAuthClient({
      baseUrl: "https://api.example.test",
      fetch: fetcher,
      store: memoryStore(),
      browser: {
        openAuthSessionAsync: async () => ({
          type: "success",
          url: `vitlane://evil/callback?code=${handoffCode}`,
        }),
      },
      pkce: async () => ({ verifier, challenge }),
      platform: "android",
    });

    await expect(client.signInWithGoogle()).rejects.toMatchObject({
      code: "INVALID_AUTH_CALLBACK",
    });
    expect(fetcher).not.toHaveBeenCalled();
  });

  it("surfaces a sanitized provider reason without exchanging a code", async () => {
    const client = new MobileAuthClient({
      baseUrl: "https://api.example.test",
      fetch: async () => response(500, {}),
      store: memoryStore(),
      browser: {
        openAuthSessionAsync: async () => ({
          type: "success",
          url: "vitlane://auth/callback?error=AUTH_PROVIDER_FAILED",
        }),
      },
      pkce: async () => ({ verifier, challenge }),
      platform: "ios",
    });

    await expect(client.signInWithGoogle()).rejects.toMatchObject({
      code: "AUTH_PROVIDER_ERROR",
      reasonCode: "AUTH_PROVIDER_FAILED",
    });
  });

  it("requires explicit development mode before requesting a bearer session", async () => {
    const fetcher = jest.fn<ReturnType<FetchLike>, Parameters<FetchLike>>(
      async () => response(201, session),
    );
    const disabled = new MobileAuthClient({
      baseUrl: "https://api.example.test",
      fetch: fetcher,
      store: memoryStore(),
      platform: "ios",
    });

    await expect(disabled.signInWithDevelopmentProfile("empty-user"))
      .rejects.toMatchObject({ code: "DEVELOPMENT_AUTH_DISABLED" });
    expect(fetcher).not.toHaveBeenCalled();

    const enabledStore = memoryStore();
    const enabled = new MobileAuthClient({
      baseUrl: "https://api.example.test",
      fetch: fetcher,
      store: enabledStore,
      platform: "android",
      developmentSessionsEnabled: true,
    });
    await expect(enabled.signInWithDevelopmentProfile("empty-user"))
      .resolves.toEqual(session);
    expect(fetcher.mock.calls[0]?.[1]?.body).toBe(JSON.stringify({
      sessionMode: "bearer",
      profileKey: "empty-user",
    }));
    expect(enabledStore.save).toHaveBeenCalledWith(session);
  });

  it("keeps an explicitly enabled loopback Web review session in process memory only", async () => {
    const fetcher = jest.fn<ReturnType<FetchLike>, Parameters<FetchLike>>(
      async () => response(201, session),
    );
    const client = new MobileAuthClient({
      baseUrl: "http://127.0.0.1:18080",
      fetch: fetcher,
      platform: "web",
      developmentSessionsEnabled: true,
      allowInsecureLocalhost: true,
      now: () => now,
    });

    await expect(client.signInWithDevelopmentProfile("empty-user"))
      .resolves.toEqual(session);
    await expect(client.getAccessToken()).resolves.toBe(session.sessionToken);

    const remounted = new MobileAuthClient({
      baseUrl: "http://127.0.0.1:18080",
      fetch: fetcher,
      platform: "web",
      developmentSessionsEnabled: true,
      allowInsecureLocalhost: true,
      now: () => now,
    });
    await expect(remounted.getAccessToken()).resolves.toBeNull();
  });

  it("rejects Web development sessions for non-loopback API origins", async () => {
    const fetcher = jest.fn<ReturnType<FetchLike>, Parameters<FetchLike>>(
      async () => response(201, session),
    );
    const client = new MobileAuthClient({
      baseUrl: "https://api.example.test",
      fetch: fetcher,
      store: memoryStore(),
      platform: "web",
      developmentSessionsEnabled: true,
    });

    await expect(client.signInWithDevelopmentProfile("empty-user"))
      .rejects.toMatchObject({ code: "UNSUPPORTED_PLATFORM" });
    expect(fetcher).not.toHaveBeenCalled();
  });

  it("restores a native session through the canonical /me route", async () => {
    const store = memoryStore(session);
    const fetcher = jest.fn<ReturnType<FetchLike>, Parameters<FetchLike>>(
      async () => response(200, { user: { ...session.user, displayName: "Updated" } }),
    );
    const client = new MobileAuthClient({
      baseUrl: "https://api.example.test",
      fetch: fetcher,
      store,
      platform: "ios",
      now: () => now,
    });

    await expect(client.restoreSession()).resolves.toMatchObject({
      user: { displayName: "Updated" },
    });
    const [url, init] = fetcher.mock.calls[0] ?? [];
    expect(url).toBe("https://api.example.test/api/v1/me");
    expect(init?.headers).toMatchObject({
      Authorization: `Bearer ${session.sessionToken}`,
    });
  });

  it("clears expired sessions and never starts native auth on web", async () => {
    const expiredStore = memoryStore({
      ...session,
      expiresAt: "2026-09-23T00:00:00Z",
    });
    const browser = { openAuthSessionAsync: jest.fn() };
    const native = new MobileAuthClient({
      baseUrl: "https://api.example.test",
      fetch: async () => response(500, {}),
      store: expiredStore,
      browser,
      platform: "android",
      now: () => now,
    });
    await expect(native.getAccessToken()).resolves.toBeNull();
    expect(expiredStore.clear).toHaveBeenCalledTimes(1);

    const web = new MobileAuthClient({
      baseUrl: "https://api.example.test",
      fetch: async () => response(500, {}),
      store: memoryStore(),
      browser,
      platform: "web",
    });
    await expect(web.signInWithGoogle()).rejects.toMatchObject({
      code: "UNSUPPORTED_PLATFORM",
    });
    expect(browser.openAuthSessionAsync).not.toHaveBeenCalled();
  });
});
