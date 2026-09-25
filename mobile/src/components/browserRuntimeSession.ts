import type {
  BrowserHandoffEvent,
  BrowserPageIdentityEvent,
  SanitizedObservation,
} from "../../modules/vitlane-browser";
import type { BrowserControlMode } from "./BrowserRunPanel";

export type BrowserRuntimePhase =
  | "loading"
  | "product"
  | "sign_in_required"
  | "verifying"
  | "signed_in"
  | "session_unknown"
  | "checkout_ready"
  | "payment_handoff"
  | "needs_user"
  | "unavailable"
  | "error";

export type BrowserRuntimeSurface = "ios-native" | "android-host-required" | "web-review";

type ResumeGate = {
  readonly requestId: number;
  readonly requiredIdentityRevision: number;
  readonly previousObservationId?: string;
};

export type BrowserRuntimeState = {
  readonly candidateUrl: string;
  readonly phase: BrowserRuntimePhase;
  readonly controlMode: BrowserControlMode;
  readonly origin: string;
  readonly nativeOriginVerified: boolean;
  readonly identityRevision: number;
  readonly pageIdentity?: BrowserPageIdentityEvent;
  readonly lastObservationId?: string;
  readonly sessionStateHint?: SanitizedObservation["untrustedPageData"]["sessionStateHint"];
  readonly resumeGate?: ResumeGate;
  readonly handoffReason?: BrowserHandoffEvent["reason"];
  readonly errorCode?: string;
  readonly surface: BrowserRuntimeSurface;
  /** Remains false until an owner-bound server Device Channel is connected. */
  readonly deviceChannelAvailable: false;
};

export type BrowserRuntimeEvent =
  | { readonly type: "CANDIDATE_CHANGED"; readonly candidateUrl: string }
  | { readonly type: "PAGE_IDENTITY"; readonly event: BrowserPageIdentityEvent }
  | { readonly type: "HANDOFF"; readonly event: BrowserHandoffEvent }
  | { readonly type: "TAKE_OVER" }
  | { readonly type: "STOP" }
  | { readonly type: "REQUEST_RESUME_VERIFICATION" }
  | { readonly type: "OBSERVATION"; readonly observation: SanitizedObservation }
  | { readonly type: "VERIFICATION_FAILED"; readonly code: string }
  | { readonly type: "NAVIGATION_FAILED"; readonly code: string }
  | { readonly type: "APPROVE_CHECKOUT" };

export function createBrowserRuntimeState(
  candidateUrl: string,
  surface: BrowserRuntimeSurface,
): BrowserRuntimeState {
  return {
    candidateUrl,
    phase: surface === "ios-native" ? "loading" : "unavailable",
    controlMode: "paused",
    origin: displayOrigin(candidateUrl),
    nativeOriginVerified: false,
    identityRevision: 0,
    surface,
    deviceChannelAvailable: false,
  };
}

