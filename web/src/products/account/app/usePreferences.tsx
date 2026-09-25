import { createContext, useCallback, useContext, useEffect, useRef, useState, type ReactNode } from "react";
import { Button } from "../../../shared/ui";
import { APIError } from "../../../shared/api/client";
import { localeSelectedEvent, readLocalePreference, rememberLocaleChoice, useLocale, type UILocale } from "../../../shared/i18n";
import { firstPreferences, getPreferences, patchPreferences, type PreferenceFields, type PreferencesResult } from "../infra/preferencesApi";
import { useCurrentUser } from "./useCurrentUser";

type PreferencesState = { values: PreferenceFields; ready: boolean; storageScope?: string; save: (patch: Partial<PreferenceFields>) => Promise<void>; refresh: () => Promise<void> };
const PreferencesContext = createContext<PreferencesState>({ get values() { return firstPreferences(); }, ready: true, save: async () => {}, refresh: async () => {} });

// An account selection is explicit: Marketing and later guest visits in this browser follow it.
function applyAccountLocale(result: PreferencesResult, applyLocale: (locale: UILocale) => void) {
  if (result.preferences.uiLocale) rememberLocaleChoice(result.preferences.uiLocale);
  applyLocale(result.effective.uiLocale);
}

export function PreferencesProvider({ children }: { children: ReactNode }) {
  const { user } = useCurrentUser();
  const userID = user?.id;
  const { applyLocale, l } = useLocale();
  const [state, setState] = useState<{ userID?: string; data: PreferencesResult } | null>(null);
  const [error, setError] = useState(false);
  const identity = useRef(userID);
  identity.current = userID;
  const data = useRef<PreferencesResult | null>(null);
  const generation = useRef(0);
  const queue = useRef<Promise<void>>(Promise.resolve());

  const refresh = useCallback(async () => {
    if (!userID) return;
    const ticket = generation.current;
    const result = await getPreferences();
    if (identity.current !== userID || ticket !== generation.current) return;
    if (data.current && data.current.preferences.version > result.preferences.version) return;
    data.current = result;
    setState({ userID, data: result });
    applyAccountLocale(result, applyLocale);
    setError(false);
  }, [userID, applyLocale]);

  useEffect(() => {
    generation.current += 1;
    data.current = null;
    setState(null);
    setError(false);
    queue.current = Promise.resolve();
    if (userID) void refresh().catch(() => { if (identity.current === userID) setError(true); });
    else applyLocale(readLocalePreference());
    return () => { generation.current += 1; };
  }, [userID, refresh, applyLocale]);

  const save = useCallback((patch: Partial<PreferenceFields>) => {
    const ticket = generation.current;
    const current = () => Boolean(userID && identity.current === userID && ticket === generation.current);
    const next = queue.current.catch(() => {}).then(async () => {
      if (!current()) return;
      let stored = data.current ?? await getPreferences();
      if (!current()) return;
      let result: PreferencesResult;
      try { result = await patchPreferences(patch, stored.preferences.version); }
      catch (caught) {
        if (!(caught instanceof APIError) || caught.status !== 409 || !current()) throw caught;
        stored = await getPreferences();
        if (!current()) return;
        result = await patchPreferences(patch, stored.preferences.version);
      }
      if (!current()) return;
      // A later refresh cannot replace this result with an older preference version.
      data.current = result;
      setState({ userID, data: result });
      if (patch.uiLocale) applyAccountLocale(result, applyLocale);
      setError(false);
    });
    queue.current = next;
    return next.catch((caught) => { if (current()) setError(true); throw caught; });
  }, [userID, applyLocale]);

  useEffect(() => {
    if (!userID) return;
    const selected = (event: Event) => {
      event.preventDefault();
      const locale = (event as CustomEvent<UILocale>).detail;
      void save({ uiLocale: locale }).catch(() => {});
    };
    window.addEventListener(localeSelectedEvent, selected);
    return () => window.removeEventListener(localeSelectedEvent, selected);
  }, [userID, save]);

  const active = state?.userID === userID ? state?.data : null;
  return <PreferencesContext.Provider value={{ values: active?.effective ?? firstPreferences(), ready: !userID || Boolean(active), storageScope: userID, save, refresh }}>
    {children}
    {error && <aside className="shell-preferences-alert" role="alert">
      <span>{l("Your last settings could not be saved or loaded.", "마지막 설정을 저장하거나 불러오지 못했습니다.")}</span>
      <Button type="button" emphasis="quiet" size="compact" onClick={() => void refresh().catch(() => setError(true))}>{l("Reload settings", "설정 다시 불러오기")}</Button>
    </aside>}
  </PreferencesContext.Provider>;
}

export const usePreferences = () => useContext(PreferencesContext);
