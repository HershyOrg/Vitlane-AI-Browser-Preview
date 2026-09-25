import { StyleSheet, Text, View } from "react-native";

import { useLocale } from "../i18n/LocaleProvider";
import { colors, radius, spacing, type } from "../theme/tokens";
import { ActionButton } from "./ActionButton";

export type BackgroundResearchMonitorStatus =
  | "active"
  | "expired"
  | "cancelled"
  | "target-removed"
  | "archived";

export type BackgroundResearchMonitorPresentation = {
  readonly id: string;
  readonly title: string;
  readonly criteria: string;
  readonly status: BackgroundResearchMonitorStatus;
  readonly expiresLabel?: string;
};

export type BackgroundResearchFindingStatus = "new" | "hidden" | "added";

export type BackgroundResearchFindingPresentation = {
  readonly id: string;
  readonly monitorId: string;
  readonly title: string;
  readonly source?: string;
  readonly priceLabel?: string;
  readonly reason: string;
  readonly observedLabel?: string;
  readonly status: BackgroundResearchFindingStatus;
  /** True only when the caller has validated a navigable seller URL. */
  readonly openable?: boolean;
};

export type BackgroundResearchAction =
  | { readonly kind: "cancel-monitor"; readonly id: string }
  | { readonly kind: "add-finding"; readonly id: string }
  | { readonly kind: "hide-finding"; readonly id: string };

export type BackgroundResearchPresentation = {
  readonly monitors: readonly BackgroundResearchMonitorPresentation[];
  readonly findings: readonly BackgroundResearchFindingPresentation[];
  readonly unavailableMessage?: string;
};

export type BackgroundResearchPanelProps = BackgroundResearchPresentation & {
  onCancelMonitor?: (monitorId: string) => void;
  onAddFinding?: (findingId: string) => void;
  onHideFinding?: (findingId: string) => void;
  onOpenFinding?: (findingId: string) => void;
  busyAction?: BackgroundResearchAction;
};

export function BackgroundResearchPanel({
  monitors,
  findings,
  onCancelMonitor,
  onAddFinding,
  onHideFinding,
  onOpenFinding,
  busyAction,
  unavailableMessage,
}: BackgroundResearchPanelProps) {
  const { t } = useLocale();
  const activeMonitors = monitors.filter((monitor) => monitor.status === "active");
  const visibleFindings = findings.filter((finding) => finding.status !== "hidden");
  const hasUnavailableMessage = Boolean(unavailableMessage?.trim());

  if (!activeMonitors.length && !visibleFindings.length) return null;

  return (
    <View style={styles.panel}>
      <View style={styles.headingRow}>
        <View style={styles.headingCopy}>
          <Text accessibilityRole="header" style={styles.title}>{t("research.title")}</Text>
          <Text style={styles.description}>{t("research.description")}</Text>
        </View>
        {activeMonitors.length ? (
          <View style={styles.liveChip}>
            <View style={styles.liveDot} />
            <Text style={styles.liveLabel}>{t("research.active")}</Text>
          </View>
        ) : null}
      </View>

      {hasUnavailableMessage ? (
        <Text accessibilityRole="alert" style={styles.unavailable}>{unavailableMessage}</Text>
      ) : null}

      {activeMonitors.length ? (
        <View style={styles.section}>
          <Text style={styles.sectionTitle}>{t("research.monitors")}</Text>
          {activeMonitors.map((monitor) => {
            const cancelling = busyAction?.kind === "cancel-monitor" && busyAction.id === monitor.id;
            return (
              <View key={monitor.id} style={styles.monitor} testID={`research-monitor.${monitor.id}`}>
                <View style={styles.monitorHeader}>
                  <Text style={styles.monitorTitle}>{monitor.title}</Text>
                  <Text style={styles.monitorStatus}>{t(monitorStatusKeys[monitor.status])}</Text>
                </View>
                <Text style={styles.criteria}>{monitor.criteria}</Text>
                {monitor.expiresLabel ? (
                  <Text style={styles.meta}>{t("research.until", { value: monitor.expiresLabel })}</Text>
                ) : null}
                {monitor.status === "active" && onCancelMonitor ? (
                  <ActionButton
                    busy={cancelling}
                    disabled={Boolean(busyAction)}
                    compact
                    emphasis="quiet"
                    label={t("research.stopMonitor")}
                    onPress={() => onCancelMonitor(monitor.id)}
                    style={styles.rowAction}
                  />
                ) : null}
              </View>
            );
          })}
        </View>
      ) : null}

      {visibleFindings.length || activeMonitors.length ? (
        <View style={styles.section}>
          <Text style={styles.sectionTitle}>{t("research.findings")}</Text>
          {visibleFindings.length ? visibleFindings.map((finding) => {
            const adding = busyAction?.kind === "add-finding" && busyAction.id === finding.id;
            const hiding = busyAction?.kind === "hide-finding" && busyAction.id === finding.id;
            return (
              <View key={finding.id} style={styles.finding} testID={`research-finding.${finding.id}`}>
                <View style={styles.findingHeader}>
                  <View style={styles.findingCopy}>
                    <Text style={styles.findingTitle}>{finding.title}</Text>
                    {finding.source ? <Text style={styles.source}>{finding.source}</Text> : null}
                  </View>
                  {finding.priceLabel ? <Text style={styles.price}>{finding.priceLabel}</Text> : null}
                </View>
                <Text style={styles.reason}>{finding.reason}</Text>
                <View style={styles.metaRow}>
                  <Text style={styles.meta}>{t(findingStatusKeys[finding.status])}</Text>
                  {finding.observedLabel ? <Text style={styles.meta}>{finding.observedLabel}</Text> : null}
                </View>
                {finding.openable && onOpenFinding ? (
                  <ActionButton
                    disabled={Boolean(busyAction)}
                    compact
                    emphasis="quiet"
                    label={t("externalLink.reviewAction")}
                    onPress={() => onOpenFinding(finding.id)}
                    style={styles.rowAction}
                  />
                ) : null}
                {finding.status === "new" && (onAddFinding || onHideFinding) ? (
                  <View style={styles.actions}>
                    {onHideFinding ? (
                      <ActionButton
                        busy={hiding}
                        disabled={Boolean(busyAction)}
                        compact
                        emphasis="quiet"
                        label={t("research.hideFinding")}
                        onPress={() => onHideFinding(finding.id)}
                        style={styles.action}
                      />
                    ) : null}
                    {onAddFinding ? (
                      <ActionButton
                        busy={adding}
                        disabled={Boolean(busyAction)}
                        compact
                        emphasis="secondary"
                        label={t("research.addFinding")}
                        onPress={() => onAddFinding(finding.id)}
                        style={styles.action}
                      />
                    ) : null}
                  </View>
                ) : null}
              </View>
            );
          }) : (
            <Text style={styles.empty}>{t("research.noFindings")}</Text>
          )}
        </View>
      ) : null}
    </View>
  );
}

