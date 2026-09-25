import {
  MobileAuthError,
  type AuthUser,
  type NativeAuthSession,
} from "./types";

export function parseNativeAuthSession(value: unknown): NativeAuthSession {
  if (!isRecord(value)) invalidResponse();

  const sessionToken = stringField(value, "sessionToken");
  const expiresAt = stringField(value, "expiresAt");
  const userValue = value.user;
  if (
    !/^[A-Za-z0-9_-]{32,256}$/.test(sessionToken)
    || !validDate(expiresAt)
    || !isRecord(userValue)
  ) {
    invalidResponse();
  }

  const user: AuthUser = {
    id: stringField(userValue, "id"),
    email: stringField(userValue, "email"),
    displayName: stringField(userValue, "displayName", true),
    createdAt: stringField(userValue, "createdAt"),
    marketingAdmin: booleanField(userValue, "marketingAdmin"),
    phase5Operator: booleanField(userValue, "phase5Operator"),
    ...(typeof userValue.analyticsUserId === "string"
      ? { analyticsUserId: userValue.analyticsUserId }
      : {}),
  };
  if (!validDate(user.createdAt)) invalidResponse();

  return { sessionToken, expiresAt, user };
}

export function isSessionExpired(
  session: NativeAuthSession,
  nowMilliseconds = Date.now(),
): boolean {
  return Date.parse(session.expiresAt) <= nowMilliseconds;
}

function isRecord(value: unknown): value is Record<string, unknown> {
  return typeof value === "object" && value !== null && !Array.isArray(value);
}

function stringField(
  value: Record<string, unknown>,
  field: string,
  allowEmpty = false,
): string {
  const candidate = value[field];
  if (
    typeof candidate !== "string"
    || (!allowEmpty && candidate.trim() === "")
  ) {
    invalidResponse();
  }
  return candidate;
}

function booleanField(value: Record<string, unknown>, field: string): boolean {
  const candidate = value[field];
  if (typeof candidate !== "boolean") invalidResponse();
  return candidate;
}

function validDate(value: string): boolean {
  return Number.isFinite(Date.parse(value));
}

function invalidResponse(): never {
  throw new MobileAuthError(
    "INVALID_AUTH_RESPONSE",
    "The authentication server returned an invalid session.",
  );
}
