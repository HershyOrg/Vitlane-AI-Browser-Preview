import { useEffect, useState } from "react";
import {
  KeyboardAvoidingView,
  Modal,
  Platform,
  Pressable,
  ScrollView,
  StyleSheet,
  Text,
  TextInput,
  View,
} from "react-native";
import { useSafeAreaInsets } from "react-native-safe-area-context";

import {
  currencyStep,
  formatMoney,
  normalizeMoneyAmount,
  type Currency,
  type Money,
  type PlanTotals,
} from "../domain";
import { useLocale } from "../i18n/LocaleProvider";
import { colors, radius, size, spacing, type } from "../theme/tokens";
import { ActionButton } from "./ActionButton";
import { AgentAvatar } from "./AgentAvatar";
import { DemoNotice } from "./DemoNotice";
import { StickyFooter } from "./StickyFooter";

export type BudgetPreviewView = {
  knownTotal: Money;
  remaining: Money;
  overage: Money;
  totalState: PlanTotals["totalState"];
  comparisonState: PlanTotals["budgetComparisonState"];
  summary: string;
};

type BudgetSheetProps = {
  visible: boolean;
  currentBudget: number;
  currentBudgetUnlimited?: boolean;
  draftBudget: number;
  onDraftChange: (amount: number) => void;
  preview?: BudgetPreviewView;
  onApply: () => void;
  onClose: () => void;
  working?: boolean;
  budgetOptions: readonly number[];
  previewMessage?: string;
  error?: string;
  currency: Currency;
  runtimeMode?: "fixture" | "server";
};

