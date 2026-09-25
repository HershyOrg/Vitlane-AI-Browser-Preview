import type {
  BrowserPageIdentityEvent,
  SanitizedObservation,
} from "../../modules/vitlane-browser";
import {
  canApproveCheckout,
  canRequestResumeVerification,
  createBrowserRuntimeState,
  reduceBrowserRuntime,
  shouldCollectResumeObservation,
} from "./browserRuntimeSession";
import { browserRuntimeSurface } from "./BrowserRuntimeViewport.types";

const identity = (documentEpoch: number): BrowserPageIdentityEvent => ({
  page: {
    tabId: "ios-main",
    frameId: "frame_main",
    documentEpoch,
    observationId: null,
    url: "https://shop.example/product/runner",
    origin: "https://shop.example",
    title: "Runner",
    foreground: true,
  },
  securityState: "secure",
});

const observation = (
  documentEpoch: number,
  observationId: string,
  sessionStateHint: "authenticated" | "anonymous" | "unknown",
): SanitizedObservation => ({
  observationId,
  nativeMetadata: {
    tabId: "ios-main",
    frameId: "frame_main",
    documentEpoch,
    topOrigin: "https://shop.example",
    frameOrigin: "https://shop.example",
    pathname: "/product/runner",
    queryOrFragmentPresent: false,
    foreground: true,
  },
  untrustedPageData: {
    pageTypeHint: "product",
    sessionStateHint,
    title: "Runner",
    visibleText: "Public product details",
    candidates: [],
  },
  privacy: {
    inputValuesOmitted: true,
    secretsOmitted: true,
    screenshotIncluded: false,
    urlQueryAndFragmentOmitted: true,
    collectionStatus: "sanitized",
    handoffReasonCodes: [],
    excludedBoundaryCodes: ["UNSUPPORTED_INTERACTION"],
  },
});

function requestLoginResume(
  state: ReturnType<typeof createBrowserRuntimeState>,
): ReturnType<typeof createBrowserRuntimeState> {
  state = reduceBrowserRuntime(state, {
    type: "HANDOFF",
    event: {
      reason: "login",
      message: "Sign in",
      origin: "https://shop.example",
      controlGeneration: 2,
    },
  });
  state = reduceBrowserRuntime(state, { type: "TAKE_OVER" });
  return reduceBrowserRuntime(state, { type: "REQUEST_RESUME_VERIFICATION" });
}

