import { useEffect, useState } from "react";
import { Modal, Pressable, ScrollView, StyleSheet, Text, View } from "react-native";
import { useSafeAreaInsets } from "react-native-safe-area-context";

import { useLocale } from "../i18n/LocaleProvider";
import { colors, radius, size, spacing, type } from "../theme/tokens";
import { ActionButton } from "./ActionButton";
import { AgentAvatar } from "./AgentAvatar";

export type ShoppingMemoryValue = {
  readonly researchCountry: "KR" | "US";
  readonly preferredCurrency: "KRW" | "USD";
  readonly uiLocale: "ko-KR" | "en-US";
};

export type ShoppingMemorySheetProps = {
  visible: boolean;
  currentValue: ShoppingMemoryValue;
  draftValue: ShoppingMemoryValue;
  onDraftChange: (value: ShoppingMemoryValue) => void;
  onApply: () => void;
  onClose: () => void;
  working?: boolean;
  error?: string;
};

export function ShoppingMemorySheet({
  visible,
  currentValue,
  draftValue,
  onDraftChange,
  onApply,
  onClose,
  working = false,
  error,
}: ShoppingMemorySheetProps) {
  const { t } = useLocale();
  const insets = useSafeAreaInsets();
  const [confirmDiscard, setConfirmDiscard] = useState(false);
  const dirty = !sameMemoryValue(currentValue, draftValue);
  useEffect(() => setConfirmDiscard(false), [visible]);
  const attemptClose = () => {
    if (dirty) setConfirmDiscard(true);
    else onClose();
  };

  return (
    <Modal animationType="slide" onRequestClose={attemptClose} statusBarTranslucent transparent visible={visible}>
      <View style={styles.modalRoot}>
        <Pressable accessible={false} onPress={attemptClose} style={styles.scrim} />
        <View
          accessibilityViewIsModal
          importantForAccessibility="yes"
          style={[styles.sheet, { paddingBottom: Math.max(insets.bottom, spacing[3]) }]}
        >
          <View style={styles.handle} />
          <View style={styles.header}>
            <AgentAvatar size={40} />
            <View style={styles.headerCopy}>
              <Text accessibilityRole="header" style={styles.title}>{t("memory.title")}</Text>
              <Text style={styles.subtitle}>{t("memory.subtitle")}</Text>
            </View>
            <ActionButton compact emphasis="quiet" label={t("common.close")} onPress={attemptClose} />
          </View>

          <ScrollView contentContainerStyle={styles.content}>
            <Text style={styles.description}>{t("memory.description")}</Text>
            <PreferenceGroup
              label={t("memory.country")}
              onSelect={(researchCountry) => onDraftChange({ ...draftValue, researchCountry })}
              options={[
                { label: t("memory.countryKR"), value: "KR" },
                { label: t("memory.countryUS"), value: "US" },
              ]}
              selected={draftValue.researchCountry}
            />
            <PreferenceGroup
              label={t("memory.currency")}
              onSelect={(preferredCurrency) => onDraftChange({ ...draftValue, preferredCurrency })}
              options={[
                { label: t("memory.currencyKRW"), value: "KRW" },
                { label: t("memory.currencyUSD"), value: "USD" },
              ]}
              selected={draftValue.preferredCurrency}
            />
            <PreferenceGroup
              label={t("memory.language")}
              onSelect={(uiLocale) => onDraftChange({ ...draftValue, uiLocale })}
              options={[
                { label: t("memory.languageKO"), value: "ko-KR" },
                { label: t("memory.languageEN"), value: "en-US" },
              ]}
              selected={draftValue.uiLocale}
            />
            {error ? <Text accessibilityRole="alert" style={styles.error}>{error}</Text> : null}
          </ScrollView>

          {confirmDiscard ? (
            <View style={styles.discardPanel}>
              <Text style={styles.discardTitle}>{t("memory.discardTitle")}</Text>
              <Text style={styles.discardBody}>{t("memory.discardBody")}</Text>
              <View style={styles.actions}>
                <ActionButton
                  emphasis="secondary"
                  label={t("memory.keepEditing")}
                  onPress={() => setConfirmDiscard(false)}
                  style={styles.action}
                />
                <ActionButton emphasis="danger" label={t("memory.discard")} onPress={onClose} style={styles.action} />
              </View>
            </View>
          ) : (
            <View style={styles.footer}>
              <ActionButton disabled={working} emphasis="secondary" label={t("common.cancel")} onPress={attemptClose} style={styles.action} />
              <ActionButton busy={working} disabled={!dirty} emphasis="primary" label={t("memory.apply")} onPress={onApply} style={styles.action} />
            </View>
          )}
        </View>
      </View>
    </Modal>
  );
}

