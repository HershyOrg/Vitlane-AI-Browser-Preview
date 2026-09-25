import { StyleSheet, View } from "react-native";

import { colors, radius } from "../theme/tokens";

type AgentAvatarProps = {
  size?: number;
  active?: boolean;
};

/**
 * Original Vitlane mark for the conversational agent. It is intentionally
 * code-native so the preview does not depend on Meta Muse artwork or branding.
 */
export function AgentAvatar({ size = 48, active = false }: AgentAvatarProps) {
  const unit = size / 48;
  return (
    <View
      accessibilityElementsHidden
      importantForAccessibility="no-hide-descendants"
      style={[
        styles.avatar,
        {
          borderRadius: size / 2,
          height: size,
          width: size,
        },
      ]}
    >
      <View
        style={[
          styles.orbit,
          {
            borderRadius: 16 * unit,
            height: 32 * unit,
            width: 32 * unit,
          },
        ]}
      />
      <View
        style={[
          styles.lane,
          {
            borderRadius: radius.pill,
            height: 23 * unit,
            left: 15 * unit,
            top: 11 * unit,
            width: 7 * unit,
          },
        ]}
      />
      <View
        style={[
          styles.laneHighlight,
          {
            borderRadius: radius.pill,
            height: 16 * unit,
            right: 14 * unit,
            top: 19 * unit,
            width: 7 * unit,
          },
        ]}
      />
      <View
        style={[
          styles.spark,
          active && styles.sparkActive,
          {
            borderRadius: 4 * unit,
            height: 8 * unit,
            right: 6 * unit,
            top: 6 * unit,
            width: 8 * unit,
          },
        ]}
      />
    </View>
  );
}

const styles = StyleSheet.create({
  avatar: {
    alignItems: "center",
    backgroundColor: colors.surfaceSelected,
    justifyContent: "center",
    overflow: "hidden",
  },
  orbit: {
    backgroundColor: colors.surface,
    opacity: 0.72,
    position: "absolute",
    transform: [{ rotate: "24deg" }],
  },
  lane: {
    backgroundColor: colors.action,
    position: "absolute",
    transform: [{ rotate: "22deg" }],
  },
  laneHighlight: {
    backgroundColor: colors.agentHighlight,
    position: "absolute",
    transform: [{ rotate: "22deg" }],
  },
  spark: {
    backgroundColor: colors.borderStrong,
    position: "absolute",
  },
  sparkActive: { backgroundColor: colors.positive },
});
