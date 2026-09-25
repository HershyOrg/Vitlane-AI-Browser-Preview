import { useEffect, useState } from "react";
import { ActivityIndicator, Image, StyleSheet, Text, View } from "react-native";

import { useLocale } from "../i18n/LocaleProvider";
import { colors, radius, spacing, type } from "../theme/tokens";
import { ActionButton } from "./ActionButton";
import type { BrowserControlMode } from "./BrowserRunPanel";

export type BrowserSessionState =
  | "loading"
  | "product"
  | "sign_in_required"
  | "signed_in"
  | "checkout_ready"
  | "payment_handoff";

export type BrowserSessionPresentation = {
  readonly state: BrowserSessionState;
  readonly productTitle: string;
  readonly merchantName: string;
  readonly priceLabel?: string;
  readonly imageUrl?: string;
};

export type BrowserSessionSurfaceProps = {
  readonly session: BrowserSessionPresentation;
  readonly controlMode: BrowserControlMode;
  /** Labels every transition as a simulator and omits credential/payment controls. */
  readonly reviewMode?: boolean;
  readonly onPageReady?: () => void;
  readonly onPreparePurchase?: () => void;
  /** Must be wired to a fresh native observation before agent control resumes. */
  readonly onSignInCompleted?: () => void;
  readonly onPrepareCheckout?: () => void;
};

/**
 * Web-review placeholder for the page area owned by the native browser host.
 * It intentionally does not embed a cross-origin merchant iframe. Native
 * Android/iOS replace this area with their persistent browser view while the
 * session and handoff states remain shared RN UI.
 */
