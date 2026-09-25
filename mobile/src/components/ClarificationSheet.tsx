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

import { useLocale } from "../i18n/LocaleProvider";
import { colors, radius, size, spacing, type } from "../theme/tokens";
import { ActionButton } from "./ActionButton";
import { AgentAvatar } from "./AgentAvatar";
import { ChoiceCard } from "./ChoiceCard";
import { DemoNotice } from "./DemoNotice";
import { StickyFooter } from "./StickyFooter";

export type ClarificationOptionView = {
  id: string;
  title: string;
  description: string;
};

export type ClarificationQuestionView = {
  id: string;
  title: string;
  reason: string;
  optional: boolean;
  allowCustom: boolean;
  options: ClarificationOptionView[];
};

export type ClarificationDraft = {
  selectedOptionId?: string;
  customText: string;
  noPreference: boolean;
};

type ClarificationSheetProps = {
  visible: boolean;
  question?: ClarificationQuestionView;
  current: number;
  total: number;
  draft: ClarificationDraft;
  onDraftChange: (draft: ClarificationDraft) => void;
  onApply: () => void;
  onBack?: () => void;
  onNext?: () => void;
  onClose: () => void;
  working?: boolean;
  hasUnappliedChanges?: boolean;
  error?: string;
  runtimeMode?: "fixture" | "server";
};

