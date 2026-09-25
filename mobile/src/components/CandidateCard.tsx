import { useEffect, useState } from "react";
import { Image, StyleSheet, Text, View } from "react-native";

import { formatMoney, type CandidatePrice, type Locale } from "../domain";
import { useLocale } from "../i18n/LocaleProvider";
import { colors, radius, spacing, type } from "../theme/tokens";
import { ActionButton } from "./ActionButton";

export type CandidateCardView = {
  id: string;
  kind: "product" | "service";
  title: string;
  source: string;
  option: string;
  price: CandidatePrice;
  quantity?: number;
  reason: string;
  timing?: string;
  selected: boolean;
  removed?: boolean;
  visualLabel: string;
  visualTone: "water" | "moss" | "maple" | "iris";
  imageUrl?: string;
  imageAlt?: string;
  provenance?: string;
  observedAt?: string;
  availability?: "AVAILABLE" | "UNAVAILABLE" | "UNKNOWN";
};

type CandidateCardProps = {
  candidate: CandidateCardView;
  onSelect?: () => void;
  onOpenBrowser?: () => void;
  loading?: boolean;
};

const toneStyles = StyleSheet.create({
  water: { backgroundColor: colors.illustrationWater },
  moss: { backgroundColor: colors.illustrationMoss },
  maple: { backgroundColor: colors.illustrationMaple },
  iris: { backgroundColor: colors.illustrationIris },
});

export function formatCandidatePrice(
  price: CandidatePrice,
  locale: Locale,
  unknownLabel: string,
): string {
  if (price.kind === "UNKNOWN") return unknownLabel;
  if (price.kind === "RANGE") {
    return `${formatMoney(price.minimum, locale)} – ${formatMoney(price.maximum, locale)}`;
  }
  return formatMoney(price.amount, locale);
}

export function CandidateCard({
  candidate,
  onSelect,
  onOpenBrowser,
  loading = false,
}: CandidateCardProps) {
  const { locale, t } = useLocale();
  const [imageFailed, setImageFailed] = useState(false);
  useEffect(() => setImageFailed(false), [candidate.imageUrl]);
  const priceLabel = formatCandidatePrice(candidate.price, locale, t("candidate.priceUnknown"));
  const unavailable = candidate.availability === "UNAVAILABLE";
  return (
    <View
      accessibilityLabel={`${candidate.title}, ${priceLabel}`}
      accessibilityState={{ busy: loading, disabled: candidate.removed || unavailable, selected: candidate.selected }}
      style={[styles.card, candidate.selected && styles.selected, (candidate.removed || unavailable) && styles.removed]}
      testID={`candidate.${candidate.id}`}
    >
      <View style={[styles.visual, toneStyles[candidate.visualTone]]}>
        {candidate.imageUrl && !imageFailed ? (
          <Image
            accessibilityLabel={candidate.imageAlt || candidate.title}
            onError={() => setImageFailed(true)}
            resizeMode="contain"
            source={{ uri: candidate.imageUrl }}
            style={styles.image}
            testID={`candidate.${candidate.id}.image`}
          />
        ) : (
          <>
            <View style={styles.visualHalo} />
            <View style={styles.visualOrbSmall} />
            <View style={styles.visualMonogram}>
              <Text style={styles.visualLabel}>{candidate.visualLabel}</Text>
            </View>
          </>
        )}
        <Text style={styles.kind}>{candidate.kind === "service" ? t("candidate.pickup") : t("candidate.product")}</Text>
      </View>
      <View style={styles.body}>
        <View style={styles.metaRow}>
          <Text style={styles.source}>{candidate.source}</Text>
          {candidate.selected ? <Text style={styles.selectedText}>✓ {t("candidate.selected")}</Text> : null}
          {candidate.removed ? <Text style={styles.removedText}>{t("candidate.removed")}</Text> : null}
          {unavailable ? <Text style={styles.removedText}>{t("candidate.unavailable")}</Text> : null}
        </View>
        {candidate.provenance ? <Text style={styles.provenance}>{candidate.provenance}</Text> : null}
        <Text accessibilityRole="header" style={styles.title}>{candidate.title}</Text>
        {candidate.option || candidate.quantity !== undefined ? (
          <Text style={styles.option}>
            {[
              candidate.option,
              candidate.quantity !== undefined
                ? t("candidate.quantity", { quantity: candidate.quantity })
                : "",
            ].filter(Boolean).join(" · ")}
          </Text>
        ) : null}
        <View style={styles.detailRow}>
          {candidate.timing ? <Text style={styles.timing}>{candidate.timing}</Text> : <View style={styles.detailSpacer} />}
          <Text style={[styles.price, candidate.price.kind === "UNKNOWN" && styles.priceUnknown]}>{priceLabel}</Text>
        </View>
        {candidate.observedAt ? (
          <Text style={styles.observedAt}>{t("candidate.observedAt", { value: candidate.observedAt })}</Text>
        ) : null}
        <View style={styles.reasonBlock}>
          <View style={styles.reasonDot} />
          <View style={styles.reasonCopy}>
            <Text style={styles.reasonLabel}>{t("candidate.recommendation")}</Text>
            <Text style={styles.reason}>{candidate.reason}</Text>
          </View>
        </View>
        {!candidate.removed && !unavailable && (onSelect || onOpenBrowser) ? (
          <View style={styles.actions}>
            {onSelect ? (
              <ActionButton
                disabled={candidate.selected}
                emphasis={candidate.selected ? "secondary" : "quiet"}
                label={candidate.selected ? t("candidate.selected") : t("candidate.select")}
                onPress={onSelect}
                style={styles.action}
              />
            ) : null}
            {onOpenBrowser ? (
              <ActionButton
                emphasis="secondary"
                label={t("candidate.openBrowser")}
                onPress={onOpenBrowser}
                style={styles.browserAction}
                testID={`candidate.${candidate.id}.open-browser`}
              />
            ) : null}
          </View>
        ) : null}
      </View>
    </View>
  );
}

