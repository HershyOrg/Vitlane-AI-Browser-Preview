import { act, fireEvent, render, waitFor } from "@testing-library/react-native";
import { Alert } from "react-native";

import App from "../../App";
import { OpenAIChatScreen } from "./OpenAIChatScreen";
import { sendChat } from "./client";
import { clearSettings, loadSettings, saveSettings } from "./storage";
import { openAgentBrowser } from "../browser/openBrowser";

jest.mock("./client", () => ({ DEFAULT_MODEL: "gpt-5-mini", sendChat: jest.fn() }));
jest.mock("./storage", () => ({ loadSettings: jest.fn(), saveSettings: jest.fn(), clearSettings: jest.fn() }));
jest.mock("../browser/openBrowser", () => ({ openAgentBrowser: jest.fn() }));
jest.mock("expo-crypto", () => {
  let sequence = 0;
  return { randomUUID: () => `chat-${++sequence}` };
});

const settings = { apiKey: "sk-test-personal-key", model: "gpt-5-mini" };
const load = jest.mocked(loadSettings);
const save = jest.mocked(saveSettings);
const send = jest.mocked(sendChat);

beforeEach(() => {
  jest.clearAllMocks();
  load.mockResolvedValue(settings);
  save.mockResolvedValue();
  jest.mocked(clearSettings).mockResolvedValue();
  send.mockResolvedValue({ text: "안녕하세요!", incomplete: false });
  jest.mocked(openAgentBrowser).mockResolvedValue();
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
