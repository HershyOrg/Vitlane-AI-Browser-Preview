import { StyleSheet, Text, View } from "react-native";

import { useLocale } from "../i18n/LocaleProvider";
import { colors, radius, spacing, type } from "../theme/tokens";

export function DemoNotice({
  detailed = false,
  mode = "fixture",
}: {
  detailed?: boolean;
  mode?: "fixture" | "server";
}) {
  const { t } = useLocale();
  const live = mode === "server";
  return (
    <View accessibilityRole="summary" style={styles.container}>
      <View style={[styles.dot, live && styles.liveDot]} />
      <View style={styles.copy}>
        <Text style={styles.label}>{t(live ? "app.live" : "app.demo")}</Text>
        {detailed ? (
          <Text style={styles.detail}>{t(live ? "app.liveDetail" : "app.demoDetail")}</Text>
        ) : null}
      </View>
    </View>
  );
}

const styles = StyleSheet.create({
  container: {
    alignItems: "center",
    alignSelf: "flex-start",
    backgroundColor: colors.surfaceSubtle,
    borderRadius: radius.pill,
    flexDirection: "row",
    gap: spacing[2],
    paddingHorizontal: spacing[3],
    paddingVertical: spacing[1],
  },
  dot: {
    backgroundColor: colors.warning,
    borderRadius: radius.pill,
    height: 8,
    marginTop: 0,
    width: 8,
  },
  liveDot: { backgroundColor: colors.positive },
  copy: { flex: 1 },
  label: {
    color: colors.textMuted,
    fontSize: 12,
    fontWeight: "500",
    lineHeight: 18,
  },
  detail: {
    color: colors.textMuted,
    fontSize: 12,
    lineHeight: 18,
    marginTop: spacing[1],
  },
});
