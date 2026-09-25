import { ActivityIndicator, ScrollView, StyleSheet, Text, View } from "react-native";

import { ActionButton } from "../components/ActionButton";
import { AgentAvatar } from "../components/AgentAvatar";
import { useLocale } from "../i18n/LocaleProvider";
import { colors, size, spacing, type } from "../theme/tokens";

type SignInScreenProps = {
  readonly restoring: boolean;
  readonly busy: boolean;
  readonly googleEnabled: boolean;
  readonly developmentEnabled: boolean;
  readonly error: string | null;
  readonly onGoogle: () => void;
  readonly onDevelopment: () => void;
  readonly onRetry: () => void;
};

export function SignInScreen({
  restoring,
  busy,
  googleEnabled,
  developmentEnabled,
  error,
  onGoogle,
  onDevelopment,
  onRetry,
}: SignInScreenProps) {
  const { t } = useLocale();
  return (
    <ScrollView contentContainerStyle={styles.page} keyboardShouldPersistTaps="handled">
      <View style={styles.content}>
        <AgentAvatar active={!error} size={88} />
        <Text accessibilityRole="header" style={styles.title}>{t("auth.title")}</Text>
        <Text style={styles.description}>{t("auth.description")}</Text>
        {restoring ? (
          <View accessibilityLiveRegion="polite" style={styles.status}>
            <ActivityIndicator color={colors.action} />
            <Text style={styles.statusText}>{t("auth.restoring")}</Text>
          </View>
        ) : (
          <View style={styles.actions}>
            <ActionButton
              busy={busy}
              disabled={!googleEnabled}
              emphasis="primary"
              label={t("auth.google")}
              onPress={onGoogle}
            />
            {developmentEnabled ? (
              <ActionButton
                busy={busy}
                label={t("auth.development")}
                onPress={onDevelopment}
              />
            ) : null}
            {!googleEnabled && !developmentEnabled ? (
              <Text style={styles.unavailable}>{t("auth.unavailable")}</Text>
            ) : null}
          </View>
        )}
        {error ? (
          <View accessibilityLiveRegion="assertive" style={styles.error}>
            <Text style={styles.errorTitle}>{t("auth.error")}</Text>
            <Text style={styles.errorBody}>{error}</Text>
            <ActionButton compact label={t("common.retry")} onPress={onRetry} />
          </View>
        ) : null}
        <Text style={styles.security}>{t("auth.security")}</Text>
      </View>
    </ScrollView>
  );
}

const styles = StyleSheet.create({
  page: { alignItems: "center", flexGrow: 1, padding: spacing[4] },
  content: { alignItems: "center", maxWidth: size.contentMax, paddingTop: spacing[12], width: "100%" },
  title: { color: colors.text, fontSize: 30, fontWeight: "600", letterSpacing: -0.6, lineHeight: 39, marginTop: spacing[6], textAlign: "center" },
  description: { color: colors.textMuted, fontSize: type.body, lineHeight: type.bodyLine, marginTop: spacing[2], maxWidth: 340, textAlign: "center" },
  status: { alignItems: "center", flexDirection: "row", gap: spacing[2], marginTop: spacing[8] },
  statusText: { color: colors.textMuted, fontSize: type.body },
  actions: { gap: spacing[2], marginTop: spacing[8], width: "100%" },
  unavailable: { color: colors.textMuted, fontSize: type.helper, lineHeight: type.helperLine, marginTop: spacing[2], textAlign: "center" },
  error: { alignItems: "center", backgroundColor: colors.surfaceSubtle, gap: spacing[2], marginTop: spacing[5], padding: spacing[4], width: "100%" },
  errorTitle: { color: colors.danger, fontSize: type.body, fontWeight: "600" },
  errorBody: { color: colors.textMuted, fontSize: type.helper, lineHeight: type.helperLine, textAlign: "center" },
  security: { color: colors.textMuted, fontSize: 12, lineHeight: 18, marginTop: spacing[8], maxWidth: 330, textAlign: "center" },
});