export function reduceBrowserRuntime(
  state: BrowserRuntimeState,
  event: BrowserRuntimeEvent,
): BrowserRuntimeState {
  switch (event.type) {
    case "CANDIDATE_CHANGED":
      if (event.candidateUrl === state.candidateUrl) return state;
      return createBrowserRuntimeState(event.candidateUrl, state.surface);
    case "PAGE_IDENTITY": {
      const identityRevision = state.identityRevision + 1;
      const secure = event.event.securityState === "secure";
      return {
        ...state,
        phase: state.phase === "loading" ? "product" : state.phase,
        origin: displayOrigin(event.event.page.origin),
        nativeOriginVerified: secure,
        identityRevision,
        pageIdentity: event.event,
        errorCode: undefined,
      };
    }
    case "HANDOFF": {
      const phase = handoffPhase(event.event.reason);
      return {
        ...state,
        phase,
        controlMode: "paused",
        origin: displayOrigin(event.event.origin) || state.origin,
        handoffReason: event.event.reason,
        resumeGate: undefined,
        errorCode: undefined,
      };
    }
    case "TAKE_OVER":
      return { ...state, controlMode: "user" };
    case "STOP":
      return { ...state, controlMode: "paused", resumeGate: undefined };
    case "REQUEST_RESUME_VERIFICATION": {
      if (!canRequestResumeVerification(state)) return state;
      const requestId = (state.resumeGate?.requestId ?? 0) + 1;
      return {
        ...state,
        phase: "verifying",
        controlMode: "paused",
        handoffReason: undefined,
        errorCode: undefined,
        resumeGate: {
          requestId,
          requiredIdentityRevision: state.identityRevision + 1,
          ...(state.lastObservationId
            ? { previousObservationId: state.lastObservationId }
            : {}),
        },
      };
    }
    case "OBSERVATION":
      if (!isFreshResumeObservation(state, event.observation)) return state;
      const sessionStateHint = event.observation.untrustedPageData.sessionStateHint;
      return {
        ...state,
        phase: sessionStateHint === "authenticated"
          ? "signed_in"
          : sessionStateHint === "anonymous"
            ? "sign_in_required"
            : "needs_user",
        // There is no Device Channel yet. A local one-shot observation does
        // not grant the model ongoing browser control, so the user keeps it.
        controlMode: "user",
        lastObservationId: event.observation.observationId,
        sessionStateHint,
        resumeGate: undefined,
        handoffReason: undefined,
        errorCode: undefined,
      };
    case "VERIFICATION_FAILED":
      return {
        ...state,
        phase: "sign_in_required",
        controlMode: "user",
        resumeGate: undefined,
        errorCode: event.code,
      };
    case "NAVIGATION_FAILED":
      return {
        ...state,
        phase: "error",
        controlMode: "paused",
        resumeGate: undefined,
        errorCode: event.code,
      };
    case "APPROVE_CHECKOUT":
      if (!canApproveCheckout(state)) return state;
      return {
        ...state,
        phase: "payment_handoff",
        controlMode: "user",
        handoffReason: "payment",
        resumeGate: undefined,
      };
  }
}

export function canRequestResumeVerification(state: BrowserRuntimeState): boolean {
  return state.controlMode === "user"
    && (state.phase === "sign_in_required"
      || state.phase === "session_unknown"
      || state.phase === "needs_user");
}

export function canApproveCheckout(state: BrowserRuntimeState): boolean {
  return state.phase === "checkout_ready" && state.nativeOriginVerified;
}

export function shouldCollectResumeObservation(state: BrowserRuntimeState): boolean {
  return Boolean(
    state.phase === "verifying"
      && state.resumeGate
      && state.pageIdentity
      && state.identityRevision >= state.resumeGate.requiredIdentityRevision,
  );
}

export function isFreshResumeObservation(
  state: BrowserRuntimeState,
  observation: SanitizedObservation,
): boolean {
  const gate = state.resumeGate;
  const identity = state.pageIdentity;
  if (!gate || !identity || state.identityRevision < gate.requiredIdentityRevision) return false;
  if (identity.securityState !== "secure" || !identity.page.foreground) return false;
  if (observation.privacy.collectionStatus !== "sanitized") return false;
  if (observation.observationId === gate.previousObservationId) return false;
  return observation.nativeMetadata.documentEpoch === identity.page.documentEpoch
    && observation.nativeMetadata.topOrigin === identity.page.origin
    && observation.nativeMetadata.frameOrigin === identity.page.origin;
}

function handoffPhase(reason: BrowserHandoffEvent["reason"]): BrowserRuntimePhase {
  if (reason === "login" || reason === "verification") return "sign_in_required";
  if (reason === "payment" || reason === "final_action") return "checkout_ready";
  return "needs_user";
}

function displayOrigin(value: string): string {
  try {
    return new URL(value).hostname || value;
  } catch {
    return value;
  }
}
