import { parseNativeAuthSession } from "./session";
import type { AuthSessionStore, NativeAuthSession } from "./types";

/**
 * Process-local credential storage for the explicit loopback Web review path.
 * A refresh or remount clears the session; no browser storage or cookie is used.
 */
export class EphemeralSessionStore implements AuthSessionStore {
  private session: NativeAuthSession | null = null;

  public async load(): Promise<NativeAuthSession | null> {
    return this.session;
  }

  public async save(session: NativeAuthSession): Promise<void> {
    this.session = parseNativeAuthSession(session);
  }

  public async clear(): Promise<void> {
    this.session = null;
  }
}
