import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import * as Crypto from "expo-crypto";
import { StatusBar } from "expo-status-bar";
import { Platform } from "react-native";
import { SafeAreaProvider } from "react-native-safe-area-context";

import { MobileAuthClient, useAuthSession } from "./src/auth";
import { VitlaneApiError } from "./src/api";
import type { CommerceGateway } from "./src/commerce";
import { AppScaffold } from "./src/components/AppScaffold";
import { FixtureCommerceAdapter, ServerCommerceAdapter } from "./src/infra";
import { LocaleProvider, useLocale } from "./src/i18n/LocaleProvider";
import {
  readRuntimeConfig,
  type MobileRuntimeConfig,
  type ServerRuntimeConfig,
} from "./src/runtime";
import { ComponentGallery } from "./src/screens/ComponentGallery";
import { HomeScreen } from "./src/screens/HomeScreen";
import { RuntimeErrorScreen } from "./src/screens/RuntimeErrorScreen";
import { SignInScreen } from "./src/screens/SignInScreen";
import { WorkspaceScreen } from "./src/screens/WorkspaceScreen";
import {
  AsyncWorkspaceRecoveryStore,
  MemoryWorkspaceRecoveryStore,
  type WorkspaceRecoveryStore,
  useWorkspace,
} from "./src/workspace";

export default function App() {
  return (
    <SafeAreaProvider>
      <LocaleProvider>
        <StatusBar style="dark" />
        <RuntimeBoundary />
      </LocaleProvider>
    </SafeAreaProvider>
  );
}

function RuntimeBoundary() {
  let config: MobileRuntimeConfig;
  try {
    config = readRuntimeConfig();
  } catch (cause) {
    return <ConfigurationFailure detail={message(cause)} />;
  }
  if (config.dataMode === "fixture") return <FixtureApplication />;
  if (Platform.OS === "web" && !config.localWebReviewEnabled) {
    return (
      <ConfigurationFailure detail="Web server mode is available only for an explicit local review session." />
    );
  }
  return <ServerApplication config={config} />;
}

function FixtureApplication() {
  const gateway = useMemo(() => new FixtureCommerceAdapter(), []);
  const recoveryStore = useMemo(() => new MemoryWorkspaceRecoveryStore(), []);
  return (
    <WorkspaceApplication
      galleryEnabled
      gateway={gateway}
      mode="fixture"
      recoveryStore={recoveryStore}
    />
  );
}

function ServerApplication({ config }: { config: ServerRuntimeConfig }) {
  const client = useMemo(() => new MobileAuthClient({
    baseUrl: config.apiOrigin,
    developmentSessionsEnabled: config.developmentLoginEnabled,
    allowInsecureLocalhost: config.allowInsecureLocalhost,
  }), [config.allowInsecureLocalhost, config.apiOrigin, config.developmentLoginEnabled]);
  const auth = useAuthSession(client);
  const localReviewLoginAttempted = useRef(false);
  useEffect(() => {
    if (
      localReviewLoginAttempted.current
      || !config.localWebReviewEnabled
      || auth.status !== "signed_out"
      || auth.busy
      || !auth.capabilities?.localReviewEnabled
    ) return;
    localReviewLoginAttempted.current = true;
    void auth.signInWithDevelopmentProfile(config.developmentProfile);
  }, [
    auth.busy,
    auth.capabilities?.localReviewEnabled,
    auth.signInWithDevelopmentProfile,
    auth.status,
    config.developmentProfile,
    config.localWebReviewEnabled,
  ]);
  const handleWorkspaceError = useCallback((cause: unknown) => {
    if (cause instanceof VitlaneApiError && cause.status === 401) {
      void auth.restore();
    }
  }, [auth.restore]);

  if (auth.status !== "signed_in" || !auth.session) {
    return (
      <AppScaffold
        galleryEnabled={false}
        galleryOpen={false}
        mode="server"
        onToggleGallery={() => undefined}
      >
        <SignInScreen
          busy={auth.busy}
          developmentEnabled={Boolean(
            config.developmentLoginEnabled && auth.capabilities?.localReviewEnabled,
          )}
          error={auth.error}
          googleEnabled={Boolean(
            client.isSupported() && auth.capabilities?.mobileGoogleEnabled,
          )}
          onDevelopment={() => {
            void auth.signInWithDevelopmentProfile(config.developmentProfile);
          }}
          onGoogle={() => { void auth.signInWithGoogle(); }}
          onRetry={() => { void auth.restore(); }}
          restoring={auth.status === "restoring"}
        />
      </AppScaffold>
    );
  }

  return (
    <AuthenticatedWorkspace
      client={client}
      config={config}
      onError={handleWorkspaceError}
      onSignOut={() => { void auth.signOut(); }}
      userId={auth.session.user.id}
    />
  );
}

