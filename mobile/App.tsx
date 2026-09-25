import { StatusBar } from "expo-status-bar";
import { SafeAreaProvider } from "react-native-safe-area-context";

import CommerceApp from "./CommerceApp";
import { OpenAIChatScreen } from "./src/chat/OpenAIChatScreen";

export default function App() {
  const mode = process.env.EXPO_PUBLIC_VITLANE_DATA_MODE?.trim();
  if (mode && mode !== "openai") return <CommerceApp />;

  return (
    <SafeAreaProvider>
      <StatusBar style="dark" />
      <OpenAIChatScreen />
    </SafeAreaProvider>
  );
}
