import { act, fireEvent, render, waitFor } from "@testing-library/react-native";
import { StrictMode } from "react";
import { Alert, Platform } from "react-native";

import App from "../../App";
import { OpenAIChatScreen } from "./OpenAIChatScreen";
import { sendChat } from "./client";
import { clearSettings, loadSettings, saveSettings, type ChatSettings } from "./storage";
import { openAgentBrowser } from "../browser/openBrowser";
import { subscribeToBrowserSettings } from "../browser/settingsBridge";

jest.mock("./client", () => ({ DEFAULT_MODEL: "gpt-6-sol", sendChat: jest.fn() }));
jest.mock("./storage", () => ({ loadSettings: jest.fn(), saveSettings: jest.fn(), clearSettings: jest.fn() }));
jest.mock("../browser/openBrowser", () => ({ openAgentBrowser: jest.fn() }));
jest.mock("../browser/settingsBridge", () => ({ subscribeToBrowserSettings: jest.fn() }));
jest.mock("expo-crypto", () => {
  let sequence = 0;
  return { randomUUID: () => `chat-${++sequence}` };
});

const settings = { apiKey: "sk-test-personal-key", model: "gpt-6-sol" };
const load = jest.mocked(loadSettings);
const save = jest.mocked(saveSettings);
const send = jest.mocked(sendChat);
const launch = jest.mocked(openAgentBrowser);
const unsubscribeSettings = jest.fn();
let nativeSettingsSaved: (next: ChatSettings) => void;

beforeEach(() => {
  jest.clearAllMocks();
  jest.replaceProperty(Platform, "OS", "ios");
  load.mockResolvedValue(settings);
  save.mockResolvedValue();
  jest.mocked(clearSettings).mockResolvedValue();
  send.mockResolvedValue({ text: "안녕하세요!", incomplete: false });
  launch.mockResolvedValue();
  jest.mocked(subscribeToBrowserSettings).mockImplementation((onSaved) => {
    nativeSettingsSaved = onSaved;
    return unsubscribeSettings;
  });
});

afterEach(() => jest.restoreAllMocks());

it("opens the native browser with saved settings and surfaces launch errors", async () => {
  const view = await render(<OpenAIChatScreen />);
  await fireEvent.press(await view.findByRole("button", { name: "AI 브라우저 열기" }));
  expect(openAgentBrowser).toHaveBeenCalledWith(settings);
  expect(send).not.toHaveBeenCalled();
  jest.mocked(openAgentBrowser).mockRejectedValueOnce(new Error("브라우저를 열지 못했습니다."));
  await fireEvent.press(view.getByRole("button", { name: "브라우저" }));
  expect(await view.findByText("브라우저를 열지 못했습니다.")).toBeTruthy();
});

it("opens API-key setup by default without a server origin or network request", async () => {
  load.mockResolvedValue(null);
  const originalMode = process.env.EXPO_PUBLIC_VITLANE_DATA_MODE;
  delete process.env.EXPO_PUBLIC_VITLANE_DATA_MODE;
  try {
    const view = await render(<App />);
    expect(await view.findByLabelText("OpenAI API 키")).toBeTruthy();
    expect(send).not.toHaveBeenCalled();
    await fireEvent.changeText(view.getByLabelText("OpenAI API 키"), settings.apiKey);
    await fireEvent.press(view.getByRole("button", { name: "저장하고 대화하기" }));
    await waitFor(() => expect(save).toHaveBeenCalledWith(settings));
    expect(view.queryByLabelText("OpenAI API 키")).toBeNull();
    expect(view.getByRole("button", { name: "보내기" })).toBeDisabled();
  } finally {
    process.env.EXPO_PUBLIC_VITLANE_DATA_MODE = originalMode;
  }
});

it("sends conversation history and restores a failed prompt for a duplicate-free retry", async () => {
  const view = await render(<OpenAIChatScreen />);
  await fireEvent.changeText(await view.findByLabelText("메시지"), "안녕");
  await fireEvent.press(view.getByRole("button", { name: "보내기" }));
  expect(await view.findByText("안녕하세요!")).toBeTruthy();

  send.mockRejectedValueOnce(new Error("인터넷 연결을 확인해 주세요."));
  await fireEvent.changeText(view.getByLabelText("메시지"), "추천해 줘");
  await fireEvent.press(view.getByRole("button", { name: "보내기" }));
  expect(await view.findByText("인터넷 연결을 확인해 주세요.")).toBeTruthy();
  expect(view.getByLabelText("메시지").props.value).toBe("추천해 줘");

  await fireEvent.press(view.getByRole("button", { name: "보내기" }));
  await waitFor(() => expect(send).toHaveBeenCalledTimes(3));
  expect(send.mock.calls[2]?.[0].messages.map(({ role, content }) => ({ role, content }))).toEqual([
    { role: "user", content: "안녕" },
    { role: "assistant", content: "안녕하세요!" },
    { role: "user", content: "추천해 줘" },
  ]);
});

