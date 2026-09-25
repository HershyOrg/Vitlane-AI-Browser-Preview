// @vitest-environment jsdom
import { act } from "react";
import { createRoot } from "react-dom/client";
import { afterEach, describe, expect, it } from "vitest";
import { englishMessages, koreanMessages } from "./catalog";
import {
  browserPreferredLocale,
  LocaleProvider,
  localizeFixedCopy,
  readLocalePreference,
  useLocale,
} from "./LocaleProvider";

const browser = (languages: string[]) => Object.defineProperty(window.navigator, "languages", {
  configurable: true,
  get: () => languages,
});

afterEach(() => {
  for (const name of ["vt_locale", "vt_locale_choice", "vt_locale_seen"]) {
    document.cookie = `${name}=; Path=/; Max-Age=0`;
  }
  window.localStorage.clear();
  document.body.innerHTML = "";
});

describe("locale contract", () => {
  it("keeps an exact English/Korean key set", () => {
    expect(Object.keys(koreanMessages).sort()).toEqual(Object.keys(englishMessages).sort());
    expect(Object.values(englishMessages).every((value) => !/[가-힣]/u.test(value))).toBe(true);
  });

  it("follows the browser language when nothing was chosen or seen", () => {
    browser(["ko-KR", "ko", "en-US", "en"]);
    expect(readLocalePreference()).toBe("ko-KR");
    browser(["en-US", "en", "ko"]);
    expect(readLocalePreference()).toBe("en-US");
    browser(["ja-JP", "ko"]);
    expect(browserPreferredLocale()).toBe("ko-KR");
    browser(["fr-FR", "de"]);
    expect(readLocalePreference()).toBe("en-US");
  });

  it("ignores the legacy cookie that every page view used to rewrite", () => {
    browser(["ko-KR"]);
    document.cookie = "vt_locale=en-US; Path=/";
    expect(readLocalePreference()).toBe("ko-KR");
  });

  it("prefers an explicit choice, then stored choice, then the last seen language", () => {
    browser(["ko-KR"]);
    document.cookie = "vt_locale_seen=en-US; Path=/";
    expect(readLocalePreference()).toBe("en-US");
    window.localStorage.setItem("vitlane.locale.v2", "ko-KR");
    expect(readLocalePreference()).toBe("ko-KR");
    document.cookie = "vt_locale_choice=en-US; Path=/";
    expect(readLocalePreference()).toBe("en-US");
  });

  it("selects the exact paired copy from the explicit locale", () => {
    document.cookie = "vt_locale_choice=en-US; Path=/";
    expect(localizeFixedCopy("Create curation", "새 큐레이션")).toBe("Create curation");
    document.cookie = "vt_locale_choice=ko-KR; Path=/";
    expect(localizeFixedCopy("Create curation", "새 큐레이션")).toBe("새 큐레이션");
  });

  it("records what it shows as seen, a selection as chosen, and shares a stored choice", async () => {
    browser(["en-US"]);
    window.localStorage.setItem("vitlane.locale.v2", "ko-KR");
    let selectEnglish = () => {};
    function Probe() {
      const { locale, setLocale } = useLocale();
      selectEnglish = () => setLocale("en-US");
      return <output>{locale}</output>;
    }
    const host = document.createElement("div");
    document.body.append(host);
    const root = createRoot(host);
    await act(async () => root.render(<LocaleProvider><Probe /></LocaleProvider>));
    expect(host.textContent).toBe("ko-KR");
    expect(document.cookie).toContain("vt_locale_seen=ko-KR");
    expect(document.cookie).toContain("vt_locale_choice=ko-KR");

    await act(async () => selectEnglish());
    expect(host.textContent).toBe("en-US");
    expect(document.documentElement.lang).toBe("en");
    expect(document.cookie).toContain("vt_locale_choice=en-US");
    expect(document.cookie).toContain("vt_locale_seen=en-US");
    expect(window.localStorage.getItem("vitlane.locale.v2")).toBe("en-US");
    await act(async () => root.unmount());
  });
});
