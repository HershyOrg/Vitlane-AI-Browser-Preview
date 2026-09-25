import {
  StyleSheet,
  Text,
  TextInput,
  View,
} from "react-native";

import { useLocale } from "../i18n/LocaleProvider";
import { colors, radius, spacing, type } from "../theme/tokens";
import { ActionButton } from "./ActionButton";

type IntentComposerProps = {
  value: string;
  onChange: (value: string) => void;
  onSubmit: () => void;
  busy?: boolean;
  error?: string;
  compact?: boolean;
  placeholder?: string;
  submitLabel?: string;
  inputTestID?: string;
  submitTestID?: string;
};

export function IntentComposer({
  value,
  onChange,
  onSubmit,
  busy = false,
  error,
  compact = false,
  placeholder,
  submitLabel,
  inputTestID,
  submitTestID,
}: IntentComposerProps) {
  const { t } = useLocale();
  return (
    <View style={[styles.container, compact && styles.compact]}>
      <TextInput
        accessibilityLabel={placeholder ?? t("home.placeholder")}
        multiline
        onChangeText={onChange}
        placeholder={placeholder ?? t("home.placeholder")}
        placeholderTextColor={colors.textMuted}
        style={[styles.input, compact && styles.compactInput]}
        submitBehavior="newline"
        testID={inputTestID}
        textAlignVertical="top"
        value={value}
      />
      <View style={styles.actionRow}>
        {error ? <Text accessibilityLiveRegion="polite" style={styles.error}>{error}</Text> : <View style={styles.actionSpacer} />}
        <ActionButton
          busy={busy}
          compact
          disabled={!value.trim()}
          emphasis="primary"
          label={submitLabel ?? t("home.send")}
          onPress={onSubmit}
          testID={submitTestID}
        />
      </View>
    </View>
  );
}

const styles = StyleSheet.create({
  container: {
    backgroundColor: colors.surfaceSubtle,
    borderColor: colors.border,
    borderRadius: radius.sheet,
    borderWidth: StyleSheet.hairlineWidth,
    gap: spacing[2],
    padding: spacing[2],
  },
  compact: { borderRadius: radius.sheet },
  input: {
    color: colors.text,
    fontSize: 18,
    lineHeight: 27,
    minHeight: 104,
    paddingHorizontal: spacing[3],
    paddingTop: spacing[3],
  },
  compactInput: {
    fontSize: type.body,
    lineHeight: type.bodyLine,
    minHeight: 52,
  },
  actionRow: { alignItems: "center", flexDirection: "row", gap: spacing[2] },
  actionSpacer: { flex: 1 },
  error: {
    color: colors.danger,
    flex: 1,
    fontSize: type.helper,
    lineHeight: type.helperLine,
  },
});
