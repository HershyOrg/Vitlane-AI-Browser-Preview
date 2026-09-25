import { request } from "../../../shared/api/client";
import { browserPreferredLocale, readLocalePreference, type UILocale } from "../../../shared/i18n";

export type PreferenceFields = { uiLocale: UILocale; preferredCurrency: "KRW" | "USD"; researchCountry: "KR" | "US" };
export type UserPreferences = Partial<PreferenceFields> & { schemaVersion: "vitlane.user-preferences.v1"; version: number };
export type PreferencesResult = { preferences: UserPreferences; effective: UserPreferences & PreferenceFields };
/**
 * What an account without selections sees before its preferences load
 * (ADR-0080). Currency and country follow the browser language alone, so
 * switching the display language never moves them.
 */
export function firstPreferences(): PreferenceFields {
  const korean = browserPreferredLocale() === "ko-KR";
  return { uiLocale: readLocalePreference(), preferredCurrency: korean ? "KRW" : "USD", researchCountry: korean ? "KR" : "US" };
}
export const getPreferences = () => request<PreferencesResult>("/api/v1/me/preferences");
export const patchPreferences = (patch: Partial<PreferenceFields>, expectedVersion: number) => request<PreferencesResult>("/api/v1/me/preferences", {
  method: "PATCH", body: JSON.stringify({ schemaVersion: "vitlane.user-preferences.v1", expectedVersion, ...patch }),
});
