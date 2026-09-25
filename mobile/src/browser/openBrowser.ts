import { Platform } from "react-native";
import BrowserModule from "../../modules/vitlane-browser/src/VitlaneBrowserModule";
import { DEFAULT_MODEL } from "../chat/client";
import type { ChatSettings } from "../chat/storage";

export async function openAgentBrowser(settings: ChatSettings | null): Promise<void> {
  if (Platform.OS !== "android") {
    throw new Error("AI 브라우저는 Android 앱에서 사용할 수 있습니다.");
  }
  if (!BrowserModule?.openAgentBrowser) {
    throw new Error("브라우저 기능이 포함된 최신 APK를 설치해 주세요.");
  }
  await BrowserModule.openAgentBrowser(settings?.apiKey ?? "", settings?.model ?? DEFAULT_MODEL,
    settings?.browser?.maxSteps ?? 20, settings?.browser?.timeoutSeconds ?? 300);
}
