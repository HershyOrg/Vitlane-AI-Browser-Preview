import * as SecureStore from "expo-secure-store";
import { Platform } from "react-native";

import { clearSettings, loadSettings, saveSettings } from "./storage";

jest.mock("expo-secure-store", () => ({
  WHEN_UNLOCKED_THIS_DEVICE_ONLY: 3,
  isAvailableAsync: jest.fn(),
  getItemAsync: jest.fn(),
  setItemAsync: jest.fn(),
  deleteItemAsync: jest.fn(),
}));

const secure = jest.mocked(SecureStore);
const settings = { apiKey: "private-key", model: "gpt-5-mini" };
const upgraded = { ...settings, model: "gpt-6-sol" };
const persisted = (value: typeof settings) => JSON.stringify({ ...value, modelMigrationRevision: 1 });
let storedRecord: string | null;

describe("chat secure settings", () => {
  beforeEach(() => {
    jest.replaceProperty(Platform, "OS", "android");
    jest.resetAllMocks();
    storedRecord = null;
    secure.isAvailableAsync.mockResolvedValue(true);
    secure.getItemAsync.mockImplementation(async () => storedRecord);
    secure.setItemAsync.mockImplementation(async (_key, value) => { storedRecord = value; });
    secure.deleteItemAsync.mockImplementation(async () => { storedRecord = null; });
  });

  afterEach(() => jest.restoreAllMocks());

  it("persists browser limits while retaining compatibility with older settings", async () => {
    const next = { ...settings, browser: { maxSteps: 8, timeoutSeconds: 90 } };
    await saveSettings(next);
    expect(secure.setItemAsync.mock.calls[0]?.[1]).toBe(persisted(next));
    await expect(loadSettings()).resolves.toEqual(next);
    storedRecord = JSON.stringify(settings);
    await expect(loadSettings()).resolves.toEqual(upgraded);
  });

  it("persists only normalized credentials and model in device-protected SecureStore", async () => {
    await saveSettings({ ...settings, apiKey: " private-key ", model: " gpt-5-mini ", history: ["private message"] } as typeof settings);
    expect(secure.setItemAsync).toHaveBeenCalledWith(
      "vitlane.openai.settings.v1", persisted(settings), {
        keychainService: "com.vitlane.mobile.openai",
        keychainAccessible: SecureStore.WHEN_UNLOCKED_THIS_DEVICE_ONLY,
      },
    );
    await expect(loadSettings()).resolves.toEqual(settings);
    expect(secure.getItemAsync).toHaveBeenCalledWith("vitlane.openai.settings.v1", expect.objectContaining({
      keychainService: "com.vitlane.mobile.openai",
    }));
    await clearSettings();
    expect(secure.deleteItemAsync).toHaveBeenCalledWith("vitlane.openai.settings.v1", expect.objectContaining({
      keychainService: "com.vitlane.mobile.openai",
    }));
  });

  it("round trips the extended browser budget without persisting task memory", async () => {
    const next = { ...settings, browser: { maxSteps: 100, timeoutSeconds: 1800 } };
    await saveSettings(next);
    await expect(loadSettings()).resolves.toEqual(next);
    await expect(saveSettings({ ...next, browser: { maxSteps: 101, timeoutSeconds: 1800 } })).rejects.toThrow();
    await expect(saveSettings({ ...next, browser: { maxSteps: 100, timeoutSeconds: 1801 } })).rejects.toThrow();
  });

  it("upgrades the legacy default once while preserving the API key and browser limits", async () => {
    const browser = { maxSteps: 70, timeoutSeconds: 1500 };
    storedRecord = JSON.stringify({ ...settings, model: " gpt-5-mini ", browser, history: ["private task"] });
    const expected = { ...upgraded, browser };
    await expect(loadSettings()).resolves.toEqual(expected);
    expect(JSON.parse(storedRecord!)).toEqual({ ...expected, modelMigrationRevision: 1 });
    expect(storedRecord).not.toContain("private task");
    await expect(loadSettings()).resolves.toEqual(expected);
    expect(secure.setItemAsync).toHaveBeenCalledTimes(1);
    expect(secure.deleteItemAsync).not.toHaveBeenCalled();
  });

  it.each(["custom-model", "gpt-5-mini-2025-08-07", "gpt-6-sol"])("preserves the preexisting model %s and marks its migration revision", async (model) => {
    const previous = { ...settings, model };
    storedRecord = JSON.stringify(previous);
    await expect(loadSettings()).resolves.toEqual(previous);
    expect(JSON.parse(storedRecord!)).toEqual({ ...previous, modelMigrationRevision: 1 });
    await expect(loadSettings()).resolves.toEqual(previous);
    expect(secure.setItemAsync).toHaveBeenCalledTimes(1);
  });

  it.each(["gpt-5-mini", "custom-user-choice"])("preserves a later explicit user selection of %s", async (model) => {
    storedRecord = JSON.stringify(settings);
    await expect(loadSettings()).resolves.toEqual(upgraded);
    const explicit = { ...settings, model, browser: { maxSteps: 25, timeoutSeconds: 600 } };
    await saveSettings(explicit);
    await expect(loadSettings()).resolves.toEqual(explicit);
    await expect(loadSettings()).resolves.toEqual(explicit);
    expect(secure.setItemAsync).toHaveBeenCalledTimes(2);
    expect(JSON.parse(storedRecord!)).toEqual({ ...explicit, modelMigrationRevision: 1 });
  });

  it("preserves already migrated settings and never returns the internal revision marker", async () => {
    storedRecord = JSON.stringify({ ...settings, modelMigrationRevision: 2 });
    await expect(loadSettings()).resolves.toEqual(settings);
    expect(secure.setItemAsync).not.toHaveBeenCalled();
  });

  it("does not let a caller supply an old migration revision when explicitly saving settings", async () => {
    await saveSettings({ ...settings, modelMigrationRevision: 0 } as typeof settings);
    expect(JSON.parse(storedRecord!)).toEqual({ ...settings, modelMigrationRevision: 1 });
    await expect(loadSettings()).resolves.toEqual(settings);
  });

  it("serializes simultaneous legacy loads so the migrated record is written once", async () => {
    storedRecord = JSON.stringify(settings);
    let started!: () => void;
    let release!: (value: string) => void;
    const readStarted = new Promise<void>((resolve) => { started = resolve; });
    const pendingRead = new Promise<string>((resolve) => { release = resolve; });
    secure.getItemAsync.mockImplementationOnce(async () => { started(); return pendingRead; });
    const first = loadSettings();
    const second = loadSettings();
    await readStarted;
    expect(secure.getItemAsync).toHaveBeenCalledTimes(1);
    release(storedRecord!);
    await expect(Promise.all([first, second])).resolves.toEqual([upgraded, upgraded]);
    expect(secure.setItemAsync).toHaveBeenCalledTimes(1);
  });

  it("does not overwrite a newer explicit save when a legacy migration is still writing", async () => {
    storedRecord = JSON.stringify(settings);
    let started!: () => void;
    let release!: () => void;
    const writeStarted = new Promise<void>((resolve) => { started = resolve; });
    const pendingWrite = new Promise<void>((resolve) => { release = resolve; });
    secure.setItemAsync.mockImplementationOnce(async (_key, value) => {
      started(); await pendingWrite; storedRecord = value;
    });
    const loading = loadSettings();
    await writeStarted;
    const explicit = { apiKey: "replacement-key", model: "gpt-5-mini", browser: { maxSteps: 12, timeoutSeconds: 80 } };
    const saving = saveSettings(explicit);
    expect(secure.setItemAsync).toHaveBeenCalledTimes(1);
    release();
    await expect(loading).resolves.toEqual(upgraded);
    await saving;
    await expect(loadSettings()).resolves.toEqual(explicit);
    expect(JSON.parse(storedRecord!)).toEqual({ ...explicit, modelMigrationRevision: 1 });
  });

  it("does not recreate credentials when a clear is queued during migration", async () => {
    storedRecord = JSON.stringify(settings);
    let started!: () => void;
    let release!: () => void;
    const writeStarted = new Promise<void>((resolve) => { started = resolve; });
    const pendingWrite = new Promise<void>((resolve) => { release = resolve; });
    secure.setItemAsync.mockImplementationOnce(async (_key, value) => {
      started(); await pendingWrite; storedRecord = value;
    });
    const loading = loadSettings();
    await writeStarted;
    const clearing = clearSettings();
    release();
    await loading;
    await clearing;
    await expect(loadSettings()).resolves.toBeNull();
    expect(storedRecord).toBeNull();
  });

  it("rejects failed migration persistence with a fixed error and retries the unchanged legacy record", async () => {
    const original = JSON.stringify(settings);
    storedRecord = original;
    secure.setItemAsync.mockRejectedValueOnce(new Error("native error with private-key and private task"));
    await expect(loadSettings()).rejects.toThrow("설정을 안전하게 저장하지 못했습니다. 다시 시도해 주세요.");
    expect(storedRecord).toBe(original);
    expect(secure.deleteItemAsync).not.toHaveBeenCalled();
    await expect(loadSettings()).resolves.toEqual(upgraded);
    expect(JSON.parse(storedRecord!)).toEqual({ ...upgraded, modelMigrationRevision: 1 });
  });

  it("never falls back to browser storage", async () => {
    jest.replaceProperty(Platform, "OS", "web");
    await expect(saveSettings(settings)).rejects.toThrow("앱에서만 지원");
    await expect(loadSettings()).resolves.toBeNull();
    await clearSettings();
    expect(secure.isAvailableAsync).not.toHaveBeenCalled();
    expect(secure.setItemAsync).not.toHaveBeenCalled();
    expect(secure.getItemAsync).not.toHaveBeenCalled();
  });

  it.each(["not-json", '{"apiKey":"private-key"}', '{"apiKey":"","model":"gpt-5-mini"}'])
    ("deletes malformed records without exposing them", async (serialized) => {
      secure.getItemAsync.mockResolvedValue(serialized);
      await expect(loadSettings()).resolves.toBeNull();
      expect(secure.deleteItemAsync).toHaveBeenCalledTimes(1);
    });

  it("reports unavailable protected storage without trying to save", async () => {
    secure.isAvailableAsync.mockResolvedValue(false);
    await expect(saveSettings(settings)).rejects.toThrow("보안 저장소");
    expect(secure.setItemAsync).not.toHaveBeenCalled();
  });

  it("sanitizes native storage failures", async () => {
    secure.setItemAsync.mockRejectedValue(new Error("private-key"));
    await expect(saveSettings(settings)).rejects.toThrow("안전하게 저장하지 못했습니다");
    secure.getItemAsync.mockRejectedValue(new Error("private-key"));
    await expect(loadSettings()).rejects.toThrow("불러올 수 없습니다");
    secure.deleteItemAsync.mockRejectedValue(new Error("private-key"));
    await expect(clearSettings()).rejects.toThrow("삭제하지 못했습니다");
  });
});
