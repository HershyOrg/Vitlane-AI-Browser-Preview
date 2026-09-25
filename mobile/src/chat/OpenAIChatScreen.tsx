import { useEffect, useRef, useState } from "react";
import * as Crypto from "expo-crypto";
import {
  ActivityIndicator,
  Alert,
  KeyboardAvoidingView,
  Modal,
  Platform,
  ScrollView,
  StyleSheet,
  Text,
  TextInput,
  View,
} from "react-native";
import { SafeAreaView } from "react-native-safe-area-context";

import { ActionButton } from "../components/ActionButton";
import { StatusNotice } from "../components/StatusNotice";
import { colors, radius, size, spacing, type } from "../theme/tokens";
import { openAgentBrowser } from "../browser/openBrowser";
import { subscribeToBrowserSettings } from "../browser/settingsBridge";
import { DEFAULT_MODEL, sendChat, type ChatMessage } from "./client";
import { clearSettings, loadSettings, saveSettings, type ChatSettings } from "./storage";

export function OpenAIChatScreen() {
  const [settings, setSettings] = useState<ChatSettings | null>(null);
  const [loading, setLoading] = useState(true);
  const [settingsOpen, setSettingsOpen] = useState(false);
  const [messages, setMessages] = useState<ChatMessage[]>([]);
  const [draft, setDraft] = useState("");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const [notice, setNotice] = useState("");
  const request = useRef<AbortController | null>(null);
  const scroll = useRef<ScrollView>(null);

  useEffect(() => subscribeToBrowserSettings((next) => {
    setSettings(next);
    setError("");
  }), []);

  useEffect(() => {
    let active = true;
    void loadSettings().then((saved) => {
      if (!active) return;
      setSettings(saved);
      setSettingsOpen(!saved);
    }).catch(() => {
      if (!active) return;
      setError("저장된 설정을 읽지 못했습니다. API 키를 다시 설정해 주세요.");
      setSettingsOpen(true);
    }).finally(() => {
      if (active) setLoading(false);
    });
    return () => {
      active = false;
      request.current?.abort();
      request.current = null;
    };
  }, []);

  async function send() {
    const content = draft.trim();
    if (!content || !settings || request.current) return;
    const controller = new AbortController();
    request.current = controller;
    const history = [...messages, { id: Crypto.randomUUID(), role: "user" as const, content }];
    setMessages(history);
    setDraft("");
    setError("");
    setNotice("");
    setBusy(true);
    try {
      const answer = await sendChat({ ...settings, messages: history, signal: controller.signal });
      if (request.current !== controller) return;
      setMessages([...history, { id: Crypto.randomUUID(), role: "assistant", content: answer.text }]);
      if (answer.incomplete) setNotice("응답이 일부만 생성되었습니다. 필요하면 내용을 이어서 요청해 주세요.");
    } catch (cause) {
      if (request.current !== controller) return;
      // Keep the prompt editable so retrying never duplicates a conversation turn.
      setMessages(messages);
      setDraft(content);
      if (controller.signal.aborted) {
        setNotice("응답 요청을 중지했습니다.");
      } else {
        setError(cause instanceof Error ? cause.message : "응답을 받지 못했습니다. 다시 시도해 주세요.");
      }
    } finally {
      if (request.current === controller) {
        request.current = null;
        setBusy(false);
      }
    }
  }

  function newChat() {
    if (busy || messages.length === 0) return;
    Alert.alert("새 대화를 시작할까요?", "현재 대화는 저장되지 않습니다.", [
      { text: "취소", style: "cancel" },
      { text: "새 대화", onPress: () => { setMessages([]); setError(""); setNotice(""); } },
    ]);
  }

  async function openBrowser() {
    setError("");
    try {
      await openAgentBrowser(settings);
    } catch (cause) {
      setError(cause instanceof Error ? cause.message : "브라우저를 열지 못했습니다.");
    }
  }

  if (loading) {
    return <SafeAreaView style={styles.loading}><ActivityIndicator accessibilityLabel="설정 불러오는 중" color={colors.action} /></SafeAreaView>;
  }

  return (
    <SafeAreaView style={styles.page}>
      <KeyboardAvoidingView style={styles.page} behavior={Platform.OS === "ios" ? "padding" : undefined}>
        <View style={styles.header}>
          <View style={styles.brand}>
            <Text accessibilityRole="header" style={styles.title}>Vitlane</Text>
            <Text numberOfLines={1} style={styles.caption}>{settings?.model ?? "나만의 AI 대화"}</Text>
          </View>
          <ActionButton compact label="새 대화" emphasis="quiet" disabled={busy || !messages.length} onPress={newChat} />
          <ActionButton compact label="브라우저" emphasis="primary" disabled={busy} onPress={() => { void openBrowser(); }} />
          <ActionButton compact label="설정" disabled={busy} onPress={() => setSettingsOpen(true)} />
        </View>
        <ScrollView
          ref={scroll}
          style={styles.page}
          contentContainerStyle={styles.conversation}
          keyboardShouldPersistTaps="handled"
          onContentSizeChange={() => scroll.current?.scrollToEnd({ animated: true })}
        >
          {!messages.length ? (
            <View style={styles.welcome}>
              <View style={styles.mark}><Text style={styles.markText}>V</Text></View>
              <Text accessibilityRole="header" style={styles.hero}>대화에서 웹 탐색까지</Text>
              <Text style={styles.welcomeText}>궁금한 것을 묻거나, 브라우저를 열어{"\n"}AI와 함께 상품을 찾아보세요.</Text>
              <ActionButton label="AI 브라우저 열기" emphasis="primary" onPress={() => { void openBrowser(); }} />
              <Text style={styles.footnote}>검색·이동·입력을 도와드려요. 로그인과 최종 결제는 직접 진행해 주세요.</Text>
              {!settings ? <ActionButton label="OpenAI API 키 설정" emphasis="primary" onPress={() => setSettingsOpen(true)} /> : null}
            </View>
          ) : messages.map((message) => (
            <View key={message.id} style={[styles.message, message.role === "user" ? styles.userMessage : styles.assistantMessage]}>
              <Text style={styles.speaker}>{message.role === "user" ? "나" : "Vitlane"}</Text>
              <Text selectable style={styles.messageText}>{message.content}</Text>
            </View>
          ))}
          {busy ? <View accessibilityLiveRegion="polite" style={styles.thinking}><ActivityIndicator color={colors.action} /><Text style={styles.caption}>답변을 준비하고 있어요…</Text></View> : null}
        </ScrollView>
        <View style={styles.composerArea}>
          {error ? <StatusNotice message={error} tone="danger" /> : null}
          {notice ? <StatusNotice message={notice} tone="warning" /> : null}
          <View style={styles.composer}>
            <TextInput
              accessibilityLabel="메시지"
              placeholder={settings ? "메시지를 입력하세요" : "먼저 API 키를 설정해 주세요"}
              placeholderTextColor={colors.textMuted}
              style={styles.messageInput}
              value={draft}
              onChangeText={setDraft}
              editable={Boolean(settings) && !busy}
              multiline
              maxLength={12000}
              textAlignVertical="top"
            />
            {busy ? (
              <ActionButton label="중지" onPress={() => request.current?.abort()} />
            ) : (
              <ActionButton label="보내기" emphasis="primary" disabled={!settings || !draft.trim()} onPress={() => { void send(); }} />
            )}
          </View>
          <Text style={styles.footnote}>대화는 OpenAI로 전송됩니다. 이 앱에는 대화 기록을 저장하지 않습니다.</Text>
        </View>
      </KeyboardAvoidingView>
      {settingsOpen ? (
        <SettingsSheet
          current={settings}
          onClose={() => setSettingsOpen(false)}
          onSaved={(next) => { setSettings(next); setSettingsOpen(false); setError(""); }}
          onRemoved={() => { setSettings(null); setMessages([]); setDraft(""); setError(""); setNotice(""); }}
        />
      ) : null}
    </SafeAreaView>
  );
}

