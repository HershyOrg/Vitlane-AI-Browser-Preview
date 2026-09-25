import * as SecureStore from "expo-secure-store";
import { Platform } from "react-native";

export type ChatSettings = {
  apiKey: string;
  model: string;
  browser?: { maxSteps: number; timeoutSeconds: number };
};

const SETTINGS_KEY = "vitlane.openai.settings.v1";
const OPTIONS: SecureStore.SecureStoreOptions = {
  keychainService: "com.vitlane.mobile.openai",
  keychainAccessible: SecureStore.WHEN_UNLOCKED_THIS_DEVICE_ONLY,
};

function isNative(): boolean {
  return Platform.OS === "android" || Platform.OS === "ios";
}

async function requireSecureStorage(): Promise<void> {
  try {
    if (await SecureStore.isAvailableAsync()) return;
  } catch {
    // Native errors may contain sensitive details; only show a fixed message.
  }
  throw new Error("이 기기에서 보안 저장소를 사용할 수 없습니다.");
}

function parseSettings(value: unknown): ChatSettings | null {
  if (typeof value !== "object" || value === null) return null;
  const settings = value as Partial<ChatSettings>;
  if (typeof settings.apiKey !== "string" || typeof settings.model !== "string") return null;
  const apiKey = settings.apiKey.trim();
  const model = settings.model.trim();
  if (!apiKey || !model || /\s/.test(apiKey) || /\s/.test(model) || model.length > 200) return null;
  if (settings.browser !== undefined) {
    const browser = settings.browser;
    if (!browser || !Number.isInteger(browser.maxSteps) || browser.maxSteps < 1 || browser.maxSteps > 20
      || !Number.isInteger(browser.timeoutSeconds) || browser.timeoutSeconds < 30 || browser.timeoutSeconds > 300) return null;
    return { apiKey, model, browser: { maxSteps: browser.maxSteps, timeoutSeconds: browser.timeoutSeconds } };
  }
  return { apiKey, model };
}

/** Only the key and model are persisted, using the device's protected storage. */
export async function loadSettings(): Promise<ChatSettings | null> {
  if (!isNative()) return null;
  await requireSecureStorage();
  let serialized: string | null;
  try {
    serialized = await SecureStore.getItemAsync(SETTINGS_KEY, OPTIONS);
  } catch {
    throw new Error("저장된 설정을 불러올 수 없습니다. 다시 시도해 주세요.");
  }
  if (serialized === null) return null;
  let settings: ChatSettings | null = null;
  try {
    settings = parseSettings(JSON.parse(serialized) as unknown);
  } catch {
    // Remove corrupt records instead of exposing the stored value in an error.
  }
  if (!settings) await clearSettings();
  return settings;
}

export async function saveSettings(settings: ChatSettings): Promise<void> {
  if (!isNative()) throw new Error("API 키 저장은 Android 및 iOS 앱에서만 지원합니다.");
  const normalized = parseSettings(settings);
  if (!normalized) throw new Error("올바른 API 키와 모델 이름을 입력해 주세요.");
  await requireSecureStorage();
  try {
    await SecureStore.setItemAsync(SETTINGS_KEY, JSON.stringify(normalized), OPTIONS);
  } catch {
    throw new Error("설정을 안전하게 저장하지 못했습니다. 다시 시도해 주세요.");
  }
}

export async function clearSettings(): Promise<void> {
  if (!isNative()) return;
  await requireSecureStorage();
  try {
    await SecureStore.deleteItemAsync(SETTINGS_KEY, OPTIONS);
  } catch {
    throw new Error("저장된 API 키를 삭제하지 못했습니다. 다시 시도해 주세요.");
  }
}