it("cancels an active request, restores the prompt, and does not automatically retry", async () => {
  send.mockImplementationOnce(({ signal }) => new Promise((_resolve, reject) => {
    signal?.addEventListener("abort", () => reject(new Error("cancelled")));
  }));
  const view = await render(<OpenAIChatScreen />);
  await fireEvent.changeText(await view.findByLabelText("메시지"), "긴 답변");
  await fireEvent.press(view.getByRole("button", { name: "보내기" }));
  expect(view.getByRole("button", { name: "설정" })).toBeDisabled();
  await fireEvent.press(view.getByRole("button", { name: "중지" }));
  expect(await view.findByText("응답 요청을 중지했습니다.")).toBeTruthy();
  expect(view.getByLabelText("메시지").props.value).toBe("긴 답변");
  expect(send).toHaveBeenCalledTimes(1);
});

it("does not reveal the saved API key and clears it only after explicit deletion", async () => {
  const alert = jest.spyOn(Alert, "alert");
  const view = await render(<OpenAIChatScreen />);
  await fireEvent.press(await view.findByRole("button", { name: "설정" }));
  expect(view.getByLabelText("OpenAI API 키").props.value).toBe("");
  expect(view.getByLabelText("OpenAI API 키").props.secureTextEntry).toBe(true);
  await fireEvent.press(view.getByRole("button", { name: "저장된 API 키 삭제" }));
  expect(clearSettings).not.toHaveBeenCalled();
  const confirm = alert.mock.calls[0]?.[2]?.find((button) => button.text === "삭제");
  expect(confirm).toBeDefined();
  await act(async () => { confirm?.onPress?.(); });
  await waitFor(() => expect(clearSettings).toHaveBeenCalledTimes(1));
  expect(await view.findByRole("button", { name: "OpenAI API 키 설정" })).toBeTruthy();
});

it("keeps key setup open if secure storage fails", async () => {
  load.mockResolvedValue(null);
  save.mockRejectedValue(new Error("storage unavailable"));
  const view = await render(<OpenAIChatScreen />);
  await fireEvent.changeText(await view.findByLabelText("OpenAI API 키"), settings.apiKey);
  await fireEvent.press(view.getByRole("button", { name: "저장하고 대화하기" }));
  expect(await view.findByText("기기에 API 키를 저장하지 못했습니다. 다시 시도해 주세요.")).toBeTruthy();
  expect(send).not.toHaveBeenCalled();
});

