(() => {
  // ADR-0080: remember the language this browser last saw Vitlane in, and a
  // language link as an explicit choice before its navigation leaves the page.
  const locale = location.pathname === "/ko" || location.pathname.startsWith("/ko/")
    ? "ko-KR"
    : "en-US";
  document.documentElement.lang = locale === "ko-KR" ? "ko" : "en";
  const domain = /(^|\.)vitlane\.com$/i.test(location.hostname)
    ? "; Domain=.vitlane.com"
    : "";
  const remember = (name, value) => {
    document.cookie = `${name}=${value}; Path=/; Max-Age=31536000; SameSite=Lax${domain}`;
  };
  remember("vt_locale_seen", locale);
  document.addEventListener("click", (event) => {
    const link = event.target instanceof Element ? event.target.closest("a[hreflang]") : null;
    const hreflang = link ? link.getAttribute("hreflang") : "";
    const chosen = hreflang === "ko" ? "ko-KR" : hreflang === "en" ? "en-US" : "";
    if (chosen) remember("vt_locale_choice", chosen);
  }, true);
})();
