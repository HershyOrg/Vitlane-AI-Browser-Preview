import { Pressable, ScrollView, StyleSheet, Text, View } from "react-native";

import { useLocale } from "../i18n/LocaleProvider";
import { colors, radius, spacing, type } from "../theme/tokens";
import type { BrowserDestinationTarget } from "./browserDestinations";

export type BrowserDestinationLauncherProps = {
  readonly destinations: readonly BrowserDestinationTarget[];
  readonly onSelect: (destination: BrowserDestinationTarget) => void;
};

/** A reviewed list only. Navigation still requires the caller's approval step. */
export function BrowserDestinationLauncher({
  destinations,
  onSelect,
}: BrowserDestinationLauncherProps) {
  const { t } = useLocale();

  return (
    <View accessibilityLabel={t("browserDestination.title")} style={styles.root}>
      <View style={styles.heading}>
        <Text accessibilityRole="header" style={styles.title}>{t("browserDestination.title")}</Text>
        <Text style={styles.description}>{t("browserDestination.description")}</Text>
      </View>

      {destinations.length === 0 ? (
        <Text style={styles.empty}>{t("browserDestination.empty")}</Text>
      ) : (
        <ScrollView
          contentContainerStyle={styles.list}
          horizontal
          showsHorizontalScrollIndicator={false}
          testID="browser-destination.list"
        >
          {destinations.map((destination) => (
            <Pressable
              accessibilityHint={t("browserDestination.selectHint")}
              accessibilityLabel={t("browserDestination.selectLabel", { value: destination.label })}
              accessibilityRole="button"
              key={destination.id}
              onPress={() => onSelect(destination)}
              style={({ pressed }) => [styles.destination, pressed && styles.pressed]}
              testID={`browser-destination.${destination.id}`}
            >
              <View style={styles.copy}>
                <View style={styles.labelRow}>
                  <Text style={styles.label}>{destination.label}</Text>
                  <View style={styles.kindChip}>
                    <Text style={styles.kindText}>
                      {t(destination.kind === "reservation"
                        ? "browserDestination.kindReservation"
                        : "browserDestination.kindShopping")}
                    </Text>
                  </View>
                </View>
                <Text numberOfLines={1} style={styles.host}>{destination.host}</Text>
              </View>
              <Text accessibilityElementsHidden importantForAccessibility="no-hide-descendants" style={styles.chevron}>›</Text>
            </Pressable>
          ))}
        </ScrollView>
      )}

      <Text style={styles.boundary}>{t("browserDestination.boundary")}</Text>
    </View>
  );
}

const styles = StyleSheet.create({
  root: { gap: spacing[4], width: "100%" },
  heading: { gap: spacing[1] },
  title: { color: colors.text, fontSize: type.heading, fontWeight: "600", lineHeight: type.headingLine },
  description: { color: colors.textMuted, fontSize: type.body, lineHeight: type.bodyLine },
  list: { gap: spacing[2], paddingRight: spacing[4] },
  destination: {
    alignItems: "center",
    backgroundColor: colors.surface,
    borderColor: colors.border,
    borderRadius: radius.overlay,
    borderWidth: StyleSheet.hairlineWidth,
    flexDirection: "row",
    gap: spacing[3],
    minHeight: 68,
    paddingHorizontal: spacing[4],
    paddingVertical: spacing[3],
    width: 188,
  },
  pressed: { backgroundColor: colors.surfaceSelected },
  copy: { flex: 1, gap: spacing[1], minWidth: 0 },
  labelRow: { alignItems: "center", flexDirection: "row", flexWrap: "wrap", gap: spacing[2] },
  label: { color: colors.text, fontSize: type.body, fontWeight: "600", lineHeight: type.bodyLine },
  kindChip: { backgroundColor: colors.surfaceSelected, borderRadius: radius.pill, paddingHorizontal: spacing[2], paddingVertical: 2 },
  kindText: { color: colors.textAccent, fontSize: 11, fontWeight: "600", lineHeight: 16 },
  host: { color: colors.textMuted, fontSize: type.helper, lineHeight: type.helperLine },
  chevron: { color: colors.textAccent, fontSize: 28, fontWeight: "300", lineHeight: 32 },
  empty: { color: colors.textMuted, fontSize: type.body, lineHeight: type.bodyLine },
  boundary: { color: colors.textMuted, fontSize: type.helper, lineHeight: type.helperLine },
});
