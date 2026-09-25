import * as WebBrowser from "expo-web-browser";
import { Platform } from "react-native";

import {
  type FetchLike,
  VitlaneApiClient,
  VitlaneApiError,
} from "../api/VitlaneApiClient";
import { createPKCEPair, type PKCEPair } from "./pkce";
import { isSessionExpired, parseNativeAuthSession } from "./session";
import { SecureSessionStore } from "./SecureSessionStore";
import {
  MobileAuthError,
  type AuthCapabilities,
  type AuthSessionStore,
  type AuthUser,
  type NativeAuthSession,
} from "./types";
import { EphemeralSessionStore } from "./EphemeralSessionStore";

const DEFAULT_REDIRECT_URI = "vitlane://auth/callback";

type AuthBrowserResult =
  | { type: "success"; url: string }
  | { type: string };

export type AuthBrowser = {
  openAuthSessionAsync(
    url: string,
    redirectUrl: string,
  ): Promise<AuthBrowserResult>;
};

export type MobileAuthClientOptions = {
  baseUrl: string;
  redirectUri?: string;
  fetch?: FetchLike;
  store?: AuthSessionStore;
  browser?: AuthBrowser;
  pkce?: () => Promise<PKCEPair>;
  platform?: string;
  now?: () => number;
  /** Must be explicitly true before the local-only bearer endpoint is used. */
  developmentSessionsEnabled?: boolean;
  /** Allows cleartext transport only to loopback hosts in a development build. */
  allowInsecureLocalhost?: boolean;
};

type SessionEnvelope = {
  sessionToken: string;
  expiresAt: string;
  user: AuthUser;
};

type UserEnvelope = { user: AuthUser };

/**
 * Authentication boundary. Native builds use PKCE and SecureStore; the
 * explicit loopback Web review path uses a process-local session only.
 */
export class MobileAuthClient {
  private readonly baseUrl: string;
  private readonly redirectUri: string;
  private readonly store: AuthSessionStore;
  private readonly browser: AuthBrowser;
  private readonly pkce: () => Promise<PKCEPair>;
  private readonly platform: string;
  private readonly now: () => number;
  private readonly developmentSessionsEnabled: boolean;
  private readonly publicApi: VitlaneApiClient;
  private readonly authenticatedApi: VitlaneApiClient;

  public constructor(options: MobileAuthClientOptions) {
    this.baseUrl = normalizeBaseUrl(options.baseUrl, options.allowInsecureLocalhost === true);
    this.redirectUri = normalizeRedirectUri(options.redirectUri ?? DEFAULT_REDIRECT_URI);
    this.platform = options.platform ?? Platform.OS;
    this.developmentSessionsEnabled = options.developmentSessionsEnabled === true;
    this.store = options.store ?? (
      this.localWebDevelopmentSessionAllowed()
        ? new EphemeralSessionStore()
        : new SecureSessionStore({ platform: this.platform })
    );
    this.browser = options.browser ?? WebBrowser;
    this.pkce = options.pkce ?? createPKCEPair;
    this.now = options.now ?? Date.now;
    this.publicApi = new VitlaneApiClient({
      baseUrl: this.baseUrl,
      ...(options.fetch ? { fetch: options.fetch } : {}),
      credentials: "omit",
    });
    this.authenticatedApi = new VitlaneApiClient({
      baseUrl: this.baseUrl,
      ...(options.fetch ? { fetch: options.fetch } : {}),
      accessToken: () => this.getAccessToken(),
      credentials: "omit",
    });
  }

  public isSupported(): boolean {
    return this.platform === "ios" || this.platform === "android";
  }

  public getApiClient(): VitlaneApiClient {
    return this.authenticatedApi;
  }

  public async getCapabilities(): Promise<AuthCapabilities> {
    return this.publicApi.request<AuthCapabilities>("/api/v1/auth/capabilities");
  }

  public async signInWithGoogle(): Promise<NativeAuthSession> {
    this.requireNativePlatform();
    const pair = await this.pkce();
    validatePKCEPair(pair);
    const startUrl = new URL("/api/v1/auth/mobile/google/start", `${this.baseUrl}/`);
    startUrl.searchParams.set("redirect_uri", this.redirectUri);
    startUrl.searchParams.set("code_challenge", pair.challenge);

    const result = await this.browser.openAuthSessionAsync(
      startUrl.toString(),
      this.redirectUri,
    );
    if (result.type === "cancel") {
      throw new MobileAuthError("AUTH_CANCELLED", "Authentication was cancelled.");
    }
    if (result.type !== "success" || !("url" in result)) {
      throw new MobileAuthError("AUTH_DISMISSED", "Authentication did not complete.");
    }

    const code = readCallbackCode(result.url, this.redirectUri);
    const response = await this.publicApi.request<SessionEnvelope>(
      "/api/v1/auth/mobile/exchange",
      { method: "POST", body: { code, verifier: pair.verifier } },
    );
    const session = parseNativeAuthSession(response);
    await this.store.save(session);
    return session;
  }

  public async signInWithDevelopmentProfile(
    profileKey?: string,
  ): Promise<NativeAuthSession> {
    if (!this.developmentSessionsEnabled) {
      throw new MobileAuthError(
        "DEVELOPMENT_AUTH_DISABLED",
        "Development bearer sessions are disabled.",
      );
    }
    if (!this.isSupported() && !this.localWebDevelopmentSessionAllowed()) {
      throw new MobileAuthError(
        "UNSUPPORTED_PLATFORM",
        "Development bearer sessions on the web require an explicit loopback review origin.",
      );
    }
    const normalizedProfile = profileKey?.trim();
    const response = await this.publicApi.request<SessionEnvelope>(
      "/api/v1/dev/auth/session",
      {
        method: "POST",
        body: {
          sessionMode: "bearer",
          ...(normalizedProfile ? { profileKey: normalizedProfile } : {}),
        },
      },
    );
    const session = parseNativeAuthSession(response);
    await this.store.save(session);
    return session;
  }

