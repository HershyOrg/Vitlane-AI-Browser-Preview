(() => {
  // v2 (ADR-0073, Still Water): every user starts on the still accent once.
  // A v1 record only carries its theme over; its accent is discarded. The
  // palette ids of ADR-0082 live in the same record.
  const key = "vitlane.appearance.v2";
  const legacyKey = "vitlane.appearance.v1";
  const fallback = { theme: "light", accent: "blue" };

  try {
    const stored = JSON.parse(window.localStorage.getItem(key) || "null");
    const legacy = stored
      ? null
      : JSON.parse(window.localStorage.getItem(legacyKey) || "null");
    const source = stored || legacy;
    const theme =
      source && (source.theme === "light" || source.theme === "dark")
        ? source.theme
        : fallback.theme;
    // ADR-0082 palettes. An unknown id (older or newer record) reads as still.
    const accents = ["blue", "moss", "maple", "iris", "neutral"];
    const accent =
      stored && accents.includes(stored.accent) ? stored.accent : fallback.accent;
    const root = document.documentElement;
    root.classList.toggle("dark", theme === "dark");
    root.dataset.theme = theme;
    root.dataset.accent = accent;
  } catch {
    document.documentElement.dataset.theme = fallback.theme;
    document.documentElement.dataset.accent = fallback.accent;
  }
})();
