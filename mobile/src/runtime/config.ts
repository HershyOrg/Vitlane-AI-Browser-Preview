import { Platform } from "react-native";

import type { Currency } from "../domain";

type PublicEnvironment = {
  readonly dataMode?: string;
  readonly apiOrigin?: string;
  readonly country?: string;
  readonly city?: string;
  readonly currency?: string;
  readonly executionMode?: string;
  readonly modelKey?: string;
  readonly developmentLogin?: string;
  readonly developmentProfile?: string;
};

export type FixtureRuntimeConfig = {
  readonly dataMode: "fixture";
  readonly galleryEnabled: true;
};

export type ServerRuntimeConfig = {
  readonly dataMode: "server";
  readonly galleryEnabled: boolean;
  readonly apiOrigin: string;
  readonly country: "KR" | "US";
  readonly city?: string;
  readonly currency: Currency;
  readonly executionMode: "EXPERIMENT" | "LIVE";
  readonly modelKey?: string;
  readonly developmentLoginEnabled: boolean;
  readonly developmentProfile: string;
  readonly allowInsecureLocalhost: boolean;
  readonly localWebReviewEnabled: boolean;
};

export type MobileRuntimeConfig = FixtureRuntimeConfig | ServerRuntimeConfig;

export function readRuntimeConfig(
  environment: PublicEnvironment = expoPublicEnvironment(),
  options: { platform?: string; development?: boolean } = {},
): MobileRuntimeConfig {
  const development = options.development ?? __DEV__;
  const platform = options.platform ?? Platform.OS;
  const dataMode = normalized(environment.dataMode) || "server";
  if (dataMode === "fixture") {
    if (!development) {
      throw new Error("Fixture data mode is unavailable in release builds");
    }
    return { dataMode: "fixture", galleryEnabled: true };
  }
  if (dataMode !== "server") {
    throw new Error("EXPO_PUBLIC_VITLANE_DATA_MODE must be server or fixture");
  }

  const apiOrigin = normalizeOrigin(environment.apiOrigin, platform, development);
  const country = normalized(environment.country) || "KR";
  if (country !== "KR" && country !== "US") {
    throw new Error("EXPO_PUBLIC_VITLANE_COUNTRY must be KR or US");
  }
  const currency = normalized(environment.currency) || (country === "KR" ? "KRW" : "USD");
  if (currency !== "KRW" && currency !== "USD") {
    throw new Error("EXPO_PUBLIC_VITLANE_CURRENCY must be KRW or USD");
  }
  const executionMode = normalized(environment.executionMode) || "LIVE";
  if (executionMode !== "EXPERIMENT" && executionMode !== "LIVE") {
    throw new Error("EXPO_PUBLIC_VITLANE_EXECUTION_MODE must be LIVE or EXPERIMENT");
  }
  const city = normalized(environment.city);
  const modelKey = normalized(environment.modelKey);
  return {
    dataMode: "server",
    galleryEnabled: development,
    apiOrigin,
    country,
    currency,
    executionMode,
    developmentLoginEnabled: development && environment.developmentLogin === "true",
    developmentProfile: normalized(environment.developmentProfile) || "empty-user",
    allowInsecureLocalhost: development && apiOrigin.startsWith("http://"),
    localWebReviewEnabled: development
      && platform === "web"
      && environment.developmentLogin === "true"
      && isLoopbackOrigin(apiOrigin),
    ...(city ? { city } : {}),
    ...(modelKey ? { modelKey } : {}),
  };
}

function isLoopbackOrigin(value: string): boolean {
  if (!value) return false;
  const hostname = new URL(value).hostname;
  return ["localhost", "127.0.0.1", "[::1]"].includes(hostname);
}

function expoPublicEnvironment(): PublicEnvironment {
  return {
    dataMode: process.env.EXPO_PUBLIC_VITLANE_DATA_MODE,
    apiOrigin: process.env.EXPO_PUBLIC_VITLANE_API_ORIGIN,
    country: process.env.EXPO_PUBLIC_VITLANE_COUNTRY,
    city: process.env.EXPO_PUBLIC_VITLANE_CITY,
    currency: process.env.EXPO_PUBLIC_VITLANE_CURRENCY,
    executionMode: process.env.EXPO_PUBLIC_VITLANE_EXECUTION_MODE,
    modelKey: process.env.EXPO_PUBLIC_VITLANE_MODEL_KEY,
    developmentLogin: process.env.EXPO_PUBLIC_VITLANE_ENABLE_DEV_LOGIN,
    developmentProfile: process.env.EXPO_PUBLIC_VITLANE_DEV_PROFILE,
  };
}

function normalizeOrigin(
  value: string | undefined,
  platform: string,
  development: boolean,
): string {
  const normalizedValue = normalized(value);
  if (!normalizedValue) {
    if (platform === "web") return "";
    throw new Error("EXPO_PUBLIC_VITLANE_API_ORIGIN is required for native server mode");
  }
  const parsed = new URL(normalizedValue);
  if (
    !["http:", "https:"].includes(parsed.protocol)
    || parsed.username
    || parsed.password
    || parsed.pathname !== "/"
    || parsed.search
    || parsed.hash
  ) {
    throw new Error("EXPO_PUBLIC_VITLANE_API_ORIGIN must be an HTTP(S) origin");
  }
  const localDevelopmentOrigin = development
    && parsed.protocol === "http:"
    && ["localhost", "127.0.0.1", "[::1]"].includes(parsed.hostname);
  if (parsed.protocol !== "https:" && !localDevelopmentOrigin) {
    throw new Error("EXPO_PUBLIC_VITLANE_API_ORIGIN must use HTTPS outside local development");
  }
  return normalizedValue.replace(/\/+$/, "");
}

function normalized(value: string | undefined): string {
  return value?.trim() ?? "";
}