export function BudgetSheet({
  visible,
  currentBudget,
  currentBudgetUnlimited = false,
  draftBudget,
  onDraftChange,
  preview,
  onApply,
  onClose,
  working = false,
  budgetOptions,
  previewMessage,
  error,
  currency,
  runtimeMode = "fixture",
}: BudgetSheetProps) {
  const { locale, t } = useLocale();
  const insets = useSafeAreaInsets();
  const [input, setInput] = useState(String(draftBudget));
  const [confirmDiscard, setConfirmDiscard] = useState(false);
  useEffect(() => setInput(String(draftBudget)), [draftBudget, visible]);
  useEffect(() => setConfirmDiscard(false), [visible]);

  const dirty = draftBudget !== currentBudget;
  const attemptClose = () => {
    if (dirty) setConfirmDiscard(true);
    else onClose();
  };

  const update = (next: number) => {
    const normalized = normalizeMoneyAmount(next, currency);
    setInput(String(normalized));
    onDraftChange(normalized);
  };

  return (
    <Modal
      animationType="slide"
      onRequestClose={attemptClose}
      statusBarTranslucent
      transparent
      visible={visible}
    >
      <View style={styles.modalRoot}>
        <Pressable accessible={false} onPress={attemptClose} style={styles.scrim} />
        <KeyboardAvoidingView
          behavior={Platform.OS === "ios" ? "padding" : "height"}
          pointerEvents="box-none"
          style={styles.keyboardRoot}
        >
          <View
            accessibilityViewIsModal
            importantForAccessibility="yes"
            style={styles.sheet}
          >
          <View style={styles.handle} />
          <View style={styles.header}>
            <AgentAvatar active size={36} />
            <View style={styles.headerCopy}>
              <Text style={styles.agentName}>{t("agent.name")}</Text>
              <Text accessibilityRole="header" style={styles.headerTitle}>{t("budget.title")}</Text>
            </View>
            <ActionButton compact emphasis="quiet" label={t("common.close")} onPress={attemptClose} />
          </View>
          <View style={styles.demoHost}><DemoNotice mode={runtimeMode} /></View>
          <ScrollView
            contentContainerStyle={styles.content}
            keyboardDismissMode={Platform.OS === "ios" ? "interactive" : "on-drag"}
            keyboardShouldPersistTaps="handled"
          >
            <Text style={styles.description}>{t("budget.description")}</Text>
            <View style={styles.budgetHero}>
              <View style={styles.currentRow}>
                <Text style={styles.label}>{t("budget.current")}</Text>
                <Text style={styles.currentAmount}>
                  {currentBudgetUnlimited
                    ? t("budget.unlimited")
                    : formatMoney({ currency, amount: currentBudget }, locale)}
                </Text>
              </View>
              <View style={styles.budgetArrow}><Text style={styles.budgetArrowText}>↓</Text></View>
              <Text style={styles.draftLabel}>{t("budget.amountInput")}</Text>
            </View>
            <View accessibilityLabel={t("budget.amountInput")} style={styles.stepper}>
              <ActionButton
                accessibilityLabel={t("budget.decrease", {
                  amount: formatMoney({ currency, amount: currencyStep(currency) }, locale),
                })}
                compact
                emphasis="secondary"
                label="−"
                onPress={() => update(draftBudget - currencyStep(currency))}
              />
              <View style={styles.inputWrap}>
                <TextInput
                  accessibilityLabel={t("budget.amountInput")}
                  inputMode={currency === "KRW" ? "numeric" : "decimal"}
                  onBlur={() => update(parseInput(input, currency) ?? draftBudget)}
                  onChangeText={(value) => {
                    const sanitized = currency === "KRW"
                      ? value.replace(/\D/g, "")
                      : value.replace(/[^\d.]/g, "").replace(/(\..*)\./g, "$1");
                    setInput(sanitized);
                    const parsed = parseInput(sanitized, currency);
                    if (parsed !== null) onDraftChange(parsed);
                  }}
                  onSubmitEditing={() => update(parseInput(input, currency) ?? draftBudget)}
                  returnKeyType="done"
                  style={styles.amountInput}
                  testID="budget.amount"
                  value={input}
                />
                <Text style={styles.currency}>{currency}</Text>
              </View>
              <ActionButton
                accessibilityLabel={t("budget.increase", {
                  amount: formatMoney({ currency, amount: currencyStep(currency) }, locale),
                })}
                compact
                emphasis="secondary"
                label="+"
                onPress={() => update(draftBudget + currencyStep(currency))}
              />
            </View>
            <View accessibilityRole="radiogroup" style={styles.presets}>
              {budgetOptions.map((amount) => {
                const selected = draftBudget === amount;
                return (
                  <Pressable
                    accessibilityRole="radio"
                    accessibilityState={{ checked: selected, selected }}
                    key={amount}
                    onPress={() => update(amount)}
                    style={[styles.preset, selected && styles.presetSelected]}
                    testID={`budget.preset.${amount}`}
                  >
                    <Text style={[styles.presetAmount, selected && styles.presetAmountSelected]}>{formatMoney({ currency, amount }, locale)}</Text>
                    {selected ? <Text style={styles.selectedLabel}>✓ {t("common.selected")}</Text> : null}
                  </Pressable>
                );
              })}
            </View>
            <View style={styles.preview}>
              <Text style={styles.previewEyebrow}>{t("budget.feasible")}</Text>
              {preview ? (
                <>
                  <Text style={styles.previewSummary}>{preview.summary}</Text>
                  <View style={styles.metricRow}>
                    <Text style={styles.metricLabel}>{t("budget.previewTotal")}</Text>
                    <Text style={styles.metricValue}>{formatMoney(preview.knownTotal, locale)}</Text>
                  </View>
                  {preview.comparisonState === "AVAILABLE" ? (
                    <View style={styles.metricRow}>
                      <Text style={styles.metricLabel}>
                        {t(preview.overage.amount > 0 ? "budget.previewOverage" : "budget.previewRemaining")}
                      </Text>
                      <Text style={styles.metricValue}>{formatMoney(
                        preview.overage.amount > 0 ? preview.overage : preview.remaining,
                        locale,
                      )}</Text>
                    </View>
                  ) : (
                    <Text style={styles.previewUnavailable}>
                      {t("budget.previewComparisonUnavailable")}
                    </Text>
                  )}
                </>
              ) : (
                <Text style={[styles.previewPending, previewMessage && styles.previewUnavailable]}>
                  {previewMessage ?? t("budget.previewPending")}
                </Text>
              )}
            </View>
          </ScrollView>
          {error ? <Text accessibilityRole="alert" style={styles.error}>{error}</Text> : null}
          {confirmDiscard ? (
            <View
              accessibilityLiveRegion="polite"
              style={[styles.discardPanel, { paddingBottom: Math.max(insets.bottom, spacing[4]) }]}
            >
              <Text style={styles.discardTitle}>{t("budget.discardTitle")}</Text>
              <Text style={styles.discardBody}>{t("budget.discardBody")}</Text>
              <View style={styles.discardActions}>
                <ActionButton
                  emphasis="secondary"
                  label={t("budget.keepEditing")}
                  onPress={() => setConfirmDiscard(false)}
                  style={styles.flexAction}
                />
                <ActionButton
                  emphasis="danger"
                  label={t("budget.discard")}
                  onPress={() => {
                    setConfirmDiscard(false);
                    onClose();
                  }}
                  style={styles.flexAction}
                />
              </View>
            </View>
          ) : (
            <View style={styles.footerBorder}>
              <StickyFooter
                onPrimary={onApply}
                onSecondary={attemptClose}
                primaryBusy={working}
                primaryDisabled={!preview || draftBudget === currentBudget}
                primaryLabel={t("budget.apply")}
                primaryTestID="budget.apply"
                secondaryLabel={t("common.cancel")}
              />
            </View>
          )}
          </View>
        </KeyboardAvoidingView>
      </View>
    </Modal>
  );
}

function parseInput(value: string, currency: Currency): number | null {
  if (!value.trim()) return null;
  const parsed = Number(value);
  if (!Number.isFinite(parsed) || parsed < 0) return null;
  return normalizeMoneyAmount(parsed, currency);
}

