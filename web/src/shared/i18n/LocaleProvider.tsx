import {
  createContext,
  type ReactNode,
  useContext,
  useLayoutEffect,
  useMemo,
  useState,
} from "react";
import {
  englishMessages,
  formatMessage,
  koreanMessages,
  type MessageKey,
  type MessageValues,
} from "./catalog";

export type UILocale = "en-US" | "ko-KR";

type LocaleContextValue = {
  locale: UILocale;
  setLocale: (locale: UILocale) => void;
  applyLocale: (locale: UILocale) => void;
  t: (key: MessageKey, values?: MessageValues) => string;
  l: (english: string, korean: string, values?: MessageValues) => string;
};

export type Localize = LocaleContextValue["l"];

// ADR-0080: Marketing and App share both cookies under .vitlane.com. The
// legacy vt_locale cookie was rewritten by every page view and is not read.
const localeChoiceCookie = "vt_locale_choice";
const localeSeenCookie = "vt_locale_seen";
const localeStorageKey = "vitlane.locale.v2";
export const localeSelectedEvent = "vitlane:locale-selected";
const standaloneContext: LocaleContextValue = {
  locale: "ko-KR",
  setLocale: () => undefined,
  applyLocale: () => undefined,
  t: (key, values) => formatMessage(koreanMessages[key], values),
  l: (english, korean, values) => formatMessage(korean, values),
};
const LocaleContext = createContext<LocaleContextValue>(standaloneContext);

export function LocaleProvider({ children }: { children: ReactNode }) {
  const [locale, setLocaleState] = useState<UILocale>(readLocalePreference);

  useLayoutEffect(() => {
    // A selection kept only in this origin's storage is still explicit; share it.
    const stored = readStoredLocale();
    if (stored && !readCookieLocale(localeChoiceCookie)) writeLocaleCookie(localeChoiceCookie, stored);
  }, []);

  useLayoutEffect(() => {
    document.documentElement.lang = locale === "ko-KR" ? "ko" : "en";
    writeLocaleCookie(localeSeenCookie, locale);
  }, [locale]);

  const value = useMemo<LocaleContextValue>(() => {
    const messages = locale === "ko-KR" ? koreanMessages : englishMessages;
    return {
      locale,
      applyLocale: setLocaleState,
      setLocale: (selected) => {
        setLocaleState(selected);
        rememberLocaleChoice(selected);
        const event = new CustomEvent(localeSelectedEvent, { detail: selected, cancelable: true });
        window.dispatchEvent(event);
        if (!event.defaultPrevented) {
          try { window.localStorage.setItem(localeStorageKey, selected); } catch { /* Browser storage may be unavailable. */ }
        }
      },
      t: (key, values) => formatMessage(messages[key], values),
      l: (english, korean, values) => formatMessage(
        locale === "ko-KR" ? korean : english,
        values,
      ),
    };
  }, [locale]);

  return <LocaleContext.Provider value={value}>{children}</LocaleContext.Provider>;
}

export function useLocale() {
  return useContext(LocaleContext);
}

/**
 * ADR-0080 order: an explicit choice in this browser, then the language this
 * browser last displayed, then the browser language, otherwise English.
 * Account selections arrive later through PreferencesProvider.
 */
export function readLocalePreference(): UILocale {
  // Non-browser callers have no user preference to inspect. Keep the legacy
  // Korean fallback for pure domain tests and tools.
  if (typeof document === "undefined") return "ko-KR";
  return readCookieLocale(localeChoiceCookie)
    ?? readStoredLocale()
    ?? readCookieLocale(localeSeenCookie)
    ?? browserPreferredLocale();
}

/** Korean when the browser lists Korean before English, otherwise English. */
export function browserPreferredLocale(): UILocale {
  if (typeof document === "undefined") return "ko-KR";
  const languages = navigator.languages?.length ? navigator.languages : [navigator.language];
  for (const language of languages) {
    const primary = language?.toLowerCase().split("-")[0];
    if (primary === "ko") return "ko-KR";
    if (primary === "en") return "en-US";
  }
  return "en-US";
}

/** Records an explicit selection, including one stored on the account. */
export function rememberLocaleChoice(locale: UILocale) {
  writeLocaleCookie(localeChoiceCookie, locale);
}

/** Deterministic selection for non-React domain and infrastructure code. */
export function localizeFixedCopy(
  english: string,
  korean: string,
  values?: MessageValues,
) {
  return formatMessage(
    readLocalePreference() === "ko-KR" ? korean : english,
    values,
  );
}

/** Marks protocol, evidence, or persisted content that must not vary by UI mode. */
export function invariantContent<const Value extends string>(value: Value): Value {
  return value;
}

function readCookieLocale(name: string): UILocale | undefined {
  const value = document.cookie
    .split(";")
    .map((part) => part.trim())
    .find((part) => part.startsWith(`${name}=`))
    ?.slice(name.length + 1);
  return value === "en-US" || value === "ko-KR" ? value : undefined;
}

function readStoredLocale(): UILocale | undefined {
  try {
    const stored = window.localStorage.getItem(localeStorageKey);
    return stored === "en-US" || stored === "ko-KR" ? stored : undefined;
  } catch {
    return undefined;
  }
}

function writeLocaleCookie(name: string, locale: UILocale) {
  const sharedDomain = /(^|\.)vitlane\.com$/i.test(window.location.hostname)
    ? "; Domain=.vitlane.com"
    : "";
  document.cookie = `${name}=${locale}; Path=/; Max-Age=31536000; SameSite=Lax${sharedDomain}`;
}
