import { NativeModule, requireOptionalNativeModule } from 'expo';

export type BrowserSettingsRequest = {
  requestId: string;
  apiKey: string;
  model: string;
  maxSteps: number;
  timeoutSeconds: number;
};

/** Android launches a native WebView activity; iOS retains the scoped native view API. */
declare class VitlaneBrowserModule extends NativeModule<{
  onSettingsRequest: (request: BrowserSettingsRequest) => void;
}> {
  openAgentBrowser(apiKey: string, model: string, maxSteps: number, timeoutSeconds: number): Promise<void>;
  finishSettingsRequest(requestId: string, apiKey: string, model: string, maxSteps: number, timeoutSeconds: number, error: string): Promise<void>;
}

export default requireOptionalNativeModule<VitlaneBrowserModule>('VitlaneBrowser');