const styles = StyleSheet.create({
  card: {
    backgroundColor: colors.surface,
    borderColor: colors.border,
    borderRadius: radius.sheet,
    borderWidth: StyleSheet.hairlineWidth,
    overflow: "hidden",
  },
  selected: { backgroundColor: colors.surfaceSelected },
  removed: { opacity: 0.58 },
  visual: {
    alignItems: "center",
    height: 156,
    justifyContent: "center",
    position: "relative",
  },
  image: { backgroundColor: colors.surface, height: "100%", width: "100%" },
  visualHalo: {
    backgroundColor: "rgba(255,255,255,0.42)",
    borderRadius: radius.pill,
    height: 116,
    position: "absolute",
    transform: [{ rotate: "-12deg" }],
    width: 116,
  },
  visualOrbSmall: {
    backgroundColor: "rgba(255,255,255,0.72)",
    borderRadius: radius.pill,
    height: 30,
    position: "absolute",
    right: "25%",
    top: 25,
    width: 30,
  },
  visualMonogram: {
    alignItems: "center",
    backgroundColor: "rgba(255,255,255,0.86)",
    borderRadius: radius.pill,
    height: 78,
    justifyContent: "center",
    width: 78,
  },
  visualLabel: { color: colors.textAccent, fontSize: 27, fontWeight: "600" },
  kind: {
    backgroundColor: "rgba(255,255,255,0.82)",
    borderRadius: radius.pill,
    bottom: spacing[2],
    color: colors.text,
    fontSize: 11,
    left: spacing[2],
    overflow: "hidden",
    paddingHorizontal: spacing[2],
    paddingVertical: spacing[1],
    position: "absolute",
  },
  body: { gap: spacing[2], padding: spacing[4] },
  metaRow: { alignItems: "center", flexDirection: "row", flexWrap: "wrap", gap: spacing[2] },
  source: { color: colors.textMuted, flex: 1, fontSize: type.helper, lineHeight: type.helperLine },
  provenance: { color: colors.textMuted, fontSize: 11, lineHeight: 16 },
  selectedText: { color: colors.textAccent, fontSize: 12, fontWeight: "500" },
  removedText: { color: colors.danger, fontSize: 12, fontWeight: "500" },
  title: { color: colors.text, fontSize: 18, fontWeight: "600", lineHeight: 25 },
  option: { color: colors.textMuted, fontSize: type.helper, lineHeight: type.helperLine },
  detailRow: { alignItems: "flex-end", flexDirection: "row", gap: spacing[3], justifyContent: "space-between" },
  timing: { color: colors.textMuted, flex: 1, fontSize: type.helper, lineHeight: type.helperLine },
  detailSpacer: { flex: 1 },
  price: { color: colors.text, fontSize: type.price, fontWeight: "600", lineHeight: type.priceLine },
  priceUnknown: { color: colors.textMuted, fontSize: type.body, lineHeight: type.bodyLine },
  observedAt: { color: colors.textMuted, fontSize: 11, lineHeight: 16, textAlign: "right" },
  reasonBlock: { alignItems: "flex-start", backgroundColor: colors.surfaceSubtle, borderRadius: radius.overlay, flexDirection: "row", gap: spacing[2], padding: spacing[3] },
  reasonDot: { backgroundColor: colors.action, borderRadius: radius.pill, height: 7, marginTop: 5, width: 7 },
  reasonCopy: { flex: 1 },
  reasonLabel: { color: colors.textMuted, fontSize: 12, fontWeight: "500", lineHeight: 18 },
  reason: { color: colors.text, fontSize: type.helper, lineHeight: type.helperLine, marginTop: spacing[1] },
  actions: { alignItems: "stretch", flexDirection: "row", flexWrap: "wrap", gap: spacing[2] },
  action: { flexGrow: 1 },
  browserAction: { flexBasis: "100%" },
});
