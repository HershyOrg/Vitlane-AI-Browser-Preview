import { analyticsHeaders } from "../analytics/analytics";
import type { APIErrorPayload } from "./types";
import { localizeFixedCopy } from "../i18n";

const API_BASE = import.meta.env.VITE_API_URL ?? "";

export const operatorFreshAuthRequiredEvent =
  "vitlane:operator-fresh-auth-required";
export const authSessionExpiredEvent = "vitlane:auth-session-expired";

export class APIError extends Error {
  readonly code: string;
  readonly status: number;
  readonly retryable: boolean;
  readonly reasonCode?: string;
  readonly resource?: string;
  readonly retryAfter?: string;
  readonly agencyOrderId?: string;
  readonly itemTitle?: string;

  constructor(
    code: string,
    message: string,
    status: number,
    metadata: {
      retryable?: boolean;
      reasonCode?: string;
      resource?: string;
      retryAfter?: string;
      agencyOrderId?: string;
      itemTitle?: string;
    } = {},
  ) {
    super(message);
    this.code = code;
    this.status = status;
    this.retryable = metadata.retryable ?? false;
    this.reasonCode = metadata.reasonCode;
    this.resource = metadata.resource;
    this.retryAfter = metadata.retryAfter;
    this.agencyOrderId = metadata.agencyOrderId;
    this.itemTitle = metadata.itemTitle;
  }
}

export async function request<T>(
  path: string,
  init: RequestInit = {},
  options: { acceptedStatuses?: readonly number[] } = {},
): Promise<T> {
  const formDataBody = typeof FormData !== "undefined" && init.body instanceof FormData;
  const response = await fetch(`${API_BASE}${path}`, {
    ...init,
    credentials: "same-origin",
    headers: formDataBody ? init.headers : {
      "Content-Type": "application/json",
      ...init.headers,
      ...analyticsHeaders(`${API_BASE}${path}`, init.method),
    },
  });
  return decode<T>(response, path, options.acceptedStatuses);
}

async function decode<T>(
  response: Response,
  path: string,
  acceptedStatuses: readonly number[] = [],
): Promise<T> {
  if (response.status === 204) {
    return undefined as T;
  }
  const body = (await response.json()) as T | APIErrorPayload;
  if (!response.ok && !acceptedStatuses.includes(response.status)) {
    const error = body as APIErrorPayload;
    const code = error.error?.code ?? "INTERNAL_ERROR";
    if (code === "FRESH_AUTH_REQUIRED" && typeof window !== "undefined") {
      window.dispatchEvent(new CustomEvent(operatorFreshAuthRequiredEvent));
    }
    if (
      response.status === 401 &&
      code !== "FRESH_AUTH_REQUIRED" &&
      !isAuthenticationProbe(path) &&
      typeof window !== "undefined"
    ) {
      window.dispatchEvent(new CustomEvent(authSessionExpiredEvent));
    }
    throw new APIError(
      code,
      error.error?.message ?? localizeFixedCopy(
        "We couldn't process the request.",
        "요청을 처리하지 못했습니다.",
      ),
      response.status,
      {
        retryable: error.error?.retryable,
        reasonCode: error.error?.reasonCode,
        resource: error.error?.resource,
        retryAfter: error.error?.retryAfter,
        agencyOrderId: error.error?.agencyOrderId,
        itemTitle: error.error?.itemTitle,
      },
    );
  }
  return body as T;
}

function isAuthenticationProbe(path: string) {
  const pathname = path.split("?", 1)[0];
  return pathname === "/api/v1/me" || pathname.startsWith("/api/v1/auth/");
}
