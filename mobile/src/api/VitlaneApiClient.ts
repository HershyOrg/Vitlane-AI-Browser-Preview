export type FetchLike = (
  input: string,
  init?: RequestInit,
) => Promise<Response>;

export type AccessTokenProvider = () => Promise<string | null> | string | null;

export type ApiRequestOptions = {
  method?: "GET" | "POST" | "PUT" | "PATCH" | "DELETE";
  headers?: Readonly<Record<string, string>>;
  body?: unknown;
  signal?: AbortSignal;
  acceptedStatuses?: readonly number[];
  timeoutMilliseconds?: number;
};

type ErrorEnvelope = {
  error?: {
    code?: string;
    message?: string;
    retryable?: boolean;
    reasonCode?: string;
    resource?: string;
    retryAfter?: string;
  };
};

export class VitlaneApiError extends Error {
  public constructor(
    public readonly code: string,
    message: string,
    public readonly status: number,
    public readonly metadata: {
      readonly retryable: boolean;
      readonly reasonCode?: string;
      readonly resource?: string;
      readonly retryAfter?: string;
    },
  ) {
    super(message);
    this.name = "VitlaneApiError";
  }
}

export type VitlaneApiClientOptions = {
  /** Empty is valid for the Expo web same-origin preview. */
  baseUrl: string;
  fetch?: FetchLike;
  accessToken?: AccessTokenProvider;
  credentials?: "omit" | "same-origin";
  timeoutMilliseconds?: number;
};

/** A retry-free JSON client. Mutation replay belongs to the command layer. */
export class VitlaneApiClient {
  private readonly baseUrl: string;
  private readonly fetcher: FetchLike;
  private readonly accessToken?: AccessTokenProvider;
  private readonly credentials: "omit" | "same-origin";
  private readonly timeoutMilliseconds: number;

  public constructor(options: VitlaneApiClientOptions) {
    this.baseUrl = normalizeBaseUrl(options.baseUrl);
    const platformFetch = globalThis.fetch?.bind(globalThis) as FetchLike | undefined;
    const fetcher = options.fetch ?? platformFetch;
    if (!fetcher) throw new Error("VitlaneApiClient requires fetch");
    this.fetcher = fetcher;
    this.accessToken = options.accessToken;
    this.credentials = options.credentials ?? (options.accessToken ? "omit" : "same-origin");
    this.timeoutMilliseconds = options.timeoutMilliseconds ?? 15_000;
  }

  public async request<T>(path: string, options: ApiRequestOptions = {}): Promise<T> {
    if (!path.startsWith("/")) throw new TypeError("API path must start with /");
    const token = (await this.accessToken?.())?.trim();
    if (token && /[\s\r\n]/.test(token)) {
      throw new Error("Access token has an invalid format");
    }
    const headers: Record<string, string> = {
      Accept: "application/json",
      ...options.headers,
    };
    if (options.body !== undefined) headers["Content-Type"] = "application/json";
    if (token) headers.Authorization = `Bearer ${token}`;

    const controller = new AbortController();
    const abortFromCaller = () => controller.abort(options.signal?.reason);
    if (options.signal?.aborted) abortFromCaller();
    else options.signal?.addEventListener("abort", abortFromCaller, { once: true });
    const timeout = setTimeout(
      () => controller.abort(new Error("Vitlane API request timed out")),
      options.timeoutMilliseconds ?? this.timeoutMilliseconds,
    );
    try {
      const response = await this.fetcher(`${this.baseUrl}${path}`, {
        method: options.method ?? "GET",
        credentials: this.credentials,
        headers,
        ...(options.body === undefined ? {} : { body: JSON.stringify(options.body) }),
        signal: controller.signal,
      });
      return await decodeResponse<T>(response, options.acceptedStatuses ?? []);
    } finally {
      clearTimeout(timeout);
      options.signal?.removeEventListener("abort", abortFromCaller);
    }
  }
}

function normalizeBaseUrl(value: string): string {
  const normalized = value.trim().replace(/\/+$/, "");
  if (!normalized) return "";
  const parsed = new URL(normalized);
  if (!["http:", "https:"].includes(parsed.protocol) || parsed.username || parsed.password) {
    throw new TypeError("API base URL must be an HTTP(S) origin without credentials");
  }
  if (parsed.pathname !== "/" || parsed.search || parsed.hash) {
    throw new TypeError("API base URL must not contain a path, query, or fragment");
  }
  return normalized;
}

async function decodeResponse<T>(
  response: Response,
  acceptedStatuses: readonly number[],
): Promise<T> {
  if (response.status === 204) return undefined as T;
  let body: unknown;
  try {
    body = await response.json();
  } catch {
    throw new VitlaneApiError(
      "INVALID_RESPONSE",
      "The server returned an invalid JSON response.",
      response.status,
      { retryable: response.status >= 500 },
    );
  }
  if (!response.ok && !acceptedStatuses.includes(response.status)) {
    const envelope = body as ErrorEnvelope;
    throw new VitlaneApiError(
      envelope.error?.code ?? "INTERNAL_ERROR",
      envelope.error?.message ?? "The request could not be completed.",
      response.status,
      {
        retryable: envelope.error?.retryable ?? false,
        ...(envelope.error?.reasonCode ? { reasonCode: envelope.error.reasonCode } : {}),
        ...(envelope.error?.resource ? { resource: envelope.error.resource } : {}),
        ...(envelope.error?.retryAfter ? { retryAfter: envelope.error.retryAfter } : {}),
      },
    );
  }
  return body as T;
}
