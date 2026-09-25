export type AuthUser = {
  analyticsUserId?: string;
  id: string;
  email: string;
  displayName: string;
  createdAt: string;
  marketingAdmin: boolean;
  phase5Operator: boolean;
};

/**
 * The opaque session token is a server-issued credential. Callers must pass
 * this value only through the Authorization header and must never persist it
 * outside an AuthSessionStore.
 */
export type NativeAuthSession = {
  sessionToken: string;
  expiresAt: string;
  user: AuthUser;
};

export type AuthCapabilities = {
  googleEnabled: boolean;
  mobileGoogleEnabled: boolean;
  localReviewEnabled: boolean;
  localReviewSeeded: boolean;
};

export interface AuthSessionStore {
  load(): Promise<NativeAuthSession | null>;
  save(session: NativeAuthSession): Promise<void>;
  clear(): Promise<void>;
}

export type MobileAuthErrorCode =
  | "AUTH_CANCELLED"
  | "AUTH_DISMISSED"
  | "AUTH_PROVIDER_ERROR"
  | "DEVELOPMENT_AUTH_DISABLED"
  | "INVALID_AUTH_CALLBACK"
  | "INVALID_AUTH_RESPONSE"
  | "SECURE_STORAGE_UNAVAILABLE"
  | "UNSUPPORTED_PLATFORM";

export class MobileAuthError extends Error {
  public constructor(
    public readonly code: MobileAuthErrorCode,
    message: string,
    public readonly reasonCode?: string,
    public readonly cause?: unknown,
  ) {
    super(message);
    this.name = "MobileAuthError";
  }
}
