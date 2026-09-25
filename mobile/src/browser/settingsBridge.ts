import { Platform } from "react-native";
import BrowserModule, { type BrowserSettingsRequest } from "../../modules/vitlane-browser/src/VitlaneBrowserModule";
import { loadSettings, saveSettings, type ChatSettings } from "../chat/storage";

/** Native dialog requests are handled by the same protected store as the chat settings. */
export function subscribeToBrowserSettings(onSaved: (settings: ChatSettings) => void): () => void {
  if (Platform.OS !== "android" || !BrowserModule?.addListener || !BrowserModule.finishSettingsRequest) return () => {};
  let active = true;
  let saving = false;
  const module = BrowserModule;
  async function reply(request: BrowserSettingsRequest, next: ChatSettings | null, error = "") {
    try {
      await module.finishSettingsRequest(request.requestId, next?.apiKey ?? "", next?.model ?? "",
        next?.browser?.maxSteps ?? 20, next?.browser?.timeoutSeconds ?? 300, error);
    } catch { /* The native screen may have closed; never log credential-bearing arguments. */ }
  }
  const subscription = module.addListener("onSettingsRequest", (request) => {
    void (async () => {
      if (!active) return;
      if (saving) { await reply(request, null, "설정을 저장 중입니다. 잠시 후 다시 시도해 주세요."); return; }
      saving = true;
      try {
        const current = await loadSettings();
        if (!active) return;
        const apiKey = request.apiKey.trim() || current?.apiKey || "";
        const model = request.model.trim();
        if (apiKey.length > 1024 || !/^sk-[A-Za-z0-9_-]+$/.test(apiKey)) {
          await reply(request, null, "sk-로 시작하는 OpenAI API 키를 입력해 주세요."); return;
        }
        if (!/^[a-zA-Z0-9][a-zA-Z0-9._:-]{0,199}$/.test(model)
          || !Number.isInteger(request.maxSteps) || request.maxSteps < 1 || request.maxSteps > 20
          || !Number.isInteger(request.timeoutSeconds) || request.timeoutSeconds < 30 || request.timeoutSeconds > 300) {
          await reply(request, null, "모델 이름, 실행 횟수와 제한 시간을 확인해 주세요."); return;
        }
        const next: ChatSettings = { apiKey, model,
          browser: { maxSteps: request.maxSteps, timeoutSeconds: request.timeoutSeconds } };
        await saveSettings(next);
        if (!active) return;
        onSaved(next);
        await reply(request, next);
      } catch {
        await reply(request, null, "설정을 안전하게 저장하지 못했습니다. 다시 시도해 주세요.");
      } finally { saving = false; }
    })();
  });
  return () => { active = false; subscription.remove(); };
}