describe("Android unified native conversation", () => {
  beforeEach(() => { jest.replaceProperty(Platform, "OS", "android"); });

  it("waits for saved settings, then automatically opens chat once without a second composer", async () => {
    let finishLoad!: (value: ChatSettings | null) => void;
    load.mockImplementationOnce(() => new Promise(resolve => { finishLoad = resolve; }));
    const view = await render(<OpenAIChatScreen />);
    expect(view.getByLabelText("설정 불러오는 중")).toBeTruthy();
    expect(launch).not.toHaveBeenCalled();
    const saved = { ...settings, browser: { maxSteps: 45, timeoutSeconds: 900 } };
    await act(async () => { finishLoad(saved); });
    await waitFor(() => expect(launch).toHaveBeenCalledWith(saved));
    expect(launch).toHaveBeenCalledTimes(1);
    expect(view.getByRole("button", { name: "대화 열기" })).toBeTruthy();
    expect(view.queryByLabelText("메시지")).toBeNull();
    expect(view.queryByRole("button", { name: "새 대화" })).toBeNull();
    expect(view.queryByRole("button", { name: "브라우저" })).toBeNull();
    expect(send).not.toHaveBeenCalled();
  });

  it("opens the app's native chat with no key instead of showing an automatic settings modal", async () => {
    load.mockResolvedValue(null);
    const originalMode = process.env.EXPO_PUBLIC_VITLANE_DATA_MODE;
    process.env.EXPO_PUBLIC_VITLANE_DATA_MODE = "openai";
    try {
      const view = await render(<App />);
      await waitFor(() => expect(launch).toHaveBeenCalledWith(null));
      expect(view.queryByLabelText("OpenAI API 키")).toBeNull();
      expect(view.queryByLabelText("메시지")).toBeNull();
      expect(send).not.toHaveBeenCalled();
      await fireEvent.press(view.getByRole("button", { name: "설정" }));
      expect(view.getByLabelText("OpenAI API 키")).toBeTruthy();
    } finally {
      process.env.EXPO_PUBLIC_VITLANE_DATA_MODE = originalMode;
    }
  });

  it("still opens native chat after a settings read failure and keeps a visible recovery notice", async () => {
    load.mockRejectedValueOnce(new Error("protected storage unavailable"));
    const view = await render(<OpenAIChatScreen />);
    await waitFor(() => expect(launch).toHaveBeenCalledWith(null));
    expect(view.getByText("저장된 설정을 읽지 못했습니다. API 키를 다시 설정해 주세요.")).toBeTruthy();
    expect(view.queryByLabelText("OpenAI API 키")).toBeNull();
    expect(send).not.toHaveBeenCalled();
  });

  it("keeps a launch error visible until an explicit retry, without a relaunch loop", async () => {
    launch.mockRejectedValueOnce(new Error("대화 화면을 열지 못했습니다."));
    const view = await render(<OpenAIChatScreen />);
    expect(await view.findByText("대화 화면을 열지 못했습니다.")).toBeTruthy();
    await view.rerender(<OpenAIChatScreen />);
    expect(launch).toHaveBeenCalledTimes(1);
    await fireEvent.press(view.getByRole("button", { name: "대화 열기" }));
    await waitFor(() => expect(launch).toHaveBeenCalledTimes(2));
    expect(view.queryByText("대화 화면을 열지 못했습니다.")).toBeNull();
    expect(send).not.toHaveBeenCalled();
  });

  it("prevents duplicate launches while the initial native launch is pending", async () => {
    let finishLaunch!: () => void;
    launch.mockImplementationOnce(() => new Promise(resolve => { finishLaunch = resolve; }));
    const view = await render(<OpenAIChatScreen />);
    await waitFor(() => expect(launch).toHaveBeenCalledTimes(1));
    expect(view.getByRole("button", { name: "대화 열기" })).toBeDisabled();
    expect(view.getByRole("button", { name: "설정" })).toBeDisabled();
    await fireEvent.press(view.getByRole("button", { name: "대화 열기" }));
    expect(launch).toHaveBeenCalledTimes(1);
    await act(async () => { finishLaunch(); });
    expect(view.getByRole("button", { name: "대화 열기" })).not.toBeDisabled();
  });

  it("keeps the settings bridge active and reopens only on request using the latest native settings", async () => {
    const view = await render(<OpenAIChatScreen />);
    await waitFor(() => expect(launch).toHaveBeenCalledTimes(1));
    const replacement = { apiKey: "sk-new-key", model: "gpt-custom", browser: { maxSteps: 33, timeoutSeconds: 600 } };
    await act(async () => { nativeSettingsSaved(replacement); });
    expect(view.getByText("gpt-custom")).toBeTruthy();
    await view.rerender(<OpenAIChatScreen />);
    expect(launch).toHaveBeenCalledTimes(1);
    expect(subscribeToBrowserSettings).toHaveBeenCalledTimes(1);
    expect(unsubscribeSettings).not.toHaveBeenCalled();
    await fireEvent.press(view.getByRole("button", { name: "대화 열기" }));
    expect(launch).toHaveBeenLastCalledWith(replacement);
    await view.unmount();
    expect(unsubscribeSettings).toHaveBeenCalledTimes(1);
  });

  it("does not overwrite a newer native settings update with a late initial read", async () => {
    let finishLoad!: (value: ChatSettings | null) => void;
    load.mockImplementationOnce(() => new Promise(resolve => { finishLoad = resolve; }));
    const view = await render(<OpenAIChatScreen />);
    const replacement = { apiKey: "sk-new-key", model: "gpt-new" };
    await act(async () => { nativeSettingsSaved(replacement); finishLoad(settings); });
    await waitFor(() => expect(launch).toHaveBeenCalledWith(replacement));
    expect(view.getByText("gpt-new")).toBeTruthy();
  });

  it("lets fallback settings be saved before reopening and does not automatically launch again", async () => {
    load.mockResolvedValue(null);
    const view = await render(<OpenAIChatScreen />);
    await waitFor(() => expect(launch).toHaveBeenCalledTimes(1));
    await fireEvent.press(view.getByRole("button", { name: "설정" }));
    await fireEvent.changeText(view.getByLabelText("OpenAI API 키"), settings.apiKey);
    await fireEvent.press(view.getByRole("button", { name: "저장하고 대화하기" }));
    await waitFor(() => expect(view.queryByLabelText("OpenAI API 키")).toBeNull());
    expect(save).toHaveBeenCalledWith(settings);
    expect(launch).toHaveBeenCalledTimes(1);
    await fireEvent.press(view.getByRole("button", { name: "대화 열기" }));
    expect(launch).toHaveBeenLastCalledWith(settings);
  });

  it("does not launch after unmount while settings are still loading", async () => {
    let finishLoad!: (value: ChatSettings | null) => void;
    load.mockImplementationOnce(() => new Promise(resolve => { finishLoad = resolve; }));
    const view = await render(<OpenAIChatScreen />);
    await view.unmount();
    await act(async () => { finishLoad(settings); });
    expect(launch).not.toHaveBeenCalled();
  });

  it("attempts automatic launch only once under StrictMode effect replay", async () => {
    const view = await render(<StrictMode><OpenAIChatScreen /></StrictMode>);
    await waitFor(() => expect(launch).toHaveBeenCalledTimes(1));
    await view.rerender(<StrictMode><OpenAIChatScreen /></StrictMode>);
    expect(launch).toHaveBeenCalledTimes(1);
  });
});

it("keeps the web conversation available without automatic native launch", async () => {
  jest.replaceProperty(Platform, "OS", "web");
  const view = await render(<OpenAIChatScreen />);
  expect(await view.findByLabelText("메시지")).toBeTruthy();
  expect(launch).not.toHaveBeenCalled();
});
