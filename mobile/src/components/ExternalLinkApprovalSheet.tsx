import { Modal, Pressable, ScrollView, StyleSheet, Text, View } from "react-native";
import { useSafeAreaInsets } from "react-native-safe-area-context";

import { useLocale } from "../i18n/LocaleProvider";
import { colors, radius, size, spacing, type } from "../theme/tokens";
import { ActionButton } from "./ActionButton";
import { AgentAvatar } from "./AgentAvatar";
import type { BrowserDestinationKind } from "./browserDestinations";

export type ExternalLinkApprovalPresentation = {
  readonly productTitle: string;
  readonly sellerName: string;
  readonly destinationLabel: string;
  readonly priceLabel?: string;
  /** Direct destinations open in user-only mode after this approval. */
  readonly destinationKind?: BrowserDestinationKind;
  /** Compatibility name used by workspace navigation presentations. */
  readonly purpose?: "search" | "reservation";
};

export type ExternalLinkApprovalSheetProps = {
  visible: boolean;
  target: ExternalLinkApprovalPresentation | null;
  onAllow: () => void;
  onDeny: () => void;
  working?: boolean;
  error?: string;
  allowLabel?: string;
};

/**
 * Deterministic approval for leaving Vitlane. Opening the URL remains the
 * caller's responsibility and must happen only after `onAllow`.
 */
export function ExternalLinkApprovalSheet({
  visible,
  target,
  onAllow,
  onDeny,
  working = false,
  error,
  allowLabel,
}: ExternalLinkApprovalSheetProps) {
  const { t } = useLocale();
  const insets = useSafeAreaInsets();
  const directKind = target?.destinationKind
    ?? (target?.purpose === "reservation" ? "reservation" : target?.purpose === "search" ? "shopping" : undefined);
  const direct = directKind !== undefined;
  const titleKey = directKind === "reservation"
    ? "externalLink.directReservationTitle"
    : direct
      ? "externalLink.directShoppingTitle"
      : "externalLink.title";
  return (
    <Modal
      animationType="slide"
      onRequestClose={onDeny}
      statusBarTranslucent
      transparent
      visible={visible && target !== null}
    >
      <View style={styles.modalRoot}>
        <Pressable accessible={false} onPress={onDeny} style={styles.scrim} />
        {target ? (
          <View
            accessibilityViewIsModal
            importantForAccessibility="yes"
            style={[styles.sheet, { paddingBottom: Math.max(insets.bottom, spacing[4]) }]}
          >
            <View style={styles.handle} />
            <ScrollView
              contentContainerStyle={styles.content}
              keyboardShouldPersistTaps="handled"
              style={styles.scroll}
            >
              <View style={styles.agentRow}>
                <AgentAvatar size={40} />
                <Text style={styles.eyebrow}>{t("externalLink.review")}</Text>
              </View>
              <Text accessibilityRole="header" style={styles.title}>{t(titleKey)}</Text>
              <Text style={styles.description}>
                {t(direct ? "externalLink.directDescription" : "externalLink.description")}
              </Text>

              <View style={styles.destinationCard}>
                <View style={styles.destinationHeader}>
                  <Text style={styles.productTitle}>{target.productTitle}</Text>
                  {target.priceLabel ? <Text style={styles.price}>{target.priceLabel}</Text> : null}
                </View>
                <View style={styles.sellerRow}>
                  <Text style={styles.seller}>{target.sellerName}</Text>
                  {directKind ? (
                    <View style={styles.kindChip}>
                      <Text style={styles.kindText}>
                        {t(directKind === "reservation"
                          ? "browserDestination.kindReservation"
                          : "browserDestination.kindShopping")}
                      </Text>
                    </View>
                  ) : null}
                </View>
                <Text accessibilityLabel={t("externalLink.destination", { value: target.destinationLabel })} style={styles.destination}>
                  {target.destinationLabel}
                </Text>
              </View>

              <View style={styles.boundary}>
                <View style={styles.boundaryDot} />
                <Text style={styles.boundaryText}>
                  {t(direct ? "externalLink.directBoundary" : "externalLink.boundary")}
                </Text>
              </View>

              {error ? <Text accessibilityRole="alert" style={styles.error}>{error}</Text> : null}

              <View style={styles.actions}>
                <ActionButton
                  disabled={working}
                  emphasis="secondary"
                  label={t("externalLink.deny")}
                  onPress={onDeny}
                  style={styles.action}
                />
                <ActionButton
                  busy={working}
                  emphasis="primary"
                  label={allowLabel ?? t(direct ? "externalLink.allowDirect" : "externalLink.allow")}
                  onPress={onAllow}
                  style={styles.action}
                />
              </View>
            </ScrollView>
          </View>
        ) : null}
      </View>
    </Modal>
  );
}

const styles = StyleSheet.create({
  modalRoot: { flex: 1, justifyContent: "flex-end" },
  scrim: { ...StyleSheet.absoluteFill, backgroundColor: colors.scrim },
  sheet: {
    alignSelf: "center",
    backgroundColor: colors.surface,
    borderTopLeftRadius: radius.sheet,
    borderTopRightRadius: radius.sheet,
    maxHeight: "90%",
    maxWidth: size.contentMax + spacing[8] * 2,
    padding: spacing[4],
    width: "100%",
  },
  handle: { alignSelf: "center", backgroundColor: colors.borderStrong, borderRadius: radius.pill, height: 4, width: 40 },
  scroll: { flexShrink: 1 },
  content: { gap: spacing[4], paddingTop: spacing[4] },
  agentRow: { alignItems: "center", flexDirection: "row", gap: spacing[3] },
  eyebrow: { color: colors.textAccent, flex: 1, fontSize: type.helper, fontWeight: "600", lineHeight: type.helperLine },
  title: { color: colors.text, fontSize: type.heading, fontWeight: "600", lineHeight: type.headingLine },
  description: { color: colors.textMuted, fontSize: type.body, lineHeight: type.bodyLine },
  destinationCard: { backgroundColor: colors.surfaceSubtle, borderRadius: radius.overlay, gap: spacing[1], padding: spacing[3] },
  destinationHeader: { alignItems: "flex-start", flexDirection: "row", gap: spacing[3] },
  productTitle: { color: colors.text, flex: 1, fontSize: type.body, fontWeight: "600", lineHeight: type.bodyLine },
  price: { color: colors.text, fontSize: type.body, fontWeight: "600", lineHeight: type.bodyLine },
  seller: { color: colors.textMuted, fontSize: type.helper, lineHeight: type.helperLine },
  sellerRow: { alignItems: "center", flexDirection: "row", flexWrap: "wrap", gap: spacing[2] },
  kindChip: { backgroundColor: colors.surfaceSelected, borderRadius: radius.pill, paddingHorizontal: spacing[2], paddingVertical: 2 },
  kindText: { color: colors.textAccent, fontSize: 11, fontWeight: "600", lineHeight: 16 },
  destination: { color: colors.textAccent, fontSize: type.helper, lineHeight: type.helperLine },
  boundary: { alignItems: "flex-start", backgroundColor: colors.warningSoft, borderRadius: radius.overlay, flexDirection: "row", gap: spacing[2], padding: spacing[3] },
  boundaryDot: { backgroundColor: colors.warning, borderRadius: radius.pill, height: 8, marginTop: 6, width: 8 },
  boundaryText: { color: colors.text, flex: 1, fontSize: type.helper, lineHeight: type.helperLine },
  error: { color: colors.danger, fontSize: type.helper, lineHeight: type.helperLine },
  actions: { flexDirection: "row", gap: spacing[2] },
  action: { flex: 1 },
});
