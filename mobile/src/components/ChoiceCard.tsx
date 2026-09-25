import { Pressable, StyleSheet, Text, View } from "react-native";

import { colors, radius, size, spacing, type } from "../theme/tokens";

type ChoiceCardProps = {
  title: string;
  description: string;
  selected?: boolean;
  disabled?: boolean;
  onPress?: () => void;
  testID?: string;
};

export function ChoiceCard({
  title,
  description,
  selected = false,
  disabled = false,
  onPress,
  testID,
}: ChoiceCardProps) {
  return (
    <Pressable
      accessibilityLabel={`${title}. ${description}`}
      accessibilityRole="radio"
      accessibilityState={{ checked: selected, disabled, selected }}
      disabled={disabled}
      onPress={onPress}
      testID={testID}
      style={({ pressed }) => [
        styles.container,
        selected && styles.selected,
        pressed && !disabled && styles.pressed,
        disabled && styles.disabled,
      ]}
    >
      <View style={styles.copy}>
        <Text style={[styles.title, selected && styles.titleSelected]}>{title}</Text>
        <Text style={[styles.description, selected && styles.descriptionSelected]}>{description}</Text>
      </View>
      <View style={[styles.indicator, selected && styles.indicatorSelected]}>
        {selected ? <Text style={styles.check}>✓</Text> : null}
      </View>
    </Pressable>
  );
}

const styles = StyleSheet.create({
  container: {
    alignItems: "flex-start",
    backgroundColor: colors.surfaceSubtle,
    borderColor: "transparent",
    borderRadius: radius.overlay,
    borderWidth: 1.5,
    flexDirection: "row",
    gap: spacing[3],
    minHeight: 96,
    padding: spacing[4],
  },
  selected: {
    backgroundColor: colors.surfaceSelected,
    borderColor: colors.action,
  },
  pressed: { borderColor: colors.borderStrong },
  disabled: { opacity: 0.48 },
  indicator: {
    alignItems: "center",
    borderColor: colors.borderStrong,
    borderRadius: radius.pill,
    borderWidth: 1,
    height: 24,
    justifyContent: "center",
    marginTop: 1,
    width: 24,
  },
  indicatorSelected: { backgroundColor: colors.action, borderColor: colors.action },
  check: { color: colors.onAction, fontSize: 15, fontWeight: "600" },
  copy: { flex: 1, minHeight: size.touch, justifyContent: "center" },
  title: {
    color: colors.text,
    fontSize: type.body,
    fontWeight: "600",
    lineHeight: type.bodyLine,
  },
  titleSelected: { color: colors.textAccent },
  description: {
    color: colors.textMuted,
    fontSize: type.helper,
    lineHeight: type.helperLine,
    marginTop: spacing[1],
  },
  descriptionSelected: { color: colors.text },
});
