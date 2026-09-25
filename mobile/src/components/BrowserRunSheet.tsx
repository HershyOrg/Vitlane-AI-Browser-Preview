import type { ReactNode } from "react";
import { Modal, ScrollView, StyleSheet, Text, View } from "react-native";
import { useSafeAreaInsets } from "react-native-safe-area-context";

import { useLocale } from "../i18n/LocaleProvider";
import { colors, radius, size, spacing, type } from "../theme/tokens";
import { ActionButton } from "./ActionButton";
import { BrowserRunPanel, type BrowserRunPanelProps } from "./BrowserRunPanel";

export type BrowserRunSheetProps = BrowserRunPanelProps & {
  readonly visible: boolean;
  readonly onClose: () => void;
  readonly page?: ReactNode;
  readonly reviewMode?: boolean;
  /** Native browser views need a stable flex host and must not sit in a ScrollView. */
  readonly nativeViewport?: boolean;
};

/**
 * Reviewable common UI for the Browser screen. Production Android must place
 * the actual WebContents and trusted controls in the Chromium native host;
 * iOS uses the WKWebView adapter. `reviewMode` prevents this RN preview from
 * being mistaken for a trusted browser surface.
 */
export function BrowserRunSheet({
  visible,
  onClose,
  page,
  reviewMode = false,
  nativeViewport = false,
  ...panelProps
}: BrowserRunSheetProps) {
  const { t } = useLocale();
  const insets = useSafeAreaInsets();
  const verified = panelProps.run.nativeOriginVerified && !reviewMode;

  return (
    <Modal animationType="slide" onRequestClose={onClose} visible={visible}>
      <View style={[styles.screen, { paddingTop: insets.top }]} testID="run.screen">
        <View style={styles.header} testID="browser.origin-bar">
          <View style={styles.originCopy}>
            <View style={styles.originLine}>
              <View style={[styles.securityDot, verified ? styles.verifiedDot : styles.previewDot]} />
              <Text style={styles.origin} numberOfLines={1} testID="browser.origin-host">
                {panelProps.run.origin}
              </Text>
            </View>
            <Text style={styles.security} testID="browser.security-state">
              {verified ? t("browser.nativeVerified") : t("browser.reviewSurface")}
            </Text>
          </View>
          <ActionButton compact emphasis="quiet" label={t("common.close")} onPress={onClose} />
        </View>

        {nativeViewport ? (
          <View style={styles.nativePage} testID="browser.page-surface">
            {page}
          </View>
        ) : (
          <ScrollView
            contentContainerStyle={styles.scrollContent}
            style={styles.scroll}
            testID="browser.page-surface"
          >
            {page ?? <FixtureMerchantPage />}
          </ScrollView>
        )}

        <View style={[styles.panelHost, { paddingBottom: Math.max(insets.bottom, spacing[3]) }]}>
          <BrowserRunPanel {...panelProps} />
        </View>
      </View>
    </Modal>
  );
}

function FixtureMerchantPage() {
  const { t } = useLocale();
  return (
    <View accessibilityLabel={t("browser.fixturePageLabel")} style={styles.fixturePage}>
      <View style={styles.fixtureHero}>
        <View style={styles.fixtureImage} />
        <View style={styles.fixtureCopy}>
          <View style={[styles.fixtureLine, styles.fixtureLineShort]} />
          <View style={styles.fixtureLine} />
          <View style={[styles.fixtureLine, styles.fixtureLineMedium]} />
        </View>
      </View>
      <Text style={styles.fixtureTitle}>{t("browser.fixtureProduct")}</Text>
      <Text style={styles.fixtureBody}>{t("browser.fixtureDescription")}</Text>
      <View style={styles.fixtureOption}>
        <Text style={styles.fixtureOptionLabel}>{t("browser.fixtureOption")}</Text>
        <Text style={styles.fixtureOptionValue}>{t("browser.fixtureOptionValue")}</Text>
      </View>
      <View style={styles.fixtureBoundary}>
        <Text style={styles.fixtureBoundaryText}>{t("browser.fixtureBoundary")}</Text>
      </View>
    </View>
  );
}

const styles = StyleSheet.create({
  screen: { backgroundColor: colors.canvas, flex: 1 },
  header: {
    alignItems: "center",
    backgroundColor: colors.surface,
    borderBottomColor: colors.border,
    borderBottomWidth: StyleSheet.hairlineWidth,
    flexDirection: "row",
    gap: spacing[3],
    minHeight: size.header,
    paddingHorizontal: spacing[4],
  },
  originCopy: { flex: 1, minWidth: 0 },
  originLine: { alignItems: "center", flexDirection: "row", gap: spacing[2] },
  securityDot: { borderRadius: radius.pill, height: 8, width: 8 },
  verifiedDot: { backgroundColor: colors.positive },
  previewDot: { backgroundColor: colors.warning },
  origin: { color: colors.text, flex: 1, fontSize: type.body, fontWeight: "600", lineHeight: type.bodyLine },
  security: { color: colors.textMuted, fontSize: 12, lineHeight: 18 },
  scroll: { flex: 1 },
  nativePage: { flex: 1, minHeight: 0 },
  scrollContent: { alignItems: "center", padding: spacing[4], paddingBottom: spacing[8] },
  panelHost: {
    alignSelf: "center",
    backgroundColor: colors.surface,
    borderTopColor: colors.border,
    borderTopWidth: StyleSheet.hairlineWidth,
    maxWidth: size.contentMax + spacing[8] * 2,
    padding: spacing[3],
    width: "100%",
  },
  fixturePage: {
    backgroundColor: colors.surface,
    borderRadius: radius.overlay,
    gap: spacing[4],
    maxWidth: size.contentMax,
    minHeight: 420,
    padding: spacing[4],
    width: "100%",
  },
  fixtureHero: { flexDirection: "row", gap: spacing[4] },
  fixtureImage: { backgroundColor: colors.illustrationIris, borderRadius: radius.product, height: 116, width: 116 },
  fixtureCopy: { flex: 1, gap: spacing[2], justifyContent: "center" },
  fixtureLine: { backgroundColor: colors.surfaceSubtle, borderRadius: radius.pill, height: 12, width: "100%" },
  fixtureLineShort: { backgroundColor: colors.surfaceSelected, width: "42%" },
  fixtureLineMedium: { width: "74%" },
  fixtureTitle: { color: colors.text, fontSize: type.heading, fontWeight: "600", lineHeight: type.headingLine },
  fixtureBody: { color: colors.textMuted, fontSize: type.body, lineHeight: type.bodyLine },
  fixtureOption: {
    alignItems: "center",
    backgroundColor: colors.surfaceSubtle,
    borderRadius: radius.product,
    flexDirection: "row",
    justifyContent: "space-between",
    padding: spacing[3],
  },
  fixtureOptionLabel: { color: colors.textMuted, fontSize: type.helper, lineHeight: type.helperLine },
  fixtureOptionValue: { color: colors.text, fontSize: type.body, fontWeight: "500", lineHeight: type.bodyLine },
  fixtureBoundary: { backgroundColor: colors.warningSoft, borderRadius: radius.overlay, padding: spacing[3] },
  fixtureBoundaryText: { color: colors.text, fontSize: type.helper, lineHeight: type.helperLine },
});