  public async getStoredSession(): Promise<NativeAuthSession | null> {
    const session = await this.store.load();
    if (!session) return null;
    if (!isSessionExpired(session, this.now())) return session;
    await this.store.clear();
    return null;
  }

  public async getAccessToken(): Promise<string | null> {
    return (await this.getStoredSession())?.sessionToken ?? null;
  }

  /** Revalidates a stored token and refreshes its non-secret user projection. */
  public async restoreSession(): Promise<NativeAuthSession | null> {
    const session = await this.getStoredSession();
    if (!session) return null;
    try {
      const response = await this.authenticatedApi.request<UserEnvelope>(
        "/api/v1/me",
      );
      const refreshed = parseNativeAuthSession({ ...session, user: response.user });
      await this.store.save(refreshed);
      return refreshed;
    } catch (error) {
      if (error instanceof VitlaneApiError && error.status === 401) {
        await this.store.clear();
        return null;
      }
      throw error;
    }
  }

  public async logout(): Promise<void> {
    const token = await this.getAccessToken();
    try {
      if (token) {
        await this.authenticatedApi.request<void>("/api/v1/auth/logout", {
          method: "POST",
        });
      }
    } finally {
      await this.store.clear();
    }
  }

  public async logoutAll(): Promise<void> {
    const token = await this.getAccessToken();
    try {
      if (token) {
        await this.authenticatedApi.request<void>("/api/v1/auth/logout-all", {
          method: "POST",
        });
      }
    } finally {
      await this.store.clear();
    }
  }

  private requireNativePlatform(): void {
    if (this.isSupported()) return;
    throw new MobileAuthError(
      "UNSUPPORTED_PLATFORM",
      "The native authentication flow is available only on iOS and Android.",
    );
  }

  private localWebDevelopmentSessionAllowed(): boolean {
    if (this.platform !== "web" || !this.developmentSessionsEnabled) return false;
    const hostname = new URL(this.baseUrl).hostname;
    return ["localhost", "127.0.0.1", "[::1]"].includes(hostname);
  }
}

function readCallbackCode(callbackUrl: string, redirectUri: string): string {
  let callback: URL;
  try {
    callback = new URL(callbackUrl);
  } catch (error) {
    throw invalidCallback(error);
  }
  const expected = new URL(redirectUri);
  if (
    callback.protocol !== expected.protocol
    || callback.hostname !== expected.hostname
    || callback.port !== expected.port
    || callback.pathname !== expected.pathname
    || callback.username !== expected.username
    || callback.password !== expected.password
    || callback.hash !== ""
  ) {
    throw invalidCallback();
  }
  const codes = callback.searchParams.getAll("code");
  const errors = callback.searchParams.getAll("error");
  if (errors.length === 1 && codes.length === 0) {
    const reason = normalizeReasonCode(errors[0]);
    throw new MobileAuthError(
      "AUTH_PROVIDER_ERROR",
      "The identity provider could not complete authentication.",
      reason,
    );
  }
  if (codes.length !== 1 || errors.length !== 0) throw invalidCallback();
  const code = codes[0]?.trim() ?? "";
  if (!/^[A-Za-z0-9_-]{32,256}$/.test(code)) throw invalidCallback();
  return code;
}

function normalizeReasonCode(value: string | undefined): string | undefined {
  const normalized = value?.trim();
  return normalized && /^[A-Z0-9_]{1,80}$/.test(normalized)
    ? normalized
    : undefined;
}

function invalidCallback(cause?: unknown): MobileAuthError {
  return new MobileAuthError(
    "INVALID_AUTH_CALLBACK",
    "The authentication callback was invalid.",
    undefined,
    cause,
  );
}

function validatePKCEPair(pair: PKCEPair): void {
  const base64Url = /^[A-Za-z0-9_-]{43}$/;
  if (!base64Url.test(pair.verifier) || !base64Url.test(pair.challenge)) {
    throw new MobileAuthError(
      "INVALID_AUTH_CALLBACK",
      "Unable to create a valid authentication challenge.",
    );
  }
}

function normalizeBaseUrl(value: string, allowInsecureLocalhost: boolean): string {
  const normalized = value.trim().replace(/\/+$/, "");
  const parsed = new URL(normalized);
  if (
    !["http:", "https:"].includes(parsed.protocol)
    || parsed.username
    || parsed.password
    || parsed.pathname !== "/"
    || parsed.search
    || parsed.hash
  ) {
    throw new TypeError("Authentication base URL must be an HTTP(S) origin");
  }
  const localDevelopmentOrigin = allowInsecureLocalhost
    && parsed.protocol === "http:"
    && ["localhost", "127.0.0.1", "[::1]"].includes(parsed.hostname);
  if (parsed.protocol !== "https:" && !localDevelopmentOrigin) {
    throw new TypeError("Authentication base URL must use HTTPS outside local development");
  }
  return normalized;
}

function normalizeRedirectUri(value: string): string {
  const parsed = new URL(value.trim());
  if (
    ["http:", "https:"].includes(parsed.protocol)
    || !parsed.protocol
    || !parsed.hostname
    || parsed.username
    || parsed.password
    || parsed.search
    || parsed.hash
  ) {
    throw new TypeError("Mobile redirect URI must be an exact custom-scheme URL");
  }
  return parsed.toString();
}