export function BrowserSessionSurface({
  session,
  controlMode,
  reviewMode = false,
  onPageReady,
  onPreparePurchase,
  onSignInCompleted,
  onPrepareCheckout,
}: BrowserSessionSurfaceProps) {
  const { t } = useLocale();
  const [imageFailed, setImageFailed] = useState(false);

  useEffect(() => setImageFailed(false), [session.imageUrl]);
  useEffect(() => {
    if (session.state === "loading") onPageReady?.();
  }, [onPageReady, session.state]);

  return (
    <View
      accessibilityLabel={t("browser.sessionSurface")}
      style={styles.page}
      testID={`browser.session.state.${session.state}`}
    >
      <View style={styles.merchantHeader}>
        <View style={styles.merchantMark}>
          <Text style={styles.merchantMarkText}>{session.merchantName.slice(0, 1).toUpperCase()}</Text>
        </View>
        <View style={styles.merchantCopy}>
          <Text numberOfLines={1} style={styles.merchantName}>{session.merchantName}</Text>
          <Text style={styles.reviewHint}>{t("browser.nativePagePlaceholder")}</Text>
        </View>
      </View>

      {session.state === "loading" ? (
        <View accessibilityLiveRegion="polite" style={styles.centerState}>
          <ActivityIndicator color={colors.action} size="large" />
          <Text accessibilityRole="header" style={styles.stateTitle}>{t("browser.loadingProduct")}</Text>
          <Text style={styles.stateBody}>{t("browser.loadingProductBody")}</Text>
        </View>
      ) : null}

      {session.state === "product" ? (
        <View style={styles.content}>
          <ProductSummary session={session} imageFailed={imageFailed} onImageError={() => setImageFailed(true)} />
          <View style={styles.infoCard}>
            <Text style={styles.infoEyebrow}>{t("browser.sessionReadyEyebrow")}</Text>
            <Text style={styles.infoTitle}>{t("browser.sessionReadyTitle")}</Text>
            <Text style={styles.infoBody}>{t("browser.sessionReadyBody")}</Text>
          </View>
          {onPreparePurchase ? (
            <ActionButton
              emphasis="primary"
              label={t(reviewMode ? "browser.reviewAdvance" : "browser.preparePurchase")}
              onPress={onPreparePurchase}
              testID="browser.prepare-purchase"
            />
          ) : null}
        </View>
      ) : null}

      {session.state === "sign_in_required" ? (
        <View style={styles.content}>
          <View style={styles.pageNotice}>
            <View style={styles.warningDot} />
            <View style={styles.noticeCopy}>
              <Text accessibilityRole="header" style={styles.infoTitle}>{t("browser.signInPageTitle")}</Text>
              <Text style={styles.infoBody}>{t("browser.signInPageBody")}</Text>
            </View>
          </View>
          {reviewMode ? (
            <View style={styles.reviewBoundary} testID="browser.review-no-credentials">
              <Text style={styles.privateText}>{t("browser.reviewNoCredentials")}</Text>
            </View>
          ) : (
            <View accessibilityLabel={t("browser.merchantSignInForm")} style={styles.merchantForm}>
              <Text style={styles.formTitle}>{t("browser.merchantSignIn")}</Text>
              <View style={styles.formField}><Text style={styles.formPlaceholder}>{t("browser.merchantAccountField")}</Text></View>
              <View style={styles.formField}><Text style={styles.formPlaceholder}>{t("browser.merchantPasswordField")}</Text></View>
              <View style={styles.merchantButton}><Text style={styles.merchantButtonText}>{t("browser.merchantSignInButton")}</Text></View>
            </View>
          )}
          <View style={styles.privateCard}>
            <Text style={styles.privateText}>{t("browser.signInPrivacy")}</Text>
          </View>
          {controlMode === "user" && onSignInCompleted ? (
            <ActionButton
              emphasis="primary"
              label={t(reviewMode ? "browser.reviewSignInState" : "browser.signInCompleted")}
              onPress={onSignInCompleted}
              testID="browser.sign-in-completed"
            />
          ) : null}
        </View>
      ) : null}

      {session.state === "signed_in" ? (
        <View accessibilityLiveRegion="polite" style={styles.content}>
          <View style={styles.successCard}>
            <View style={styles.successMark}><Text style={styles.successMarkText}>✓</Text></View>
            <View style={styles.noticeCopy}>
              <Text accessibilityRole="header" style={styles.infoTitle}>{t(reviewMode ? "browser.reviewSignedInTitle" : "browser.signedInTitle")}</Text>
              <Text style={styles.infoBody}>{t(reviewMode ? "browser.reviewSignedInBody" : "browser.signedInBody")}</Text>
            </View>
          </View>
          <ProductSummary session={session} compact imageFailed={imageFailed} onImageError={() => setImageFailed(true)} />
          {onPrepareCheckout ? (
            <ActionButton
              emphasis="primary"
              label={t("browser.prepareCheckout")}
              onPress={onPrepareCheckout}
              testID="browser.prepare-checkout"
            />
          ) : null}
        </View>
      ) : null}

      {session.state === "checkout_ready" ? (
        <View accessibilityLiveRegion="polite" style={styles.content}>
          <ProductSummary session={session} compact imageFailed={imageFailed} onImageError={() => setImageFailed(true)} />
          <View style={styles.infoCard}>
            <Text style={styles.infoEyebrow}>{t("browser.checkoutReadyEyebrow")}</Text>
            <Text accessibilityRole="header" style={styles.infoTitle}>{t("browser.checkoutReadyTitle")}</Text>
            <Text style={styles.infoBody}>{t("browser.checkoutReadyBody")}</Text>
          </View>
        </View>
      ) : null}

      {session.state === "payment_handoff" ? (
        <View style={styles.content}>
          <ProductSummary session={session} compact imageFailed={imageFailed} onImageError={() => setImageFailed(true)} />
          <View style={styles.paymentCard}>
            <Text style={styles.infoEyebrow}>{t("browser.paymentHandoffEyebrow")}</Text>
            <Text accessibilityRole="header" style={styles.paymentTitle}>{t("browser.paymentHandoffTitle")}</Text>
            <Text style={styles.paymentBody}>{t("browser.paymentHandoffBody")}</Text>
            {reviewMode ? (
              <View style={styles.reviewBoundary} testID="browser.review-no-payment">
                <Text style={styles.privateText}>{t("browser.reviewNoPayment")}</Text>
              </View>
            ) : (
              <View style={styles.merchantPaymentButton}>
                <Text style={styles.merchantPaymentButtonText}>{t("browser.merchantPaymentButton")}</Text>
              </View>
            )}
            <Text style={styles.paymentFootnote}>{t("browser.paymentHandoffFootnote")}</Text>
          </View>
        </View>
      ) : null}
    </View>
  );
}