function SettingsSheet({ current, onClose, onSaved, onRemoved }: {
  current: ChatSettings | null;
  onClose(): void;
  onSaved(settings: ChatSettings): void;
  onRemoved(): void;
}) {
  const [key, setKey] = useState("");
  const [model, setModel] = useState(current?.model ?? DEFAULT_MODEL);
  const [saving, setSaving] = useState(false);
  const [error, setError] = useState("");

  async function save() {
    if (saving) return;
    const apiKey = key.trim() || current?.apiKey || "";
    const nextModel = model.trim();
    if (!apiKey.startsWith("sk-") || /\s/.test(apiKey)) {
      setError("sk-로 시작하는 OpenAI API 키를 입력해 주세요.");
      return;
    }
    if (!/^[a-zA-Z0-9][a-zA-Z0-9._:-]{0,199}$/.test(nextModel)) {
      setError("사용할 모델 ID를 입력해 주세요. 예: gpt-5-mini");
      return;
    }
    setSaving(true);
    setError("");
    try {
      const next: ChatSettings = { ...current, apiKey, model: nextModel };
      await saveSettings(next);
      setKey("");
      onSaved(next);
    } catch {
      setError("기기에 API 키를 저장하지 못했습니다. 다시 시도해 주세요.");
    } finally {
      setSaving(false);
    }
  }

  async function remove() {
    setSaving(true);
    setError("");
    try {
      await clearSettings();
      setKey("");
      onRemoved();
      onClose();
    } catch {
      setError("API 키를 삭제하지 못했습니다. 다시 시도해 주세요.");
    } finally {
      setSaving(false);
    }
  }

  return (
    <Modal animationType="slide" presentationStyle="pageSheet" onRequestClose={() => { if (!saving) onClose(); }}>
      <SafeAreaView style={styles.page}>
        <KeyboardAvoidingView style={styles.page} behavior={Platform.OS === "ios" ? "padding" : "height"}>
          <View style={styles.header}>
            <Text accessibilityRole="header" style={[styles.title, styles.brand]}>OpenAI 연결</Text>
            <ActionButton label="닫기" compact emphasis="quiet" disabled={saving} onPress={onClose} />
          </View>
          <ScrollView contentContainerStyle={styles.settings} keyboardShouldPersistTaps="handled">
            <Text style={styles.messageText}>본인의 OpenAI API 키로 대화합니다. 별도 Vitlane 계정이나 서버 주소는 필요하지 않습니다.</Text>
            <Text style={styles.label}>API 키</Text>
            {current ? <Text style={styles.caption}>키가 저장되어 있습니다. 변경할 때만 새 키를 입력하세요.</Text> : null}
            <TextInput
              accessibilityLabel="OpenAI API 키"
              placeholder={current ? "저장된 키 유지" : "sk-…"}
              placeholderTextColor={colors.textMuted}
              value={key}
              onChangeText={setKey}
              secureTextEntry
              autoCapitalize="none"
              autoCorrect={false}
              autoComplete="off"
              importantForAutofill="no"
              editable={!saving}
              maxLength={1024}
              style={styles.settingsInput}
            />
            <Text style={styles.caption}>키는 기기의 보안 저장소에 보관합니다. API 사용 요금은 입력한 키의 OpenAI 계정에 청구됩니다.</Text>
            <Text style={styles.label}>모델</Text>
            <TextInput accessibilityLabel="모델 ID" value={model} onChangeText={setModel} autoCapitalize="none" autoCorrect={false} editable={!saving} maxLength={200} style={styles.settingsInput} />
            <Text style={styles.caption}>기본 모델은 {DEFAULT_MODEL}입니다. 계정에서 사용할 수 있는 Responses API 모델 ID로 변경할 수 있습니다.</Text>
            {error ? <StatusNotice message={error} tone="danger" /> : null}
            <ActionButton label="저장하고 대화하기" emphasis="primary" busy={saving} onPress={() => { void save(); }} />
            {current ? <ActionButton label="저장된 API 키 삭제" emphasis="danger" disabled={saving} onPress={() => Alert.alert("API 키를 삭제할까요?", "저장된 키와 현재 대화가 이 앱에서 지워집니다.", [{ text: "취소", style: "cancel" }, { text: "삭제", style: "destructive", onPress: () => { void remove(); } }])} /> : null}
          </ScrollView>
        </KeyboardAvoidingView>
      </SafeAreaView>
    </Modal>
  );
}