describe("browser runtime resume gate", () => {
  it("selects an honest host for each platform", () => {
    expect(browserRuntimeSurface("server", "ios")).toBe("ios-native");
    expect(browserRuntimeSurface("server", "android")).toBe("android-host-required");
    expect(browserRuntimeSurface("server", "web")).toBe("web-review");
    expect(browserRuntimeSurface("fixture", "ios")).toBe("web-review");
    expect(browserRuntimeSurface("direct", "ios")).toBe("ios-native");
    expect(browserRuntimeSurface("direct", "android")).toBe("android-host-required");
    expect(browserRuntimeSurface("direct", "web")).toBe("web-review");
  });

  it("requires a newly emitted identity and matching fresh observation", () => {
    let state = createBrowserRuntimeState("https://shop.example/product/runner", "ios-native");
    state = reduceBrowserRuntime(state, { type: "PAGE_IDENTITY", event: identity(1) });
    state = requestLoginResume(state);

    expect(shouldCollectResumeObservation(state)).toBe(false);
    expect(reduceBrowserRuntime(state, {
      type: "OBSERVATION",
      observation: observation(1, "obs-too-early", "authenticated"),
    })).toBe(state);

    state = reduceBrowserRuntime(state, { type: "PAGE_IDENTITY", event: identity(2) });
    expect(shouldCollectResumeObservation(state)).toBe(true);
    expect(reduceBrowserRuntime(state, {
      type: "OBSERVATION",
      observation: observation(1, "obs-wrong-document", "authenticated"),
    })).toBe(state);

    state = reduceBrowserRuntime(state, {
      type: "OBSERVATION",
      observation: observation(2, "obs-authenticated", "authenticated"),
    });
    expect(state.phase).toBe("signed_in");
    expect(state.controlMode).toBe("user");
    expect(state.lastObservationId).toBe("obs-authenticated");
  });

  it("only opens the resume gate while the user is handling a browser handoff", () => {
    let state = createBrowserRuntimeState("https://shop.example/product/runner", "ios-native");
    state = reduceBrowserRuntime(state, { type: "PAGE_IDENTITY", event: identity(1) });
    expect(canRequestResumeVerification(state)).toBe(false);
    expect(reduceBrowserRuntime(state, { type: "REQUEST_RESUME_VERIFICATION" })).toBe(state);

    state = reduceBrowserRuntime(state, {
      type: "HANDOFF",
      event: {
        reason: "login",
        message: "Sign in",
        origin: "https://shop.example",
        controlGeneration: 2,
      },
    });
    state = reduceBrowserRuntime(state, { type: "TAKE_OVER" });
    expect(canRequestResumeVerification(state)).toBe(true);
    expect(reduceBrowserRuntime(state, { type: "REQUEST_RESUME_VERIFICATION" }).phase).toBe("verifying");
  });

  it("does not claim sign-in from an anonymous or unknown page reload", () => {
    for (const [hint, expectedPhase] of [
      ["anonymous", "sign_in_required"],
      ["unknown", "needs_user"],
    ] as const) {
      let state = createBrowserRuntimeState("https://shop.example/product/runner", "ios-native");
      state = reduceBrowserRuntime(state, { type: "PAGE_IDENTITY", event: identity(1) });
      state = requestLoginResume(state);
      state = reduceBrowserRuntime(state, { type: "PAGE_IDENTITY", event: identity(2) });
      state = reduceBrowserRuntime(state, {
        type: "OBSERVATION",
        observation: observation(2, `obs-${hint}`, hint),
      });

      expect(state.phase).toBe(expectedPhase);
      expect(state.phase).not.toBe("signed_in");
      expect(state.sessionStateHint).toBe(hint);
      expect(state.controlMode).toBe("user");
    }
  });

  it("clears the previous run presentation when a different candidate opens", () => {
    let state = createBrowserRuntimeState("https://shop.example/product/runner", "ios-native");
    state = reduceBrowserRuntime(state, { type: "PAGE_IDENTITY", event: identity(1) });
    state = requestLoginResume(state);
    state = reduceBrowserRuntime(state, { type: "PAGE_IDENTITY", event: identity(2) });
    state = reduceBrowserRuntime(state, {
      type: "OBSERVATION",
      observation: observation(2, "obs-authenticated", "authenticated"),
    });
    expect(state.phase).toBe("signed_in");

    state = reduceBrowserRuntime(state, {
      type: "CANDIDATE_CHANGED",
      candidateUrl: "https://other-shop.example/product/new",
    });

    expect(state.phase).toBe("loading");
    expect(state.candidateUrl).toBe("https://other-shop.example/product/new");
    expect(state.pageIdentity).toBeUndefined();
    expect(state.lastObservationId).toBeUndefined();
    expect(state.nativeOriginVerified).toBe(false);
  });

  it("moves payment handoff to approval before final user control", () => {
    let state = createBrowserRuntimeState("https://shop.example/product/runner", "ios-native");
    state = reduceBrowserRuntime(state, { type: "PAGE_IDENTITY", event: identity(1) });
    state = reduceBrowserRuntime(state, {
      type: "HANDOFF",
      event: {
        reason: "payment",
        message: "Payment page",
        origin: "https://shop.example",
        controlGeneration: 4,
      },
    });
    expect(state.phase).toBe("checkout_ready");
    expect(state.controlMode).toBe("paused");
    expect(canApproveCheckout(state)).toBe(true);

    state = reduceBrowserRuntime(state, { type: "APPROVE_CHECKOUT" });
    expect(state.phase).toBe("payment_handoff");
    expect(state.controlMode).toBe("user");
  });

  it("does not accept checkout approval outside a secure native checkout boundary", () => {
    const productState = reduceBrowserRuntime(
      createBrowserRuntimeState("https://shop.example/product/runner", "ios-native"),
      { type: "PAGE_IDENTITY", event: identity(1) },
    );
    expect(reduceBrowserRuntime(productState, { type: "APPROVE_CHECKOUT" })).toBe(productState);

    const insecureIdentity: BrowserPageIdentityEvent = {
      ...identity(2),
      securityState: "insecure",
    };
    let checkoutState = reduceBrowserRuntime(
      createBrowserRuntimeState("http://shop.example/product/runner", "ios-native"),
      { type: "PAGE_IDENTITY", event: insecureIdentity },
    );
    checkoutState = reduceBrowserRuntime(checkoutState, {
      type: "HANDOFF",
      event: {
        reason: "payment",
        message: "Payment page",
        origin: "http://shop.example",
        controlGeneration: 4,
      },
    });
    expect(checkoutState.phase).toBe("checkout_ready");
    expect(canApproveCheckout(checkoutState)).toBe(false);
    expect(reduceBrowserRuntime(checkoutState, { type: "APPROVE_CHECKOUT" })).toBe(checkoutState);
  });
});