function AuthenticatedWorkspace({
  client,
  config,
  userId,
  onError,
  onSignOut,
}: {
  client: MobileAuthClient;
  config: ServerRuntimeConfig;
  userId: string;
  onError: (cause: unknown) => void;
  onSignOut: () => void;
}) {
  const commandIdFactory = useMemo(() => () => Crypto.randomUUID(), []);
  const gateway = useMemo(() => new ServerCommerceAdapter({
    api: client.getApiClient(),
    country: config.country,
    currency: config.currency,
    executionMode: config.executionMode,
    idFactory: commandIdFactory,
    ...(config.city ? { city: config.city } : {}),
    ...(config.modelKey ? { modelKey: config.modelKey } : {}),
  }), [
    client,
    commandIdFactory,
    config.city,
    config.country,
    config.currency,
    config.executionMode,
    config.modelKey,
  ]);
  const recoveryStore = useMemo(
    () => new AsyncWorkspaceRecoveryStore(userId),
    [userId],
  );
  return (
    <WorkspaceApplication
      accountAction={onSignOut}
      commandIdFactory={commandIdFactory}
      galleryEnabled={config.galleryEnabled}
      gateway={gateway}
      mode="server"
      onError={onError}
      recoveryStore={recoveryStore}
    />
  );
}

function WorkspaceApplication({
  gateway,
  recoveryStore,
  mode,
  galleryEnabled,
  commandIdFactory,
  accountAction,
  onError,
}: {
  gateway: CommerceGateway;
  recoveryStore: WorkspaceRecoveryStore;
  mode: "fixture" | "server";
  galleryEnabled: boolean;
  commandIdFactory?: () => string;
  accountAction?: () => void;
  onError?: (cause: unknown) => void;
}) {
  const { t } = useLocale();
  const flow = useWorkspace({
    gateway,
    recoveryStore,
    ...(commandIdFactory ? { commandIdFactory } : {}),
    ...(onError ? { onError } : {}),
  });
  const [intent, setIntent] = useState("");
  const [galleryOpen, setGalleryOpen] = useState(false);
  const [homeError, setHomeError] = useState<string>();

  const submitIntent = async () => {
    if (!intent.trim()) {
      setHomeError(t("home.emptyError"));
      return;
    }
    setHomeError(undefined);
    await flow.createWorkspace(intent);
  };

  return (
    <AppScaffold
      accountActionLabel={accountAction ? t("auth.signOut") : undefined}
      galleryEnabled={galleryEnabled}
      galleryOpen={galleryOpen}
      mode={mode}
      onAccountAction={accountAction}
      onToggleGallery={() => setGalleryOpen((value) => !value)}
    >
      {galleryOpen ? (
        <ComponentGallery />
      ) : flow.workspace ? (
        <WorkspaceScreen
          busy={flow.busy}
          error={flow.error}
          onAnswerQuestion={flow.answerQuestion}
          onApplyBudget={flow.applyBudgetPreview}
          onClearError={flow.clearError}
          onPreviewBudget={flow.previewBudget}
          onRefresh={flow.refresh}
          onStartBrowserRun={flow.startBrowserRun}
          onRespondToAgentMessage={flow.respondToAgentMessage}
          onCancelResearchSubscription={flow.cancelResearchSubscription}
          onHideResearchFinding={flow.hideResearchFinding}
          onImportResearchFinding={flow.importResearchFinding}
          onSubmitFollowUp={flow.submitFollowUp}
          onUpdateShoppingPreferences={flow.updateShoppingPreferences}
          workspace={flow.workspace}
        />
      ) : (
        <HomeScreen
          busy={flow.busy || flow.restoring}
          error={homeError ?? flow.error ?? undefined}
          intent={intent}
          onIntentChange={(value) => {
            setIntent(value);
            if (homeError) setHomeError(undefined);
          }}
          onSubmit={submitIntent}
        />
      )}
    </AppScaffold>
  );
}

function ConfigurationFailure({ detail }: { detail: string }) {
  return (
    <AppScaffold
      galleryEnabled={false}
      galleryOpen={false}
      mode="server"
      onToggleGallery={() => undefined}
    >
      <RuntimeErrorScreen detail={detail} />
    </AppScaffold>
  );
}

function message(cause: unknown): string {
  return cause instanceof Error ? cause.message : "Invalid mobile runtime configuration";
}
