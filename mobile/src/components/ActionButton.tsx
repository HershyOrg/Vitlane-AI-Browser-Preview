import {
  ActivityIndicator,
  Pressable,
  StyleSheet,
  Text,
  type PressableProps,
  type StyleProp,
  type ViewStyle,
} from "react-native";

import { colors, radius, size, spacing, type } from "../theme/tokens";

export type ActionButtonEmphasis = "primary" | "secondary" | "quiet" | "danger";

type ActionButtonProps = Omit<PressableProps, "children" | "style"> & {
  label: string;
  emphasis?: ActionButtonEmphasis;
  busy?: boolean;
  compact?: boolean;
  style?: StyleProp<ViewStyle>;
};

export function ActionButton({
  label,
  emphasis = "secondary",
  busy = false,
  compact = false,
  disabled,
  style,
  ...props
}: ActionButtonProps) {
  const unavailable = disabled || busy;
  return (
    <Pressable
      accessibilityLabel={label}
      accessibilityRole="button"
      accessibilityState={{ busy, disabled: unavailable }}
      disabled={unavailable}
      style={({ pressed }) => [
        styles.base,
        compact ? styles.compact : styles.defaultSize,
        emphasisStyles[emphasis],
        pressed && !unavailable && styles.pressed,
        unavailable && styles.disabled,
        style,
      ]}
      {...props}
    >
      {busy ? (
        <ActivityIndicator
          color={emphasis === "primary" ? colors.onAction : colors.action}
          size="small"
        />
      ) : (
        <Text style={[styles.label, labelStyles[emphasis]]}>{label}</Text>
      )}
    </Pressable>
  );
}

const styles = StyleSheet.create({
  base: {
    alignItems: "center",
    borderRadius: radius.pill,
    flexDirection: "row",
    justifyContent: "center",
    minWidth: size.touch,
    paddingHorizontal: spacing[4],
  },
  defaultSize: { minHeight: size.primary },
  compact: { minHeight: size.touch },
  label: {
    fontSize: type.body,
    fontWeight: "500",
    lineHeight: type.bodyLine,
    textAlign: "center",
  },
  pressed: { opacity: 0.72, transform: [{ scale: 0.985 }] },
  disabled: { opacity: 0.48 },
});

const emphasisStyles = StyleSheet.create({
  primary: { backgroundColor: colors.action },
  secondary: { backgroundColor: colors.surfaceSubtle },
  quiet: { backgroundColor: "transparent" },
  danger: { backgroundColor: "transparent" },
});

const labelStyles = StyleSheet.create({
  primary: { color: colors.onAction },
  secondary: { color: colors.text },
  quiet: { color: colors.textAccent },
  danger: { color: colors.danger },
});
