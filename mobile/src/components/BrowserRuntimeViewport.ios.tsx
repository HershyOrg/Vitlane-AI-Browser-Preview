import { forwardRef, useEffect, useImperativeHandle, useMemo, useReducer, useRef } from "react";
import { StyleSheet } from "react-native";

import {
  VitlaneBrowserView,
  type BrowserControlEvent,
  type BrowserErrorEvent,
  type BrowserHandoffEvent,
  type BrowserPageIdentityEvent,
  type SanitizedObservation,
  type VitlaneBrowserRef,
} from "../../modules/vitlane-browser";
import { BrowserSessionSurface } from "./BrowserSessionSurface";
import type {
  BrowserRuntimeViewportHandle,
  BrowserRuntimeViewportProps,
} from "./BrowserRuntimeViewport.types";
import {
  canApproveCheckout,
  canRequestResumeVerification,
  createBrowserRuntimeState,
  reduceBrowserRuntime,
  shouldCollectResumeObservation,
} from "./browserRuntimeSession";

/**
 * The WKWebView remains mounted for the lifetime of this component. Changing
 * login, review, and payment presentation state never replaces the browser or
 * its app-scoped website data store.
 */
export const BrowserRuntimeViewport = forwardRef<
  BrowserRuntimeViewportHandle,
  BrowserRuntimeViewportProps
>(function BrowserRuntimeViewport(
  props,
  ref,
) {
  const { runtimeMode, candidateUrl, onRuntimeStateChange } = props;
  const nativeRef = useRef<VitlaneBrowserRef>(null);
  const verifyingKey = useRef<string | undefined>(undefined);
  const verificationSequence = useRef(0);
  const [runtime, dispatch] = useReducer(
    reduceBrowserRuntime,
    undefined,
    () => createBrowserRuntimeState(candidateUrl, "ios-native"),
  );
  const runtimeRef = useRef(runtime);
  runtimeRef.current = runtime;
  const directRuntime = useMemo(() => {
    const current = runtime.candidateUrl === candidateUrl
      ? runtime
      : createBrowserRuntimeState(candidateUrl, "ios-native");
    return {
      ...current,
      phase: current.phase === "error" ? "error" as const : "needs_user" as const,
      controlMode: "user" as const,
    };
  }, [candidateUrl, runtime]);

  useEffect(
    () => onRuntimeStateChange?.(runtimeMode === "direct" ? directRuntime : runtime),
    [directRuntime, onRuntimeStateChange, runtime, runtimeMode],
  );

  useEffect(() => {
    verifyingKey.current = undefined;
    dispatch({ type: "CANDIDATE_CHANGED", candidateUrl });
  }, [candidateUrl]);

  useEffect(() => {
    if (runtimeMode !== "server" || !shouldCollectResumeObservation(runtime)) return;
    const gate = runtime.resumeGate;
    const identity = runtime.pageIdentity;
    if (!gate || !identity) return;
    const key = `${gate.requestId}:${runtime.identityRevision}`;
    if (verifyingKey.current === key) return;
    verifyingKey.current = key;
    const sequence = ++verificationSequence.current;

    void (async () => {
      try {
        const browser = nativeRef.current;
        if (!browser) throw new Error("native_browser_missing");
        await browser.beginRun({
          runId: localVerificationRunId(props.runId, sequence),
          deviceId: "ios-local-verifier",
          profileRef: "merchant-default",
          leaseEpoch: sequence,
        });
        // onObservation is the authority for completing the gate. The promise
        // only tells us when it is safe to return the browser to user control.
        await browser.observe();
        await browser.takeOver();
      } catch (error) {
        try {
          await nativeRef.current?.takeOver();
        } catch {
          // The browser may already have handed control back while navigating.
        }
        dispatch({ type: "VERIFICATION_FAILED", code: browserErrorCode(error) });
      }
    })();
  }, [props, runtime, runtimeMode]);

  useImperativeHandle(ref, () => ({
    takeOver: async () => {
      await nativeRef.current?.takeOver();
      if (runtimeMode !== "direct") dispatch({ type: "TAKE_OVER" });
    },
    stop: async () => {
      if (runtimeMode === "direct") return;
      await nativeRef.current?.stopAgent("react_native_stop");
      dispatch({ type: "STOP" });
    },
    requestResumeVerification: async () => {
      if (runtimeMode === "direct") return;
      if (!canRequestResumeVerification(runtimeRef.current)) return;
      dispatch({ type: "REQUEST_RESUME_VERIFICATION" });
      // A reload guarantees that completion is based on a page identity event
      // emitted after the user requested resume, while preserving cookies.
      await nativeRef.current?.reload();
    },
    approveCheckout: async () => {
      if (runtimeMode === "direct") return;
      if (!canApproveCheckout(runtimeRef.current)) return;
      await nativeRef.current?.takeOver();
      dispatch({ type: "APPROVE_CHECKOUT" });
    },
  }), [runtimeMode]);

  if (runtimeMode === "fixture") {
    return (
      <BrowserSessionSurface
        controlMode={props.controlMode}
        onPageReady={props.onFixturePageReady}
        onPrepareCheckout={props.onFixturePrepareCheckout}
        onPreparePurchase={props.onFixturePreparePurchase}
        onSignInCompleted={props.onFixtureSignInCompleted}
        reviewMode={props.reviewMode ?? false}
        session={props.session}
      />
    );
  }

  return (
    <VitlaneBrowserView
      onControlStateChange={({ nativeEvent }: { nativeEvent: BrowserControlEvent }) => {
        if (runtimeMode === "direct") return;
        // `agent` here is the short local sanitization gate. It is deliberately
        // not projected as ongoing AI control without a server Device Channel.
        if (nativeEvent.mode === "user") dispatch({ type: "TAKE_OVER" });
        if (nativeEvent.mode === "stopped") dispatch({ type: "STOP" });
      }}
      onHandoff={({ nativeEvent }: { nativeEvent: BrowserHandoffEvent }) => {
        if (runtimeMode === "direct") return;
        dispatch({ type: "HANDOFF", event: nativeEvent });
      }}
      onNavigationError={({ nativeEvent }: { nativeEvent: BrowserErrorEvent }) => {
        dispatch({ type: "NAVIGATION_FAILED", code: nativeEvent.code });
      }}
      onObservation={({ nativeEvent }: { nativeEvent: SanitizedObservation }) => {
        if (runtimeMode === "direct") return;
        dispatch({ type: "OBSERVATION", observation: nativeEvent });
      }}
      onPageIdentity={({ nativeEvent }: { nativeEvent: BrowserPageIdentityEvent }) => {
        dispatch({ type: "PAGE_IDENTITY", event: nativeEvent });
      }}
      ref={nativeRef}
      style={styles.browser}
      testID="browser.native-viewport"
      url={candidateUrl}
    />
  );
});

function localVerificationRunId(base: string, sequence: number): string {
  const normalized = base.replace(/[^A-Za-z0-9._:-]/g, "-").slice(0, 96) || "browser-run";
  return `${normalized}:verify:${sequence}`;
}

function browserErrorCode(error: unknown): string {
  if (error instanceof Error && error.message) return error.message.slice(0, 120);
  return "resume_verification_failed";
}

const styles = StyleSheet.create({ browser: { flex: 1, minHeight: 420, width: "100%" } });
