import { useCallback, useEffect, useState } from "react";

import { MobileAuthClient } from "./MobileAuthClient";
import type { AuthCapabilities, NativeAuthSession } from "./types";

export type AuthSessionController = {
  readonly status: "restoring" | "signed_out" | "signed_in";
  readonly session: NativeAuthSession | null;
  readonly capabilities: AuthCapabilities | null;
  readonly busy: boolean;
  readonly error: string | null;
  signInWithGoogle(): Promise<NativeAuthSession | null>;
  signInWithDevelopmentProfile(profileKey?: string): Promise<NativeAuthSession | null>;
  signOut(): Promise<void>;
  restore(): Promise<NativeAuthSession | null>;
  clearError(): void;
};

export function useAuthSession(client: MobileAuthClient): AuthSessionController {
  const [status, setStatus] = useState<AuthSessionController["status"]>("restoring");
  const [session, setSession] = useState<NativeAuthSession | null>(null);
  const [capabilities, setCapabilities] = useState<AuthCapabilities | null>(null);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);

  const restore = useCallback(async () => {
    setStatus("restoring");
    setError(null);
    const [restored, available] = await Promise.allSettled([
      client.restoreSession(),
      client.getCapabilities(),
    ]);
    if (available.status === "fulfilled") setCapabilities(available.value);
    if (restored.status === "fulfilled" && restored.value) {
      setSession(restored.value);
      setStatus("signed_in");
      return restored.value;
    }
    setSession(null);
    setStatus("signed_out");
    if (restored.status === "rejected") setError(message(restored.reason));
    else if (available.status === "rejected") setError(message(available.reason));
    return null;
  }, [client]);

  useEffect(() => { void restore(); }, [restore]);

  const signIn = useCallback(async (
    action: () => Promise<NativeAuthSession>,
  ): Promise<NativeAuthSession | null> => {
    if (busy) return null;
    setBusy(true);
    setError(null);
    try {
      const next = await action();
      setSession(next);
      setStatus("signed_in");
      return next;
    } catch (cause) {
      setError(message(cause));
      setStatus("signed_out");
      return null;
    } finally {
      setBusy(false);
    }
  }, [busy]);

  const signInWithGoogle = useCallback(
    () => signIn(() => client.signInWithGoogle()),
    [client, signIn],
  );
  const signInWithDevelopmentProfile = useCallback(
    (profileKey?: string) => signIn(() => client.signInWithDevelopmentProfile(profileKey)),
    [client, signIn],
  );

  const signOut = useCallback(async () => {
    if (busy) return;
    setBusy(true);
    setError(null);
    try {
      await client.logout();
    } catch (cause) {
      setError(message(cause));
    } finally {
      setSession(null);
      setStatus("signed_out");
      setBusy(false);
    }
  }, [busy, client]);

  return {
    status,
    session,
    capabilities,
    busy,
    error,
    signInWithGoogle,
    signInWithDevelopmentProfile,
    signOut,
    restore,
    clearError: useCallback(() => setError(null), []),
  };
}

function message(cause: unknown): string {
  return cause instanceof Error ? cause.message : "Authentication failed";
}