const styles = StyleSheet.create({
  modalRoot: { flex: 1 },
  scrim: { ...StyleSheet.absoluteFill, backgroundColor: colors.scrim },
  keyboardRoot: { flex: 1, justifyContent: "flex-end" },
  sheet: {
    alignSelf: "center",
    backgroundColor: colors.surface,
    borderTopLeftRadius: radius.sheet,
    borderTopRightRadius: radius.sheet,
    maxHeight: "92%",
    maxWidth: size.contentMax + spacing[8] * 2,
    minHeight: "78%",
    overflow: "hidden",
    width: "100%",
  },
  handle: {
    alignSelf: "center",
    backgroundColor: colors.borderStrong,
    borderRadius: radius.pill,
    height: 4,
    marginTop: spacing[2],
    width: 40,
  },
  header: {
    alignItems: "center",
    flexDirection: "row",
    gap: spacing[3],
    justifyContent: "space-between",
    minHeight: size.header,
    paddingHorizontal: spacing[4],
  },
  headerCopy: { flex: 1 },
  agentName: { color: colors.text, fontSize: type.body, fontWeight: "600", lineHeight: type.bodyLine },
  headerTitle: { color: colors.textMuted, fontSize: 12, lineHeight: 18 },
  demoHost: { paddingHorizontal: spacing[4] },
  content: { gap: spacing[4], padding: spacing[4], paddingBottom: spacing[6] },
  description: { color: colors.textMuted, fontSize: type.body, lineHeight: type.bodyLine },
  budgetHero: { backgroundColor: colors.surfaceSelected, borderRadius: radius.overlay, padding: spacing[4] },
  currentRow: { alignItems: "flex-end", flexDirection: "row", justifyContent: "space-between" },
  label: { color: colors.textMuted, fontSize: type.helper, lineHeight: type.helperLine },
  currentAmount: { color: colors.text, fontSize: type.price, fontWeight: "600", lineHeight: type.priceLine },
  budgetArrow: { alignItems: "center", backgroundColor: colors.surface, borderRadius: radius.pill, height: 30, justifyContent: "center", marginTop: spacing[3], width: 30 },
  budgetArrowText: { color: colors.textAccent, fontSize: 17, fontWeight: "600" },
  draftLabel: { color: colors.textAccent, fontSize: type.helper, fontWeight: "600", lineHeight: type.helperLine, marginTop: spacing[2] },
  stepper: { alignItems: "center", flexDirection: "row", gap: spacing[2] },
  inputWrap: {
    alignItems: "center",
    backgroundColor: colors.surfaceSubtle,
    borderColor: colors.border,
    borderRadius: radius.pill,
    borderWidth: StyleSheet.hairlineWidth,
    flex: 1,
    flexDirection: "row",
    minHeight: size.primary,
    paddingHorizontal: spacing[3],
  },
  amountInput: { color: colors.text, flex: 1, fontSize: 20, fontWeight: "600", minWidth: 0, paddingVertical: 0 },
  currency: { color: colors.textMuted, fontSize: type.helper, marginLeft: spacing[2] },
  presets: { flexDirection: "row", gap: spacing[2] },
  preset: {
    backgroundColor: colors.surfaceSubtle,
    borderColor: "transparent",
    borderRadius: radius.overlay,
    borderWidth: 1.5,
    flex: 1,
    minHeight: 72,
    padding: spacing[2],
  },
  presetSelected: { backgroundColor: colors.surfaceSelected, borderColor: colors.action },
  presetAmount: { color: colors.text, fontSize: type.helper, fontWeight: "600", lineHeight: type.helperLine },
  presetAmountSelected: { color: colors.textAccent },
  selectedLabel: { color: colors.textAccent, fontSize: 11, marginTop: spacing[1] },
  preview: { backgroundColor: colors.surfaceSelected, borderRadius: radius.overlay, gap: spacing[2], padding: spacing[4] },
  previewEyebrow: { color: colors.textMuted, fontSize: type.helper, fontWeight: "500", lineHeight: type.helperLine },
  previewSummary: { color: colors.text, fontSize: type.body, fontWeight: "500", lineHeight: type.bodyLine },
  previewPending: { color: colors.textMuted, fontSize: type.body, lineHeight: type.bodyLine },
  previewUnavailable: { color: colors.danger },
  error: { color: colors.danger, fontSize: type.helper, lineHeight: type.helperLine, paddingHorizontal: spacing[4], paddingTop: spacing[2] },
  metricRow: { alignItems: "center", flexDirection: "row", justifyContent: "space-between" },
  metricLabel: { color: colors.textMuted, flex: 1, fontSize: type.helper, lineHeight: type.helperLine },
  metricValue: { color: colors.text, fontSize: type.body, fontWeight: "600", lineHeight: type.bodyLine },
  footerBorder: { borderTopColor: colors.border, borderTopWidth: StyleSheet.hairlineWidth },
  discardPanel: {
    backgroundColor: colors.warningSoft,
    borderTopColor: colors.border,
    borderTopWidth: StyleSheet.hairlineWidth,
    gap: spacing[2],
    padding: spacing[4],
  },
  discardTitle: { color: colors.text, fontSize: type.body, fontWeight: "600", lineHeight: type.bodyLine },
  discardBody: { color: colors.textMuted, fontSize: type.helper, lineHeight: type.helperLine },
  discardActions: { flexDirection: "row", gap: spacing[2] },
  flexAction: { flex: 1 },
});
