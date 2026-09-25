import { StyleSheet, View } from "react-native";
import { useSafeAreaInsets } from "react-native-safe-area-context";

import { spacing } from "../theme/tokens";
import { ActionButton } from "./ActionButton";

type StickyFooterProps = {
  primaryLabel: string;
  onPrimary: () => void;
  primaryDisabled?: boolean;
  primaryBusy?: boolean;
  secondaryLabel?: string;
  onSecondary?: () => void;
  primaryTestID?: string;
  secondaryTestID?: string;
};

export function StickyFooter({
  primaryLabel,
  onPrimary,
  primaryDisabled,
  primaryBusy,
  secondaryLabel,
  onSecondary,
  primaryTestID,
  secondaryTestID,
}: StickyFooterProps) {
  const insets = useSafeAreaInsets();
  return (
    <View style={[styles.container, { paddingBottom: Math.max(insets.bottom, spacing[3]) }]}>
      {secondaryLabel && onSecondary ? (
        <ActionButton
          emphasis="secondary"
          label={secondaryLabel}
          onPress={onSecondary}
          style={styles.secondary}
          testID={secondaryTestID}
        />
      ) : null}
      <ActionButton
        busy={primaryBusy}
        disabled={primaryDisabled}
        emphasis="primary"
        label={primaryLabel}
        onPress={onPrimary}
        style={styles.primary}
        testID={primaryTestID}
      />
    </View>
  );
}

const styles = StyleSheet.create({
  container: {
    flexDirection: "row",
    gap: spacing[2],
    paddingHorizontal: spacing[4],
    paddingTop: spacing[3],
  },
  secondary: { flex: 0.8 },
  primary: { flex: 1.2 },
});