type PreferenceGroupProps<T extends string> = {
  label: string;
  selected: T;
  options: readonly { readonly label: string; readonly value: T }[];
  onSelect: (value: T) => void;
};

function PreferenceGroup<T extends string>({ label, selected, options, onSelect }: PreferenceGroupProps<T>) {
  return (
    <View style={styles.group}>
      <Text style={styles.groupLabel}>{label}</Text>
      <View accessibilityRole="radiogroup" style={styles.optionRow}>
        {options.map((option) => {
          const checked = selected === option.value;
          return (
            <Pressable
              accessibilityLabel={option.label}
              accessibilityRole="radio"
              accessibilityState={{ checked, selected: checked }}
              key={option.value}
              onPress={() => onSelect(option.value)}
              style={[styles.option, checked && styles.optionSelected]}
            >
              <Text style={[styles.optionLabel, checked && styles.optionLabelSelected]}>{option.label}</Text>
              {checked ? <Text style={styles.check}>✓</Text> : null}
            </Pressable>
          );
        })}
      </View>
    </View>
  );
}

function sameMemoryValue(left: ShoppingMemoryValue, right: ShoppingMemoryValue) {
  return left.researchCountry === right.researchCountry
    && left.preferredCurrency === right.preferredCurrency
    && left.uiLocale === right.uiLocale;
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
    overflow: "hidden",
    width: "100%",
  },
  handle: { alignSelf: "center", backgroundColor: colors.borderStrong, borderRadius: radius.pill, height: 4, marginTop: spacing[2], width: 40 },
  header: { alignItems: "center", flexDirection: "row", gap: spacing[3], minHeight: size.header, paddingHorizontal: spacing[4] },
  headerCopy: { flex: 1, minWidth: 0 },
  title: { color: colors.text, fontSize: type.body, fontWeight: "600", lineHeight: type.bodyLine },
  subtitle: { color: colors.textMuted, fontSize: 12, lineHeight: 18 },
  content: { gap: spacing[5], padding: spacing[4], paddingBottom: spacing[6] },
  description: { color: colors.textMuted, fontSize: type.body, lineHeight: type.bodyLine },
  group: { gap: spacing[2] },
  groupLabel: { color: colors.text, fontSize: type.helper, fontWeight: "600", lineHeight: type.helperLine },
  optionRow: { flexDirection: "row", gap: spacing[2] },
  option: {
    alignItems: "center",
    backgroundColor: colors.surfaceSubtle,
    borderColor: "transparent",
    borderRadius: radius.control,
    borderWidth: 1.5,
    flex: 1,
    flexDirection: "row",
    justifyContent: "space-between",
    minHeight: size.touch,
    paddingHorizontal: spacing[3],
  },
  optionSelected: { backgroundColor: colors.surfaceSelected, borderColor: colors.action },
  optionLabel: { color: colors.text, flex: 1, fontSize: type.helper, lineHeight: type.helperLine },
  optionLabelSelected: { color: colors.textAccent, fontWeight: "600" },
  check: { color: colors.textAccent, fontSize: type.body, fontWeight: "600" },
  error: { color: colors.danger, fontSize: type.helper, lineHeight: type.helperLine },
  footer: { borderTopColor: colors.border, borderTopWidth: StyleSheet.hairlineWidth, flexDirection: "row", gap: spacing[2], padding: spacing[4] },
  actions: { flexDirection: "row", gap: spacing[2] },
  action: { flex: 1 },
  discardPanel: { backgroundColor: colors.warningSoft, borderTopColor: colors.border, borderTopWidth: StyleSheet.hairlineWidth, gap: spacing[2], padding: spacing[4] },
  discardTitle: { color: colors.text, fontSize: type.body, fontWeight: "600", lineHeight: type.bodyLine },
  discardBody: { color: colors.textMuted, fontSize: type.helper, lineHeight: type.helperLine },
});
