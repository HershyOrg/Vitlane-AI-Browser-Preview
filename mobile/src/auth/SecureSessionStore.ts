import * as SecureStore from "expo-secure-store";
import { Platform } from "react-native";

import { parseNativeAuthSession } from "./session";
import {
  MobileAuthError,
  type AuthSessionStore,
  type NativeAuthSession,
} from "./types";

const SESSION_KEY = "vitlane.auth.session.v1";

type SecureStoreDriver = {
  isAvailableAsync(): Promise<boolean>;
  getItemAsync(key: string, options?: SecureStore.SecureStoreOptions): Promise<string | null>;
  setItemAsync(
    key: string,
    value: string,
    options?: SecureStore.SecureStoreOptions,
  ): Promise<void>;
  deleteItemAsync(key: string, options?: SecureStore.SecureStoreOptions): Promise<void>;
};

export type SecureSessionStoreOptions = {
  /** Injectable only for tests and nonstandard native shells. */
  platform?: string;
  driver?: SecureStoreDriver;
};

/**
 * Persists native credentials in iOS Keychain or Android Keystore-backed
 * SecureStore. Web intentionally has no token persistence fallback.
 */
export class SecureSessionStore implements AuthSessionStore {
  private readonly platform: string;
  private readonly driver: SecureStoreDriver;
  private readonly secureStoreOptions: SecureStore.SecureStoreOptions;

  public constructor(options: SecureSessionStoreOptions = {}) {
    this.platform = options.platform ?? Platform.OS;
    this.driver = options.driver ?? SecureStore;
    this.secureStoreOptions = {
      keychainService: "com.vitlane.mobile.auth",
      keychainAccessible: SecureStore.WHEN_UNLOCKED_THIS_DEVICE_ONLY,
    };
  }

  public async load(): Promise<NativeAuthSession | null> {
    if (!this.isNative()) return null;
    await this.requireAvailable();
    const serialized = await this.driver.getItemAsync(SESSION_KEY, this.secureStoreOptions);
    if (!serialized) return null;
    try {
      return parseNativeAuthSession(JSON.parse(serialized) as unknown);
    } catch {
      await this.driver.deleteItemAsync(SESSION_KEY, this.secureStoreOptions);
      return null;
    }
  }

  public async save(session: NativeAuthSession): Promise<void> {
    if (!this.isNative()) {
      throw new MobileAuthError(
        "UNSUPPORTED_PLATFORM",
        "Native session tokens are not persisted on the web.",
      );
    }
    await this.requireAvailable();
    const normalized = parseNativeAuthSession(session);
    await this.driver.setItemAsync(
      SESSION_KEY,
      JSON.stringify(normalized),
      this.secureStoreOptions,
    );
  }

  public async clear(): Promise<void> {
    if (!this.isNative()) return;
    await this.requireAvailable();
    await this.driver.deleteItemAsync(SESSION_KEY, this.secureStoreOptions);
  }

  private isNative(): boolean {
    return this.platform === "ios" || this.platform === "android";
  }

  private async requireAvailable(): Promise<void> {
    if (await this.driver.isAvailableAsync()) return;
    throw new MobileAuthError(
      "SECURE_STORAGE_UNAVAILABLE",
      "Secure credential storage is unavailable on this device.",
    );
  }
}
