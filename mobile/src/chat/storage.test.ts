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

describe("chat secure settings", () => {
  beforeEach(() => {
    jest.replaceProperty(Platform, "OS", "android");
    jest.resetAllMocks();
    secure.isAvailableAsync.mockResolvedValue(true);
    secure.getItemAsync.mockResolvedValue(null);
    secure.setItemAsync.mockResolvedValue();
    secure.deleteItemAsync.mockResolvedValue();
  });

  afterEach(() => jest.restoreAllMocks());

  it("persists browser limits while retaining compatibility with older settings", async () => {
    const next = { ...settings, browser: { maxSteps: 8, timeoutSeconds: 90 } };
    await saveSettings(next);
    expect(secure.setItemAsync.mock.calls[0]?.[1]).toBe(JSON.stringify(next));
    secure.getItemAsync.mockResolvedValue(JSON.stringify(next));
    await expect(loadSettings()).resolves.toEqual(next);
    secure.getItemAsync.mockResolvedValue(JSON.stringify(settings));
    await expect(loadSettings()).resolves.toEqual(settings);
  });

  it("persists only normalized credentials and model in device-protected SecureStore", async () => {
    await saveSettings({ ...settings, apiKey: " private-key ", model: " gpt-5-mini ", history: ["private message"] } as typeof settings);
    expect(secure.setItemAsync).toHaveBeenCalledWith(
      "vitlane.openai.settings.v1", JSON.stringify(settings), {
        keychainService: "com.vitlane.mobile.openai",
        keychainAccessible: SecureStore.WHEN_UNLOCKED_THIS_DEVICE_ONLY,
      },
    );
    secure.getItemAsync.mockResolvedValue(JSON.stringify(settings));
    await expect(loadSettings()).resolves.toEqual(settings);
    expect(secure.getItemAsync).toHaveBeenCalledWith("vitlane.openai.settings.v1", expect.objectContaining({
      keychainService: "com.vitlane.mobile.openai",
    }));
    await clearSettings();
    expect(secure.deleteItemAsync).toHaveBeenCalledWith("vitlane.openai.settings.v1", expect.objectContaining({
      keychainService: "com.vitlane.mobile.openai",
    }));
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
