import { DEFAULT_MODEL, sendChat, type ChatMessage } from "./client";

const history: ChatMessage[] = [
  { id: "one", role: "user", content: "안녕" },
  { id: "two", role: "assistant", content: "안녕하세요!" },
  { id: "three", role: "user", content: "이전 대화를 기억해?" },
];

const output = (text: string) => ({
  type: "message",
  content: [{ type: "output_text", text }],
});

const response = (body: unknown, status = 200): Response => ({
  ok: status >= 200 && status < 300,
  status,
  json: jest.fn(async () => body),
}) as unknown as Response;

describe("direct OpenAI chat", () => {
  let fetcher: jest.SpyInstance;

  beforeEach(() => {
    jest.useFakeTimers();
    fetcher = jest.spyOn(globalThis, "fetch").mockResolvedValue(response({
      status: "completed",
      output: [output("기억하고 있어요.")],
    }));
  });

  afterEach(() => {
    jest.restoreAllMocks();
    jest.useRealTimers();
  });

  it("sends complete history without IDs or credentials in the body and disables response storage", async () => {
    await expect(sendChat({ apiKey: " secret-key ", messages: history })).resolves.toEqual({
      text: "기억하고 있어요.", incomplete: false,
    });

    expect(fetcher).toHaveBeenCalledTimes(1);
    const [url, init] = fetcher.mock.calls[0] as [string, RequestInit];
    expect(url).toBe("https://api.openai.com/v1/responses");
    expect(init.credentials).toBe("omit");
    expect(init.headers).toMatchObject({ Authorization: "Bearer secret-key" });
    expect(JSON.parse(init.body as string)).toEqual({
      model: DEFAULT_MODEL,
      input: history.map(({ role, content }) => ({ role, content })),
      max_output_tokens: 4096,
      store: false,
    });
    expect(init.body).not.toContain("secret-key");
    expect(jest.getTimerCount()).toBe(0);
  });

  it("uses the configured model and gathers output text after reasoning items", async () => {
    fetcher.mockResolvedValue(response({
      status: "completed",
      output: [
        { type: "reasoning", content: [{ type: "output_text", text: "hidden" }] },
        output("첫 번째 답변"),
        { type: "message", content: [
          { type: "output_text", text: "두 번째 답변" },
          { type: "output_text", text: "마지막 답변" },
        ] },
      ],
    }));
    await expect(sendChat({ apiKey: "key", model: " gpt-custom ", messages: history })).resolves.toEqual({
      text: "첫 번째 답변\n\n두 번째 답변\n\n마지막 답변", incomplete: false,
    });
    expect(JSON.parse(fetcher.mock.calls[0][1].body).model).toBe("gpt-custom");
  });

  it("returns partial text with an incomplete flag", async () => {
    fetcher.mockResolvedValue(response({ status: "incomplete", output: [output("부분 응답")] }));
    await expect(sendChat({ apiKey: "key", messages: history })).resolves.toEqual({
      text: "부분 응답", incomplete: true,
    });
  });

  it.each([
    ["REFUSAL", { status: "completed", output: [{ type: "message", content: [{ type: "refusal", refusal: "private-detail" }] }] }],
    ["INCOMPLETE", { status: "incomplete", output: [{ type: "reasoning" }] }],
    ["EMPTY_RESPONSE", { status: "completed", output: [] }],
    ["INVALID_RESPONSE", { output: "private-detail" }],
    ["API_ERROR", { status: "failed", output: [], error: { message: "private-detail" } }],
  ])("handles %s without displaying upstream details", async (code, body) => {
    fetcher.mockResolvedValue(response(body));
    const result = sendChat({ apiKey: "key", messages: history });
    await expect(result).rejects.toMatchObject({ code });
    await expect(result).rejects.not.toThrow("private-detail");
  });

  it.each([
    [400, "INVALID_REQUEST"], [401, "INVALID_API_KEY"], [403, "FORBIDDEN"],
    [404, "MODEL_NOT_FOUND"], [429, "RATE_LIMIT"], [500, "API_ERROR"],
  ])("sanitizes HTTP %s and never automatically retries", async (status, code) => {
    const upstream = response({ error: { message: "key=secret-key" } }, status);
    fetcher.mockResolvedValue(upstream);
    const result = sendChat({ apiKey: "secret-key", messages: history });
    await expect(result).rejects.toMatchObject({ code });
    await expect(result).rejects.not.toThrow("secret-key");
    expect(upstream.json).not.toHaveBeenCalled();
    expect(fetcher).toHaveBeenCalledTimes(1);
  });

  it("sanitizes network failures", async () => {
    fetcher.mockRejectedValue(new Error("Authorization: Bearer secret-key"));
    const result = sendChat({ apiKey: "secret-key", messages: history });
    await expect(result).rejects.toMatchObject({ code: "NETWORK" });
    await expect(result).rejects.not.toThrow("secret-key");
  });

  it("aborts an active fetch when the caller cancels, even if the transport stays pending", async () => {
    fetcher.mockImplementation(() => new Promise(() => undefined));
    const controller = new AbortController();
    const result = sendChat({ apiKey: "key", messages: history, signal: controller.signal });
    controller.abort();
    await expect(result).rejects.toMatchObject({ name: "AbortError", code: "CANCELLED" });
    expect(fetcher.mock.calls[0][1].signal.aborted).toBe(true);
    expect(jest.getTimerCount()).toBe(0);
  });

  it("does not send a request that was already cancelled", async () => {
    const controller = new AbortController();
    controller.abort();
    await expect(sendChat({ apiKey: "key", messages: history, signal: controller.signal }))
      .rejects.toMatchObject({ code: "CANCELLED" });
    expect(fetcher).not.toHaveBeenCalled();
  });

  it("aborts after 120 seconds and clears timeout resources", async () => {
    fetcher.mockImplementation(() => new Promise(() => undefined));
    const result = sendChat({ apiKey: "key", messages: history });
    const assertion = expect(result).rejects.toMatchObject({ code: "TIMEOUT" });
    await jest.advanceTimersByTimeAsync(120_000);
    await assertion;
    expect(fetcher.mock.calls[0][1].signal.aborted).toBe(true);
    expect(fetcher).toHaveBeenCalledTimes(1);
    expect(jest.getTimerCount()).toBe(0);
  });

  it("rejects missing credentials or empty messages before making a billable request", async () => {
    await expect(sendChat({ apiKey: "", messages: history })).rejects.toMatchObject({ code: "INVALID_API_KEY" });
    await expect(sendChat({ apiKey: "key", messages: [] })).rejects.toMatchObject({ code: "INVALID_MESSAGES" });
    expect(fetcher).not.toHaveBeenCalled();
  });
});