const monitorStatusKeys = {
  active: "research.statusActive",
  expired: "research.statusExpired",
  cancelled: "research.statusCancelled",
  "target-removed": "research.statusTargetRemoved",
  archived: "research.statusArchived",
} as const;

const findingStatusKeys = {
  new: "research.findingNew",
  hidden: "research.findingHidden",
  added: "research.findingAdded",
} as const;

const styles = StyleSheet.create({
  panel: {
    backgroundColor: colors.surface,
    borderColor: colors.border,
    borderRadius: radius.product,
    borderWidth: StyleSheet.hairlineWidth,
    gap: spacing[5],
    padding: spacing[4],
  },
  headingRow: { alignItems: "flex-start", flexDirection: "row", gap: spacing[3] },
  headingCopy: { flex: 1, minWidth: 0 },
  title: { color: colors.text, fontSize: type.heading, fontWeight: "600", lineHeight: type.headingLine },
  description: { color: colors.textMuted, fontSize: type.helper, lineHeight: type.helperLine, marginTop: spacing[1] },
  liveChip: {
    alignItems: "center",
    backgroundColor: colors.positiveSoft,
    borderRadius: radius.pill,
    flexDirection: "row",
    gap: spacing[1],
    minHeight: 28,
    paddingHorizontal: spacing[2],
  },
  liveDot: { backgroundColor: colors.positive, borderRadius: radius.pill, height: 7, width: 7 },
  liveLabel: { color: colors.text, fontSize: 12, fontWeight: "500", lineHeight: 18 },
  unavailable: { backgroundColor: colors.dangerSoft, borderRadius: radius.control, color: colors.danger, fontSize: type.helper, lineHeight: type.helperLine, padding: spacing[3] },
  section: { gap: spacing[3] },
  sectionTitle: { color: colors.textMuted, fontSize: type.helper, fontWeight: "600", lineHeight: type.helperLine },
  monitor: { backgroundColor: colors.surfaceSelected, borderRadius: radius.overlay, gap: spacing[1], padding: spacing[3] },
  monitorHeader: { alignItems: "flex-start", flexDirection: "row", gap: spacing[2] },
  monitorTitle: { color: colors.text, flex: 1, fontSize: type.body, fontWeight: "600", lineHeight: type.bodyLine },
  monitorStatus: { color: colors.textAccent, fontSize: 12, fontWeight: "500", lineHeight: 18 },
  criteria: { color: colors.text, fontSize: type.helper, lineHeight: type.helperLine },
  meta: { color: colors.textMuted, fontSize: 12, lineHeight: 18 },
  rowAction: { alignSelf: "flex-start", marginHorizontal: -spacing[4], marginBottom: -spacing[2] },
  finding: { borderTopColor: colors.border, borderTopWidth: StyleSheet.hairlineWidth, gap: spacing[2], paddingTop: spacing[3] },
  findingHeader: { alignItems: "flex-start", flexDirection: "row", gap: spacing[3] },
  findingCopy: { flex: 1, minWidth: 0 },
  findingTitle: { color: colors.text, fontSize: type.body, fontWeight: "600", lineHeight: type.bodyLine },
  source: { color: colors.textMuted, fontSize: type.helper, lineHeight: type.helperLine },
  price: { color: colors.text, fontSize: type.body, fontWeight: "600", lineHeight: type.bodyLine },
  reason: { color: colors.textMuted, fontSize: type.helper, lineHeight: type.helperLine },
  metaRow: { alignItems: "center", flexDirection: "row", gap: spacing[2], justifyContent: "space-between" },
  actions: { flexDirection: "row", gap: spacing[2] },
  action: { flex: 1 },
  empty: { backgroundColor: colors.surfaceSubtle, borderRadius: radius.control, color: colors.textMuted, fontSize: type.helper, lineHeight: type.helperLine, padding: spacing[3] },
});
