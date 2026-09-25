import { forwardRef, useEffect, useImperativeHandle, useMemo } from "react";
import { Platform, StyleSheet, Text, View } from "react-native";

import { useLocale } from "../i18n/LocaleProvider";
import { colors, radius, spacing, type } from "../theme/tokens";
import { BrowserSessionSurface } from "./BrowserSessionSurface";
import {
  browserRuntimeSurface,
  type BrowserRuntimeViewportHandle,
  type BrowserRuntimeViewportProps,
} from "./BrowserRuntimeViewport.types";
import { createBrowserRuntimeState } from "./browserRuntimeSession";

/**
 * Web review and Android shell. The production Android surface belongs to the
 * Chromium host and cannot be represented by a React Native WebView.
 */
export const BrowserRuntimeViewport = forwardRef<
  BrowserRuntimeViewportHandle,
  BrowserRuntimeViewportProps
>(function BrowserRuntimeViewport(
  props,
  ref,
) {
  const { runtimeMode, candidateUrl, onRuntimeStateChange } = props;
  const { t } = useLocale();
  const surface = browserRuntimeSurface(runtimeMode, Platform.OS);
  const unavailableState = useMemo(
    () => createBrowserRuntimeState(candidateUrl, surface),
    [candidateUrl, surface],
  );
  const reviewState = useMemo(() => ({
    ...unavailableState,
    phase: runtimeMode === "server" ? props.session.state : "needs_user" as const,
    controlMode: runtimeMode === "server" && props.controlMode === "user"
      ? "user" as const
      : "paused" as const,
  }), [props, runtimeMode, unavailableState]);
  const directState = useMemo(() => ({
    ...unavailableState,
    phase: "needs_user" as const,
    controlMode: "user" as const,
  }), [unavailableState]);

  useEffect(() => {
    if (runtimeMode === "server") {
      onRuntimeStateChange?.(surface === "web-review" ? reviewState : unavailableState);
    } else if (runtimeMode === "direct") {
      onRuntimeStateChange?.(directState);
    }
  }, [directState, onRuntimeStateChange, reviewState, runtimeMode, surface, unavailableState]);

  useImperativeHandle(ref, () => ({
    approveCheckout: async () => undefined,
    requestResumeVerification: async () => undefined,
    stop: async () => undefined,
    takeOver: async () => undefined,
  }), []);

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

  const android = Platform.OS === "android";
  if (runtimeMode === "direct") {
    return (
      <View
        accessibilityLabel={t("browser.sessionSurface")}
        style={styles.unavailable}
        testID="browser.direct-runtime-boundary"
      >
        <View style={styles.badge}>
          <Text style={styles.badgeText}>{t("browser.directBadge")}</Text>
        </View>
        <Text accessibilityRole="header" style={styles.title}>
          {t(android ? "browser.directAndroidTitle" : "browser.directWebTitle")}
        </Text>
        <Text style={styles.body}>
          {t(android ? "browser.directAndroidBody" : "browser.directWebBody")}
        </Text>
        <View style={styles.destinationCard}>
          <Text style={styles.destinationHost}>{displayHost(candidateUrl)}</Text>
        </View>
        <View style={styles.boundary}>
          <Text style={styles.boundaryText}>{t("browser.directUserBoundary")}</Text>
        </View>
      </View>
    );
  }

  if (!android) {
    return (
      <View style={styles.reviewHost} testID="browser.web-review-simulator">
        <View style={styles.badge}>
          <Text style={styles.badgeText}>{t("browser.webReviewBadge")}</Text>
        </View>
        <Text style={styles.reviewDisclosure}>{t("browser.webReviewBody")}</Text>
        <BrowserSessionSurface
          controlMode={props.controlMode}
          onPageReady={props.onFixturePageReady}
          onPrepareCheckout={props.onFixturePrepareCheckout}
          onPreparePurchase={props.onFixturePreparePurchase}
          onSignInCompleted={props.onFixtureSignInCompleted}
          reviewMode
          session={props.session}
        />
      </View>
    );
  }
  return (
    <View accessibilityLabel={t("browser.sessionSurface")} style={styles.unavailable} testID="browser.runtime-unavailable">
      <View style={styles.badge}>
        <Text style={styles.badgeText}>{t(android ? "browser.androidHostBadge" : "browser.webReviewBadge")}</Text>
      </View>
      <Text accessibilityRole="header" style={styles.title}>
        {t(android ? "browser.androidHostTitle" : "browser.webReviewTitle")}
      </Text>
      <Text style={styles.body}>
        {t(android ? "browser.androidHostBody" : "browser.webReviewBody")}
      </Text>
      <View style={styles.boundary}>
        <Text style={styles.boundaryText}>{t("browser.deviceChannelUnavailable")}</Text>
      </View>
    </View>
  );
});

function displayHost(value: string): string {
  try {
    return new URL(value).hostname || value;
  } catch {
    return value;
  }
}

const styles = StyleSheet.create({
  unavailable: {
    alignSelf: "stretch",
    backgroundColor: colors.surface,
    borderColor: colors.border,
    borderRadius: radius.overlay,
    borderWidth: StyleSheet.hairlineWidth,
    gap: spacing[4],
    maxWidth: 720,
    minHeight: 420,
    padding: spacing[6],
    width: "100%",
  },
  reviewHost: { alignSelf: "stretch", gap: spacing[3], maxWidth: 720, width: "100%" },
  reviewDisclosure: { color: colors.textMuted, fontSize: type.helper, lineHeight: type.helperLine },
  badge: {
    alignSelf: "flex-start",
    backgroundColor: colors.warningSoft,
    borderRadius: radius.pill,
    paddingHorizontal: spacing[3],
    paddingVertical: spacing[1],
  },
  badgeText: { color: colors.text, fontSize: type.helper, fontWeight: "600", lineHeight: type.helperLine },
  title: { color: colors.text, fontSize: type.heading, fontWeight: "700", lineHeight: type.headingLine },
  body: { color: colors.textMuted, fontSize: type.body, lineHeight: type.bodyLine },
  boundary: { backgroundColor: colors.surfaceSubtle, borderRadius: radius.overlay, padding: spacing[4] },
  boundaryText: { color: colors.textMuted, fontSize: type.helper, lineHeight: type.helperLine },
  destinationCard: { backgroundColor: colors.surfaceSelected, borderRadius: radius.overlay, padding: spacing[4] },
  destinationHost: { color: colors.textAccent, fontSize: type.body, fontWeight: "600", lineHeight: type.bodyLine },
});
