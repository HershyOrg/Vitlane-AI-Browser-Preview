import { StyleSheet, Text, View } from "react-native";

import { useLocale } from "../i18n/LocaleProvider";
import { colors, radius, spacing, type } from "../theme/tokens";
import { ActionButton } from "./ActionButton";

export type ShoppingProposalStatus =
  | "pending"
  | "accepted"
  | "dismissed"
  | "acknowledged"
  | "superseded";

export type ShoppingProposalPresentation = {
  readonly id: string;
  readonly title: string;
  readonly body: string;
  readonly reason?: string;
  readonly status: ShoppingProposalStatus;
  readonly responseMode: "decision" | "acknowledge" | "none";
  readonly availableAtLabel?: string;
};

export type ShoppingProposalCardProps = {
  proposal: ShoppingProposalPresentation;
  onAccept?: (proposalId: string) => void;
  onDismiss?: (proposalId: string) => void;
  onAcknowledge?: (proposalId: string) => void;
  busyResponse?: "accept" | "dismiss" | "acknowledge";
  acceptDisabled?: boolean;
};

export function ShoppingProposalCard({
  proposal,
  onAccept,
  onDismiss,
  onAcknowledge,
  busyResponse,
  acceptDisabled = false,
}: ShoppingProposalCardProps) {
  const { t } = useLocale();
  const pending = proposal.status === "pending";

  return (
    <View style={styles.card} testID={`shopping-proposal.${proposal.id}`}>
      <View style={styles.header}>
        <Text style={styles.eyebrow}>{t("proposal.eyebrow")}</Text>
        <View style={[styles.statusChip, proposal.status === "accepted" && styles.acceptedChip]}>
          <Text style={styles.statusText}>{t(proposalStatusKeys[proposal.status])}</Text>
        </View>
      </View>
      <Text accessibilityRole="header" style={styles.title}>{proposal.title}</Text>
      <Text style={styles.body}>{proposal.body}</Text>
      {proposal.reason ? (
        <View style={styles.reason}>
          <Text style={styles.reasonLabel}>{t("proposal.reason")}</Text>
          <Text style={styles.reasonBody}>{proposal.reason}</Text>
        </View>
      ) : null}
      {pending && acceptDisabled && proposal.availableAtLabel ? (
        <Text accessibilityLiveRegion="polite" style={styles.availableAt}>
          {t("proposal.availableAt", { value: proposal.availableAtLabel })}
        </Text>
      ) : null}
      {pending && proposal.responseMode === "decision" && onAccept && onDismiss ? (
        <View style={styles.actions}>
          <ActionButton
            busy={busyResponse === "dismiss"}
            disabled={Boolean(busyResponse)}
            emphasis="secondary"
            label={t("proposal.dismiss")}
            onPress={() => onDismiss(proposal.id)}
            style={styles.action}
          />
          <ActionButton
            busy={busyResponse === "accept"}
            disabled={Boolean(busyResponse) || acceptDisabled}
            emphasis="primary"
            label={t("proposal.accept")}
            onPress={() => onAccept(proposal.id)}
            style={styles.action}
          />
        </View>
      ) : null}
      {pending && proposal.responseMode === "acknowledge" && onAcknowledge ? (
        <ActionButton
          busy={busyResponse === "acknowledge"}
          disabled={Boolean(busyResponse)}
          emphasis="secondary"
          label={t("proposal.acknowledge")}
          onPress={() => onAcknowledge(proposal.id)}
          style={styles.acknowledge}
        />
      ) : null}
    </View>
  );
}

const proposalStatusKeys = {
  pending: "proposal.statusPending",
  accepted: "proposal.statusAccepted",
  dismissed: "proposal.statusDismissed",
  acknowledged: "proposal.statusAcknowledged",
  superseded: "proposal.statusSuperseded",
} as const;

const styles = StyleSheet.create({
  card: {
    backgroundColor: colors.surfaceSelected,
    borderRadius: radius.overlay,
    gap: spacing[3],
    padding: spacing[4],
  },
  header: { alignItems: "center", flexDirection: "row", gap: spacing[2], justifyContent: "space-between" },
  eyebrow: { color: colors.textAccent, fontSize: type.helper, fontWeight: "600", lineHeight: type.helperLine },
  statusChip: { backgroundColor: colors.surface, borderRadius: radius.pill, paddingHorizontal: spacing[2], paddingVertical: spacing[1] },
  acceptedChip: { backgroundColor: colors.positiveSoft },
  statusText: { color: colors.text, fontSize: 12, fontWeight: "500", lineHeight: 18 },
  title: { color: colors.text, fontSize: type.heading, fontWeight: "600", lineHeight: type.headingLine },
  body: { color: colors.text, fontSize: type.body, lineHeight: type.bodyLine },
  reason: { borderTopColor: colors.border, borderTopWidth: StyleSheet.hairlineWidth, gap: spacing[1], paddingTop: spacing[3] },
  reasonLabel: { color: colors.textMuted, fontSize: type.helper, fontWeight: "500", lineHeight: type.helperLine },
  reasonBody: { color: colors.textMuted, fontSize: type.helper, lineHeight: type.helperLine },
  availableAt: { color: colors.warning, fontSize: type.helper, lineHeight: type.helperLine },
  actions: { flexDirection: "row", gap: spacing[2] },
  action: { flex: 1 },
  acknowledge: { alignSelf: "flex-end" },
});
