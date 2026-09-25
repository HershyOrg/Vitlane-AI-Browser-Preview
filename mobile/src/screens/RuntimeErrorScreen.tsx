import { ScrollView, StyleSheet, Text, View } from "react-native";

import { useLocale } from "../i18n/LocaleProvider";
import { colors, size, spacing, type } from "../theme/tokens";

export function RuntimeErrorScreen({ detail }: { detail: string }) {
  const { t } = useLocale();
  return (
    <ScrollView contentContainerStyle={styles.page}>
      <View accessibilityLiveRegion="assertive" style={styles.card}>
        <Text accessibilityRole="header" style={styles.title}>{t("runtime.errorTitle")}</Text>
        <Text style={styles.description}>{t("runtime.errorDescription")}</Text>
        {__DEV__ ? <Text selectable style={styles.detail}>{detail}</Text> : null}
      </View>
    </ScrollView>
  );
}

const styles = StyleSheet.create({
  page: { alignItems: "center", flexGrow: 1, padding: spacing[4], paddingTop: spacing[12] },
  card: { backgroundColor: colors.surfaceSubtle, maxWidth: size.contentMax, padding: spacing[5], width: "100%" },
  title: { color: colors.text, fontSize: type.heading, fontWeight: "600", lineHeight: type.headingLine },
  description: { color: colors.textMuted, fontSize: type.body, lineHeight: type.bodyLine, marginTop: spacing[2] },
  detail: { color: colors.danger, fontSize: type.helper, lineHeight: type.helperLine, marginTop: spacing[4] },
});
