export type ChatMessage = {
  id: string;
  role: "user" | "assistant";
  content: string;
};

export const DEFAULT_MODEL = "gpt-5-mini";

export type SendChatOptions = {
  apiKey: string;
  model?: string;
  messages: readonly ChatMessage[];
  signal?: AbortSignal;
};

export class ChatError extends Error {
  public constructor(public readonly code: string, message: string) {
    super(message);
    this.name = code === "CANCELLED" ? "AbortError" : "ChatError";
  }
}

const REQUEST_TIMEOUT_MS = 120_000;
const RESPONSES_URL = "https://api.openai.com/v1/responses";

function record(value: unknown): Record<string, unknown> | null {
  return typeof value === "object" && value !== null && !Array.isArray(value)
    ? value as Record<string, unknown>
    : null;
}

function httpError(status: number): ChatError {
  switch (status) {
    case 400:
      return new ChatError("INVALID_REQUEST", "요청을 처리할 수 없습니다. 모델 이름과 대화 내용을 확인해 주세요.");
    case 401:
      return new ChatError("INVALID_API_KEY", "API 키가 올바르지 않거나 만료되었습니다. 설정에서 키를 확인해 주세요.");
    case 403:
      return new ChatError("FORBIDDEN", "이 API 키로 요청한 모델을 사용할 수 없습니다. API 접근 권한을 확인해 주세요.");
    case 404:
      return new ChatError("MODEL_NOT_FOUND", "모델을 찾을 수 없습니다. 설정에서 사용 가능한 모델 이름을 확인해 주세요.");
    case 429:
      return new ChatError("RATE_LIMIT", "요청 한도에 도달했거나 API 사용 잔액이 부족합니다. 사용량을 확인한 뒤 다시 시도해 주세요.");
    default:
      return new ChatError("API_ERROR", status >= 500
        ? "OpenAI 서비스에 일시적인 문제가 있습니다. 잠시 후 다시 시도해 주세요."
        : "OpenAI 요청에 실패했습니다. 설정을 확인한 뒤 다시 시도해 주세요.");
  }
}

function readReply(payload: unknown): { text: string; incomplete: boolean } {
  const response = record(payload);
  if (!response || !Array.isArray(response.output)) {
    throw new ChatError("INVALID_RESPONSE", "응답을 읽을 수 없습니다. 다시 시도해 주세요.");
  }
  if (response.error || response.status === "failed" || response.status === "cancelled") {
    throw new ChatError("API_ERROR", "OpenAI가 응답을 완료하지 못했습니다. 다시 시도해 주세요.");
  }

  const parts: string[] = [];
  let refused = false;
  for (const item of response.output) {
    const message = record(item);
    if (message?.type !== "message" || !Array.isArray(message.content)) continue;
    for (const value of message.content) {
      const content = record(value);
      if (content?.type === "output_text" && typeof content.text === "string") {
        parts.push(content.text);
      }
      if (content?.type === "refusal") refused = true;
    }
  }

  const text = parts.join("\n\n").trim();
  const incomplete = response.status === "incomplete";
  if (text) return { text, incomplete };
  if (refused) {
    throw new ChatError("REFUSAL", "이 요청에는 답변할 수 없습니다. 내용을 바꿔 다시 시도해 주세요.");
  }
  throw new ChatError(incomplete ? "INCOMPLETE" : "EMPTY_RESPONSE", incomplete
    ? "응답이 완료되기 전에 중단되었습니다. 요청을 줄여 다시 시도해 주세요."
    : "텍스트 응답을 받지 못했습니다. 다시 시도해 주세요.");
}

/** Sends history directly to OpenAI; credentials are never included in the body. */
export async function sendChat({
  apiKey,
  model = DEFAULT_MODEL,
  messages,
  signal,
}: SendChatOptions): Promise<{ text: string; incomplete: boolean }> {
  if (signal?.aborted) throw new ChatError("CANCELLED", "요청을 중지했습니다.");
  const key = apiKey.trim();
  const selectedModel = model.trim() || DEFAULT_MODEL;
  if (!key || /\s/.test(key)) {
    throw new ChatError("INVALID_API_KEY", "설정에서 올바른 OpenAI API 키를 입력해 주세요.");
  }
  if (/\s/.test(selectedModel) || selectedModel.length > 200) {
    throw new ChatError("INVALID_MODEL", "설정에서 올바른 모델 이름을 입력해 주세요.");
  }
  if (!messages.length || messages.some(message => (
    (message.role !== "user" && message.role !== "assistant")
      || typeof message.content !== "string" || !message.content.trim()
  ))) {
    throw new ChatError("INVALID_MESSAGES", "보낼 메시지를 입력해 주세요.");
  }

  const controller = new AbortController();
  let cancelled = false;
  let timedOut = false;
  const onCancel = () => {
    cancelled = true;
    controller.abort();
  };
  signal?.addEventListener("abort", onCancel, { once: true });
  let onAbort: () => void = () => undefined;
  const aborted = new Promise<never>((_resolve, reject) => {
    onAbort = () => reject(new ChatError(
      timedOut ? "TIMEOUT" : "CANCELLED",
      timedOut ? "응답 시간이 초과되었습니다. 다시 시도해 주세요." : "요청을 중지했습니다.",
    ));
    controller.signal.addEventListener("abort", onAbort, { once: true });
  });
  const timeout = setTimeout(() => {
    timedOut = true;
    controller.abort();
  }, REQUEST_TIMEOUT_MS);

  try {
    const request = async () => {
      const response = await fetch(RESPONSES_URL, {
        method: "POST",
        headers: {
          Authorization: `Bearer ${key}`,
          "Content-Type": "application/json",
          Accept: "application/json",
        },
        credentials: "omit",
        body: JSON.stringify({
          model: selectedModel,
          input: messages.map(({ role, content }) => ({ role, content })),
          max_output_tokens: 4096,
          store: false,
        }),
        signal: controller.signal,
      });
      // Never display or log upstream error bodies, which can contain credentials.
      if (!response.ok) throw httpError(response.status);
      let payload: unknown;
      try {
        payload = await response.json();
      } catch {
        throw new ChatError("INVALID_RESPONSE", "응답을 읽을 수 없습니다. 다시 시도해 주세요.");
      }
      return readReply(payload);
    };
    return await Promise.race([request(), aborted]);
  } catch (error) {
    if (cancelled) throw new ChatError("CANCELLED", "요청을 중지했습니다.");
    if (timedOut) throw new ChatError("TIMEOUT", "응답 시간이 초과되었습니다. 다시 시도해 주세요.");
    if (error instanceof ChatError) throw error;
    throw new ChatError("NETWORK", "OpenAI에 연결할 수 없습니다. 인터넷 연결을 확인해 주세요.");
  } finally {
    clearTimeout(timeout);
    signal?.removeEventListener("abort", onCancel);
    controller.signal.removeEventListener("abort", onAbort);
  }
}