export function ClarificationSheet({
  visible,
  question,
  current,
  total,
  draft,
  onDraftChange,
  onApply,
  onBack,
  onNext,
  onClose,
  working = false,
  hasUnappliedChanges,
  error,
  runtimeMode = "fixture",
}: ClarificationSheetProps) {
  const { t } = useLocale();
  const insets = useSafeAreaInsets();
  const [confirmDiscard, setConfirmDiscard] = useState(false);

  useEffect(() => setConfirmDiscard(false), [question?.id, visible]);

  if (!question) return null;
  const dirty = hasUnappliedChanges
    ?? Boolean(draft.selectedOptionId || draft.customText.trim() || draft.noPreference);
  const canApply = dirty;
  const attemptClose = () => {
    if (dirty) setConfirmDiscard(true);
    else onClose();
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
        <Pressable
          accessible={false}
          onPress={attemptClose}
          style={styles.scrim}
        />
        <KeyboardAvoidingView
          behavior={Platform.OS === "ios" ? "padding" : "height"}
          pointerEvents="box-none"
          style={styles.keyboardRoot}
        >
          <View
            accessibilityViewIsModal
            importantForAccessibility="yes"
            style={styles.sheet}
            testID="question.sheet"
          >
          <View style={styles.handle} />
          <View style={styles.sheetHeader}>
            <AgentAvatar active size={36} />
            <View style={styles.headerCopy}>
              <Text accessibilityRole="header" style={styles.sheetTitle}>{t("agent.name")}</Text>
              <Text style={styles.step}>{t("question.step", { current, total })} · {t("question.title")}</Text>
            </View>
            <ActionButton compact emphasis="quiet" label={t("common.close")} onPress={attemptClose} />
          </View>
          <View style={styles.demoHost}><DemoNotice mode={runtimeMode} /></View>
          <ScrollView
            contentContainerStyle={styles.scrollContent}
            keyboardDismissMode={Platform.OS === "ios" ? "interactive" : "on-drag"}
            keyboardShouldPersistTaps="handled"
          >
            <View style={styles.progressTrack}>
              <View style={[styles.progressValue, { width: `${Math.min(100, (current / total) * 100)}%` }]} />
            </View>
            <Text accessibilityRole="header" style={styles.questionTitle}>{question.title}</Text>
            <View style={styles.reasonBox}>
              <Text style={styles.reasonLabel}>{t("question.why")}</Text>
              <Text style={styles.reason}>{question.reason}</Text>
            </View>
            <View accessibilityRole="radiogroup" style={styles.options}>
              {question.options.map((option) => (
                <ChoiceCard
                  description={option.description}
                  key={option.id}
                  onPress={() => onDraftChange({
                    selectedOptionId: option.id,
                    customText: "",
                    noPreference: false,
                  })}
                  selected={draft.selectedOptionId === option.id}
                  testID={`question.option.${option.id}`}
                  title={option.title}
                />
              ))}
            </View>
            {question.allowCustom ? (
              <View style={styles.customBlock}>
                <Text style={styles.inputLabel}>{t("question.custom")}</Text>
                <TextInput
                  accessibilityLabel={t("question.custom")}
                  onChangeText={(customText) => onDraftChange({
                    selectedOptionId: undefined,
                    customText,
                    noPreference: false,
                  })}
                  placeholder={t("question.customPlaceholder")}
                  placeholderTextColor={colors.textMuted}
                  style={styles.input}
                  testID="question.custom"
                  value={draft.customText}
                />
              </View>
            ) : null}
            {question.optional ? (
              <ActionButton
                emphasis={draft.noPreference ? "secondary" : "quiet"}
                label={t("question.noPreference")}
                onPress={() => onDraftChange({
                  selectedOptionId: undefined,
                  customText: "",
                  noPreference: true,
                })}
                testID="question.skip"
              />
            ) : null}
          </ScrollView>
          {error ? <Text accessibilityRole="alert" style={styles.error}>{error}</Text> : null}
          {confirmDiscard ? (
            <View
              accessibilityLiveRegion="polite"
              style={[styles.discardPanel, { paddingBottom: Math.max(insets.bottom, spacing[4]) }]}
            >
              <Text style={styles.discardTitle}>{t("question.discardTitle")}</Text>
              <Text style={styles.discardBody}>{t("question.discardBody")}</Text>
              <View style={styles.discardActions}>
                <ActionButton
                  emphasis="secondary"
                  label={t("question.keepEditing")}
                  onPress={() => setConfirmDiscard(false)}
                  style={styles.flexAction}
                />
                <ActionButton
                  emphasis="danger"
                  label={t("question.discard")}
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
                onPrimary={!dirty && onNext ? onNext : onApply}
                onSecondary={onBack}
                primaryBusy={working}
                primaryDisabled={!canApply && !onNext}
                primaryLabel={!dirty && onNext ? t("common.next") : t("question.apply")}
                primaryTestID={!dirty && onNext ? "question.next" : "question.apply"}
                secondaryLabel={onBack ? t("common.back") : undefined}
                secondaryTestID={onBack ? "question.back" : undefined}
              />
            </View>
          )}
          </View>
        </KeyboardAvoidingView>
      </View>
    </Modal>
  );
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
    minHeight: "72%",
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
  sheetHeader: {
    alignItems: "center",
    flexDirection: "row",
    gap: spacing[3],
    justifyContent: "space-between",
    minHeight: size.header,
    paddingHorizontal: spacing[4],
  },
  headerCopy: { flex: 1 },
  step: { color: colors.textMuted, fontSize: 12, lineHeight: 18 },
  sheetTitle: { color: colors.text, fontSize: 16, fontWeight: "600", lineHeight: 23 },
  demoHost: { paddingHorizontal: spacing[4] },
  scrollContent: { gap: spacing[4], padding: spacing[4], paddingBottom: spacing[6] },
  progressTrack: { backgroundColor: colors.surfaceSubtle, borderRadius: radius.pill, height: 5, overflow: "hidden" },
  progressValue: { backgroundColor: colors.action, borderRadius: radius.pill, height: 5 },
  questionTitle: {
    color: colors.text,
    fontSize: 25,
    fontWeight: "600",
    lineHeight: 33,
  },
  reasonBox: { backgroundColor: colors.surfaceSelected, borderRadius: radius.overlay, padding: spacing[3] },
  reasonLabel: { color: colors.textMuted, fontSize: type.helper, fontWeight: "500", lineHeight: type.helperLine },
  reason: { color: colors.text, fontSize: type.body, lineHeight: type.bodyLine, marginTop: spacing[1] },
  options: { gap: spacing[2] },
  customBlock: { gap: spacing[2] },
  inputLabel: { color: colors.text, fontSize: type.helper, fontWeight: "500", lineHeight: type.helperLine },
  input: {
    backgroundColor: colors.surfaceSubtle,
    borderColor: colors.border,
    borderRadius: radius.overlay,
    borderWidth: StyleSheet.hairlineWidth,
    color: colors.text,
    fontSize: type.body,
    minHeight: size.primary,
    paddingHorizontal: spacing[3],
  },
  error: { color: colors.danger, fontSize: type.helper, lineHeight: type.helperLine, paddingHorizontal: spacing[4], paddingTop: spacing[2] },
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
