import { waitFor } from "@testing-library/react-native";
import { Platform } from "react-native";
import BrowserModule, { type BrowserSettingsRequest } from "../../modules/vitlane-browser/src/VitlaneBrowserModule";
import { loadSettings, saveSettings } from "../chat/storage";
import { subscribeToBrowserSettings } from "./settingsBridge";

jest.mock("../../modules/vitlane-browser/src/VitlaneBrowserModule", () => ({
  __esModule: true, default: { addListener: jest.fn(), finishSettingsRequest: jest.fn() },
}));
jest.mock("../chat/storage", () => ({ loadSettings: jest.fn(), saveSettings: jest.fn() }));
const native = jest.mocked(BrowserModule!);
const load = jest.mocked(loadSettings);
const save = jest.mocked(saveSettings);
const existing = { apiKey: "sk-test-existing", model: "gpt-5-mini" };
let receive: (request: BrowserSettingsRequest) => void;
const remove = jest.fn();
const request = { requestId: "test-request", apiKey: "", model: "gpt-5-mini", maxSteps: 8, timeoutSeconds: 90 };

beforeEach(() => {
  jest.clearAllMocks();
  jest.replaceProperty(Platform, "OS", "android");
  load.mockResolvedValue(existing);
  save.mockResolvedValue();
  native.finishSettingsRequest.mockResolvedValue();
  native.addListener.mockImplementation((_event, listener) => { receive = listener; return { remove }; });
});
afterEach(() => jest.restoreAllMocks());

it("keeps a saved key when blank and acknowledges only after protected storage succeeds", async () => {
  let finishSave!: () => void;
  save.mockImplementationOnce(() => new Promise<void>(resolve => { finishSave = resolve; }));
  const onSaved = jest.fn();
  const unsubscribe = subscribeToBrowserSettings(onSaved);
  receive(request);
  const expected = { ...existing, browser: { maxSteps: 8, timeoutSeconds: 90 } };
  await waitFor(() => expect(save).toHaveBeenCalledWith(expected));
  expect(native.finishSettingsRequest).not.toHaveBeenCalled();
  expect(onSaved).not.toHaveBeenCalled();
  finishSave();
  await waitFor(() => expect(native.finishSettingsRequest).toHaveBeenCalledWith(
    request.requestId, existing.apiKey, existing.model, 8, 90, ""));
  expect(onSaved).toHaveBeenCalledWith(expected);
  unsubscribe();
  expect(remove).toHaveBeenCalledTimes(1);
});

it("replaces key and model in the store shared with chat", async () => {
  const onSaved = jest.fn();
  const unsubscribe = subscribeToBrowserSettings(onSaved);
  receive({ ...request, apiKey: "  sk-test-replacement  ", model: "gpt-4o-mini" });
  await waitFor(() => expect(onSaved).toHaveBeenCalledWith({ apiKey: "sk-test-replacement", model: "gpt-4o-mini",
    browser: { maxSteps: 8, timeoutSeconds: 90 } }));
  unsubscribe();
});

it("acknowledges extended browser budgets after save", async () => {
  const unsubscribe = subscribeToBrowserSettings(jest.fn());
  receive({ ...request, maxSteps: 100, timeoutSeconds: 1800 });
  await waitFor(() => expect(native.finishSettingsRequest).toHaveBeenCalledWith(
    request.requestId, existing.apiKey, existing.model, 100, 1800, ""));
  expect(save).toHaveBeenCalledWith({ ...existing, browser: { maxSteps: 100, timeoutSeconds: 1800 } });
  unsubscribe();
});

it("does not apply a failed write or expose its native error", async () => {
  save.mockRejectedValueOnce(new Error("sk-test-sensitive-native-error"));
  const onSaved = jest.fn();
  const unsubscribe = subscribeToBrowserSettings(onSaved);
  receive(request);
  await waitFor(() => expect(native.finishSettingsRequest).toHaveBeenCalledWith(
    request.requestId, "", "", 20, 300, "설정을 안전하게 저장하지 못했습니다. 다시 시도해 주세요."));
  expect(onSaved).not.toHaveBeenCalled();
  unsubscribe();
});