const styles = StyleSheet.create({
  page: { flex: 1, backgroundColor: colors.canvas },
  loading: { flex: 1, alignItems: "center", justifyContent: "center", backgroundColor: colors.canvas },
  header: { paddingHorizontal: spacing[4], paddingVertical: spacing[3], flexDirection: "row", alignItems: "center", gap: spacing[2], borderBottomWidth: 1, borderBottomColor: colors.border },
  brand: { flex: 1 },
  title: { fontSize: type.heading, lineHeight: type.headingLine, fontWeight: "700", color: colors.text },
  caption: { fontSize: type.helper, lineHeight: type.helperLine, color: colors.textMuted },
  conversation: { padding: spacing[4], gap: spacing[4], flexGrow: 1, width: "100%", maxWidth: 760, alignSelf: "center" },
  welcome: { flex: 1, alignItems: "center", justifyContent: "center", gap: spacing[5], paddingVertical: spacing[12] },
  mark: { width: 64, height: 64, alignItems: "center", justifyContent: "center", borderRadius: 22, backgroundColor: colors.surfaceSelected },
  markText: { fontSize: 34, fontWeight: "700", color: colors.action },
  hero: { fontSize: type.hero, lineHeight: type.heroLine, fontWeight: "600", color: colors.text },
  welcomeText: { fontSize: type.body, lineHeight: type.bodyLine, textAlign: "center", color: colors.textMuted },
  message: { padding: spacing[4], borderRadius: radius.overlay, gap: spacing[2], maxWidth: "94%" },
  userMessage: { alignSelf: "flex-end", backgroundColor: colors.surfaceSelected },
  assistantMessage: { alignSelf: "flex-start", backgroundColor: colors.surface, borderWidth: 1, borderColor: colors.border },
  speaker: { fontSize: type.helper, lineHeight: type.helperLine, fontWeight: "600", color: colors.textAccent },
  messageText: { fontSize: type.body, lineHeight: type.bodyLine, color: colors.text },
  thinking: { flexDirection: "row", gap: spacing[3], alignItems: "center", padding: spacing[3] },
  composerArea: { paddingHorizontal: spacing[4], paddingBottom: spacing[2], gap: spacing[2], borderTopWidth: 1, borderTopColor: colors.border },
  composer: { flexDirection: "row", alignItems: "flex-end", gap: spacing[2], paddingTop: spacing[3] },
  messageInput: { flex: 1, fontSize: type.body, lineHeight: type.bodyLine, color: colors.text, backgroundColor: colors.surface, minHeight: size.primary, maxHeight: 156, borderWidth: 1, borderColor: colors.border, borderRadius: radius.overlay, padding: spacing[3] },
  footnote: { fontSize: 11, lineHeight: 16, color: colors.textMuted, textAlign: "center" },
  settings: { padding: spacing[6], gap: spacing[4], width: "100%", maxWidth: size.contentMax, alignSelf: "center" },
  label: { fontSize: type.body, lineHeight: type.bodyLine, fontWeight: "600", color: colors.text, marginTop: spacing[3] },
  settingsInput: { minHeight: size.primary, padding: spacing[3], borderWidth: 1, borderColor: colors.borderStrong, borderRadius: radius.control, backgroundColor: colors.surface, color: colors.text, fontSize: type.body },
});