function ProductSummary({
  session,
  compact = false,
  imageFailed,
  onImageError,
}: {
  session: BrowserSessionPresentation;
  compact?: boolean;
  imageFailed: boolean;
  onImageError: () => void;
}) {
  const { t } = useLocale();
  return (
    <View style={[styles.product, compact && styles.productCompact]}>
      <View style={[styles.productImage, compact && styles.productImageCompact]}>
        {session.imageUrl && !imageFailed ? (
          <Image
            accessibilityLabel={session.productTitle}
            onError={onImageError}
            resizeMode="contain"
            source={{ uri: session.imageUrl }}
            style={styles.image}
          />
        ) : (
          <Text style={styles.productGlyph}>◇</Text>
        )}
      </View>
      <View style={styles.productCopy}>
        <Text style={styles.productMerchant}>{session.merchantName}</Text>
        <Text numberOfLines={compact ? 2 : 3} style={styles.productTitle}>{session.productTitle}</Text>
        <Text style={styles.productPrice}>{session.priceLabel ?? t("candidate.priceUnknown")}</Text>
      </View>
    </View>
  );
}

const styles = StyleSheet.create({
  page: {
    alignSelf: "stretch",
    backgroundColor: colors.surface,
    borderColor: colors.border,
    borderRadius: radius.overlay,
    borderWidth: StyleSheet.hairlineWidth,
    minHeight: 440,
    overflow: "hidden",
  },
  merchantHeader: {
    alignItems: "center",
    borderBottomColor: colors.border,
    borderBottomWidth: StyleSheet.hairlineWidth,
    flexDirection: "row",
    gap: spacing[3],
    padding: spacing[3],
  },
  merchantMark: {
    alignItems: "center",
    backgroundColor: colors.surfaceSelected,
    borderRadius: radius.pill,
    height: 38,
    justifyContent: "center",
    width: 38,
  },
  merchantMarkText: { color: colors.textAccent, fontSize: type.body, fontWeight: "700" },
  merchantCopy: { flex: 1, minWidth: 0 },
  merchantName: { color: colors.text, fontSize: type.body, fontWeight: "600", lineHeight: type.bodyLine },
  reviewHint: { color: colors.textMuted, fontSize: 11, lineHeight: 16 },
  centerState: { alignItems: "center", flex: 1, gap: spacing[3], justifyContent: "center", minHeight: 360, padding: spacing[6] },
  stateTitle: { color: colors.text, fontSize: type.heading, fontWeight: "600", lineHeight: type.headingLine, textAlign: "center" },
  stateBody: { color: colors.textMuted, fontSize: type.body, lineHeight: type.bodyLine, textAlign: "center" },
  content: { gap: spacing[4], padding: spacing[4] },
  product: { gap: spacing[4] },
  productCompact: { alignItems: "center", flexDirection: "row", gap: spacing[3] },
  productImage: { alignItems: "center", backgroundColor: colors.illustrationIris, borderRadius: radius.product, height: 190, justifyContent: "center", overflow: "hidden", width: "100%" },
  productImageCompact: { flexShrink: 0, height: 86, width: 86 },
  image: { backgroundColor: colors.surface, height: "100%", width: "100%" },
  productGlyph: { color: colors.textAccent, fontSize: 48, fontWeight: "300" },
  productCopy: { flex: 1, gap: spacing[1], minWidth: 0 },
  productMerchant: { color: colors.textMuted, fontSize: type.helper, lineHeight: type.helperLine },
  productTitle: { color: colors.text, fontSize: 18, fontWeight: "600", lineHeight: 25 },
  productPrice: { color: colors.text, fontSize: type.price, fontWeight: "700", lineHeight: type.priceLine },
  infoCard: { backgroundColor: colors.surfaceSelected, borderRadius: radius.overlay, gap: spacing[1], padding: spacing[4] },
  infoEyebrow: { color: colors.textAccent, fontSize: 12, fontWeight: "700", letterSpacing: 0.3, lineHeight: 18 },
  infoTitle: { color: colors.text, fontSize: type.heading, fontWeight: "600", lineHeight: type.headingLine },
  infoBody: { color: colors.textMuted, fontSize: type.body, lineHeight: type.bodyLine },
  pageNotice: { alignItems: "flex-start", backgroundColor: colors.warningSoft, borderRadius: radius.overlay, flexDirection: "row", gap: spacing[3], padding: spacing[4] },
  warningDot: { backgroundColor: colors.warning, borderRadius: radius.pill, height: 9, marginTop: 8, width: 9 },
  noticeCopy: { flex: 1, gap: spacing[1] },
  merchantForm: { borderColor: colors.border, borderRadius: radius.overlay, borderWidth: StyleSheet.hairlineWidth, gap: spacing[3], padding: spacing[4] },
  formTitle: { color: colors.text, fontSize: type.body, fontWeight: "600", lineHeight: type.bodyLine },
  formField: { backgroundColor: colors.surfaceSubtle, borderColor: colors.border, borderRadius: radius.control, borderWidth: StyleSheet.hairlineWidth, minHeight: 48, paddingHorizontal: spacing[3], justifyContent: "center" },
  formPlaceholder: { color: colors.textMuted, fontSize: type.body, lineHeight: type.bodyLine },
  merchantButton: { alignItems: "center", backgroundColor: colors.text, borderRadius: radius.control, minHeight: 48, justifyContent: "center", paddingHorizontal: spacing[4] },
  merchantButtonText: { color: colors.surface, fontSize: type.body, fontWeight: "600", lineHeight: type.bodyLine },
  privateCard: { backgroundColor: colors.surfaceSubtle, borderRadius: radius.overlay, padding: spacing[3] },
  reviewBoundary: { backgroundColor: colors.warningSoft, borderRadius: radius.overlay, padding: spacing[3] },
  privateText: { color: colors.textMuted, fontSize: type.helper, lineHeight: type.helperLine },
  successCard: { alignItems: "center", backgroundColor: colors.positiveSoft, borderRadius: radius.overlay, flexDirection: "row", gap: spacing[3], padding: spacing[4] },
  successMark: { alignItems: "center", backgroundColor: colors.positive, borderRadius: radius.pill, height: 34, justifyContent: "center", width: 34 },
  successMarkText: { color: colors.surface, fontSize: type.body, fontWeight: "700" },
  paymentCard: { backgroundColor: colors.warningSoft, borderColor: colors.warning, borderRadius: radius.overlay, borderWidth: StyleSheet.hairlineWidth, gap: spacing[3], padding: spacing[4] },
  paymentTitle: { color: colors.text, fontSize: type.heading, fontWeight: "700", lineHeight: type.headingLine },
  paymentBody: { color: colors.text, fontSize: type.body, lineHeight: type.bodyLine },
  merchantPaymentButton: { alignItems: "center", backgroundColor: colors.text, borderRadius: radius.control, minHeight: 52, justifyContent: "center", paddingHorizontal: spacing[4] },
  merchantPaymentButtonText: { color: colors.surface, fontSize: type.body, fontWeight: "700", lineHeight: type.bodyLine, textAlign: "center" },
  paymentFootnote: { color: colors.textMuted, fontSize: type.helper, lineHeight: type.helperLine, textAlign: "center" },
});
