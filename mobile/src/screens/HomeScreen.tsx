import { Pressable, ScrollView, StyleSheet, Text, View } from "react-native";

import { AgentAvatar } from "../components/AgentAvatar";
import { IntentComposer } from "../components/IntentComposer";
import { useLocale } from "../i18n/LocaleProvider";
import { colors, radius, size, spacing, type } from "../theme/tokens";

type HomeScreenProps = {
  intent: string;
  onIntentChange: (value: string) => void;
  onSubmit: () => void;
  busy?: boolean;
  error?: string;
};

export function HomeScreen({
  intent,
  onIntentChange,
  onSubmit,
  busy = false,
  error,
}: HomeScreenProps) {
  const { t } = useLocale();
  const examples = [
    {
      id: "party",
      label: t("home.exampleParty"),
      value: t("home.examplePartyValue"),
    },
  ];
  return (
    <ScrollView
      contentContainerStyle={styles.page}
      keyboardShouldPersistTaps="handled"
    >
      <View style={styles.content}>
        <View style={styles.agentIntro}>
          <AgentAvatar active size={88} />
          <Text style={styles.agentName}>{t("agent.name")}</Text>
          <View style={styles.statusRow}>
            <View style={styles.statusDot} />
            <Text style={styles.status}>{t("home.agentStatus")}</Text>
          </View>
        </View>
        <Text accessibilityRole="header" style={styles.title}>{t("home.title")}</Text>
        <Text style={styles.description}>{t("home.description")}</Text>
        <View style={styles.composerHost}>
          <IntentComposer
            busy={busy}
            error={error}
            onChange={onIntentChange}
            onSubmit={onSubmit}
            inputTestID="home.intent"
            submitTestID="home.send"
            value={intent}
          />
        </View>
        <View style={styles.ideaSection}>
          <Text style={styles.ideaHeading}>{t("home.ideas")}</Text>
          {examples.map((example) => (
            <Pressable
              accessibilityHint={t("home.exampleFillHint")}
              accessibilityRole="button"
              key={example.id}
              onPress={() => onIntentChange(example.value)}
              style={({ pressed }) => [styles.ideaCard, pressed && styles.pressed]}
              testID={`home.example.${example.id}`}
            >
              <View style={styles.ideaIcon}><Text style={styles.ideaIconText}>✦</Text></View>
              <View style={styles.ideaCopy}>
                <Text style={styles.ideaTitle}>{example.label}</Text>
                <Text style={styles.ideaDescription}>{t("home.examplePartyDescription")}</Text>
              </View>
              <Text style={styles.ideaArrow}>›</Text>
            </Pressable>
          ))}
        </View>
      </View>
    </ScrollView>
  );
}

const styles = StyleSheet.create({
  page: {
    alignItems: "center",
    flexGrow: 1,
    padding: spacing[4],
    paddingBottom: spacing[12],
  },
  content: { maxWidth: size.contentMax, paddingTop: spacing[6], width: "100%" },
  agentIntro: { alignItems: "center" },
  agentName: { color: colors.text, fontSize: 18, fontWeight: "600", lineHeight: 25, marginTop: spacing[3] },
  statusRow: { alignItems: "center", flexDirection: "row", gap: spacing[2], marginTop: spacing[1] },
  statusDot: { backgroundColor: colors.positive, borderRadius: radius.pill, height: 7, width: 7 },
  status: { color: colors.textMuted, fontSize: type.helper, lineHeight: type.helperLine },
  title: {
    color: colors.text,
    fontSize: 32,
    fontWeight: "600",
    letterSpacing: -0.8,
    lineHeight: 41,
    marginTop: spacing[8],
    textAlign: "center",
  },
  description: {
    color: colors.textMuted,
    fontSize: type.body,
    lineHeight: type.bodyLine,
    marginTop: spacing[2],
    paddingHorizontal: spacing[4],
    textAlign: "center",
  },
  composerHost: { marginTop: spacing[8] },
  ideaSection: { gap: spacing[2], marginTop: spacing[8] },
  ideaHeading: { color: colors.text, fontSize: type.heading, fontWeight: "600", lineHeight: type.headingLine },
  ideaCard: {
    alignItems: "center",
    backgroundColor: colors.surfaceSubtle,
    borderRadius: radius.overlay,
    flexDirection: "row",
    gap: spacing[3],
    justifyContent: "center",
    minHeight: 82,
    padding: spacing[3],
  },
  ideaIcon: { alignItems: "center", backgroundColor: colors.surfaceSelected, borderRadius: radius.overlay, height: 48, justifyContent: "center", width: 48 },
  ideaIconText: { color: colors.textAccent, fontSize: 20 },
  ideaCopy: { flex: 1 },
  ideaTitle: { color: colors.text, fontSize: type.body, fontWeight: "600", lineHeight: type.bodyLine },
  ideaDescription: { color: colors.textMuted, fontSize: type.helper, lineHeight: type.helperLine, marginTop: spacing[1] },
  ideaArrow: { color: colors.textMuted, fontSize: 26, lineHeight: 28 },
  pressed: { opacity: 0.58 },
});
