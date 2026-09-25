import type { Ref } from "react";

import type { BrowserSessionPresentation } from "./BrowserSessionSurface";
import type { BrowserControlMode } from "./BrowserRunPanel";
import type { BrowserRuntimeState, BrowserRuntimeSurface } from "./browserRuntimeSession";

export type BrowserRuntimeViewportHandle = {
  takeOver(): Promise<void>;
  stop(): Promise<void>;
  requestResumeVerification(): Promise<void>;
  approveCheckout(): Promise<void>;
};

export type BrowserRuntimeViewportProps = {
  readonly ref?: Ref<BrowserRuntimeViewportHandle>;
  readonly runtimeMode: "fixture" | "server" | "direct";
  readonly candidateUrl: string;
  readonly runId: string;
  readonly session: BrowserSessionPresentation;
  readonly controlMode: BrowserControlMode;
  readonly onRuntimeStateChange?: (state: BrowserRuntimeState) => void;
  /** Marks a non-authoritative server finding preview with no durable BrowserRun. */
  readonly reviewMode?: boolean;
  readonly onFixturePageReady?: () => void;
  readonly onFixturePreparePurchase?: () => void;
  readonly onFixtureSignInCompleted?: () => void;
  readonly onFixturePrepareCheckout?: () => void;
};

export function browserRuntimeSurface(
  runtimeMode: BrowserRuntimeViewportProps["runtimeMode"],
  platform: string,
): BrowserRuntimeSurface {
  if ((runtimeMode === "server" || runtimeMode === "direct") && platform === "ios") return "ios-native";
  if ((runtimeMode === "server" || runtimeMode === "direct") && platform === "android") return "android-host-required";
  return "web-review";
}
