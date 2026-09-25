import type { PropsWithChildren, ReactNode } from "react";
import {
  KeyboardAvoidingView,
  Platform,
  Pressable,
  StyleSheet,
  Text,
  View,
} from "react-native";
import { SafeAreaView } from "react-native-safe-area-context";

import { useLocale } from "../i18n/LocaleProvider";
import { colors, radius, size, spacing, type } from "../theme/tokens";
import { DemoNotice } from "./DemoNotice";

type AppScaffoldProps = PropsWithChildren<{
  footer?: ReactNode;
  galleryOpen: boolean;
  onToggleGallery: () => void;
  mode?: "fixture" | "server";
  galleryEnabled?: boolean;
  accountActionLabel?: string;
  onAccountAction?: () => void;
}>;

export function AppScaffold({
  children,
  footer,
  galleryOpen,
  onToggleGallery,
  mode = "fixture",
  galleryEnabled = true,
  accountActionLabel,
  onAccountAction,
}: AppScaffoldProps) {
  const { locale, setLocale, t } = useLocale();
  return (
    <SafeAreaView edges={["top", "left", "right"]} style={styles.safeArea}>
      <KeyboardAvoidingView
        behavior={Platform.OS === "ios" ? "padding" : undefined}
        style={styles.keyboard}
      >
        <View style={styles.header}>
          <View style={styles.brandGroup}>
            <View accessibilityElementsHidden style={styles.laneMark}>
              <View style={styles.laneMarkInner} />
            </View>
            <Text style={styles.brand}>{t("app.brand")}</Text>
          </View>
          <View style={styles.headerActions}>
            {galleryEnabled ? (
              <Pressable
                accessibilityLabel={galleryOpen ? t("app.closeGallery") : t("app.openGallery")}
                accessibilityRole="button"
                hitSlop={8}
                onPress={onToggleGallery}
                style={({ pressed }) => [styles.headerButton, pressed && styles.pressed]}
              >
                <Text style={styles.headerButtonText}>{galleryOpen ? "×" : "▦"}</Text>
              </Pressable>
            ) : null}
            {accountActionLabel && onAccountAction ? (
              <Pressable
                accessibilityRole="button"
                onPress={onAccountAction}
                style={({ pressed }) => [styles.accountButton, pressed && styles.pressed]}
              >
                <Text style={styles.accountButtonText}>{accountActionLabel}</Text>
              </Pressable>
            ) : null}
            <Pressable
              accessibilityLabel={t("app.language")}
              accessibilityRole="button"
              hitSlop={8}
              onPress={() => setLocale(locale === "ko-KR" ? "en-US" : "ko-KR")}
              style={({ pressed }) => [styles.languageButton, pressed && styles.pressed]}
            >
              <Text style={styles.languageText}>{t("app.language")}</Text>
            </Pressable>
          </View>
        </View>
        <View style={styles.noticeHost}>
          <View style={styles.noticeInner}><DemoNotice mode={mode} /></View>
        </View>
        <View style={styles.body}>{children}</View>
        {footer ? <View style={styles.footer}>{footer}</View> : null}
      </KeyboardAvoidingView>
    </SafeAreaView>
  );
}

const styles = StyleSheet.create({
  safeArea: { backgroundColor: colors.surface, flex: 1 },
  keyboard: { flex: 1 },
  header: {
    alignItems: "center",
    backgroundColor: colors.surface,
    flexDirection: "row",
    justifyContent: "space-between",
    minHeight: size.header,
    paddingHorizontal: spacing[4],
  },
  brandGroup: { alignItems: "center", flexDirection: "row", gap: spacing[2] },
  laneMark: {
    alignItems: "center",
    backgroundColor: colors.surfaceSelected,
    borderRadius: radius.pill,
    height: 28,
    justifyContent: "center",
    width: 28,
  },
  laneMarkInner: {
    backgroundColor: colors.action,
    borderRadius: radius.pill,
    height: 16,
    transform: [{ rotate: "22deg" }],
    width: 5,
  },
  brand: { color: colors.text, fontSize: 18, fontWeight: "600", letterSpacing: -0.3 },
  headerActions: { alignItems: "center", flexDirection: "row", gap: spacing[1] },
  headerButton: {
    alignItems: "center",
    backgroundColor: colors.surfaceSubtle,
    borderRadius: radius.pill,
    height: size.touch,
    justifyContent: "center",
    width: size.touch,
  },
  headerButtonText: { color: colors.text, fontSize: 21, fontWeight: "500" },
  accountButton: {
    alignItems: "center",
    backgroundColor: colors.surfaceSubtle,
    borderRadius: radius.pill,
    justifyContent: "center",
    minHeight: size.touch,
    paddingHorizontal: spacing[3],
  },
  accountButtonText: { color: colors.text, fontSize: type.helper, fontWeight: "500" },
  languageButton: {
    alignItems: "center",
    backgroundColor: colors.surfaceSubtle,
    borderRadius: radius.pill,
    justifyContent: "center",
    minHeight: size.touch,
    paddingHorizontal: spacing[2],
  },
  languageText: { color: colors.textAccent, fontSize: type.helper, fontWeight: "500" },
  pressed: { opacity: 0.58 },
  noticeHost: {
    alignItems: "center",
    backgroundColor: colors.surface,
    paddingHorizontal: spacing[4],
    paddingBottom: spacing[2],
  },
  noticeInner: { maxWidth: size.contentMax, width: "100%" },
  body: { backgroundColor: colors.surface, flex: 1 },
  footer: {
    backgroundColor: colors.surface,
    borderTopColor: colors.border,
    borderTopWidth: StyleSheet.hairlineWidth,
  },
});