it.each([{ ...request, maxSteps: 101 }, { ...request, timeoutSeconds: 0 },
  { ...request, timeoutSeconds: 90.5 }, { ...request, model: "invalid model" },
  { ...request, apiKey: "sk-invalid:key" }])(
  "rejects invalid settings before writing", async invalid => {
    const unsubscribe = subscribeToBrowserSettings(jest.fn());
    receive(invalid);
    await waitFor(() => expect(native.finishSettingsRequest).toHaveBeenCalled());
    expect(save).not.toHaveBeenCalled();
    expect(native.finishSettingsRequest.mock.calls[0]?.[1]).toBe("");
    unsubscribe();
  });

it("requires a key on first save", async () => {
  load.mockResolvedValueOnce(null);
  const unsubscribe = subscribeToBrowserSettings(jest.fn());
  receive(request);
  await waitFor(() => expect(native.finishSettingsRequest).toHaveBeenCalledWith(
    request.requestId, "", "", 20, 300, "sk-로 시작하는 OpenAI API 키를 입력해 주세요."));
  expect(save).not.toHaveBeenCalled();
  unsubscribe();
});

it("does not apply a late save to an unmounted screen", async () => {
  let finishSave!: () => void;
  save.mockImplementationOnce(() => new Promise<void>(resolve => { finishSave = resolve; }));
  const onSaved = jest.fn();
  const unsubscribe = subscribeToBrowserSettings(onSaved);
  receive(request);
  await waitFor(() => expect(save).toHaveBeenCalledTimes(1));
  unsubscribe();
  finishSave();
  await Promise.resolve();
  await Promise.resolve();
  expect(onSaved).not.toHaveBeenCalled();
  expect(native.finishSettingsRequest).not.toHaveBeenCalled();
});

it("restores the saved key, model and limits without overwriting them", async () => {
  const saved = { apiKey: "sk-new-key", model: "gpt-custom", browser: { maxSteps: 3, timeoutSeconds: 60 } };
  load.mockResolvedValueOnce(saved);
  const onSaved = jest.fn();
  const unsubscribe = subscribeToBrowserSettings(onSaved);
  receive({ ...request, action: "load", model: "stale-model" });
  await waitFor(() => expect(native.finishSettingsRequest).toHaveBeenCalledWith(
    request.requestId, saved.apiKey, saved.model, 3, 60, ""));
  expect(onSaved).toHaveBeenCalledWith(saved);
  expect(save).not.toHaveBeenCalled();
  unsubscribe();
});

it("returns empty settings when the saved key was removed", async () => {
  load.mockResolvedValueOnce(null);
  const onSaved = jest.fn();
  const unsubscribe = subscribeToBrowserSettings(onSaved);
  receive({ ...request, action: "load" });
  await waitFor(() => expect(native.finishSettingsRequest).toHaveBeenCalledWith(
    request.requestId, "", "", 20, 300, ""));
  expect(onSaved).not.toHaveBeenCalled();
  expect(save).not.toHaveBeenCalled();
  unsubscribe();
});

it("allows a replacement key to repair settings without reading a broken old record", async () => {
  load.mockRejectedValue(new Error("old key is unreadable"));
  const unsubscribe = subscribeToBrowserSettings(jest.fn());
  receive({ ...request, apiKey: "sk-replacement", model: "gpt-custom" });
  await waitFor(() => expect(native.finishSettingsRequest).toHaveBeenCalledWith(
    request.requestId, "sk-replacement", "gpt-custom", 8, 90, ""));
  expect(load).not.toHaveBeenCalled();
  unsubscribe();
});

it("waits for an in-flight save before restoring on resume", async () => {
  let finishSave!: () => void;
  const updated = { apiKey: "sk-updated", model: "gpt-custom", browser: { maxSteps: 8, timeoutSeconds: 90 } };
  save.mockImplementationOnce(() => new Promise<void>(resolve => {
    finishSave = () => { load.mockResolvedValue(updated); resolve(); };
  }));
  const unsubscribe = subscribeToBrowserSettings(jest.fn());
  receive({ ...request, apiKey: updated.apiKey, model: updated.model });
  await waitFor(() => expect(save).toHaveBeenCalledTimes(1));
  receive({ ...request, requestId: "resume", action: "load" });
  await Promise.resolve();
  expect(load).not.toHaveBeenCalled();
  finishSave();
  await waitFor(() => expect(native.finishSettingsRequest).toHaveBeenCalledWith(
    "resume", updated.apiKey, updated.model, 8, 90, ""));
  expect(save).toHaveBeenCalledTimes(1);
  unsubscribe();
});
