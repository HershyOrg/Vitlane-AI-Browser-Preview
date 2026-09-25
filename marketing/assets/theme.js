// Marketing theme (Still Water, PR F): light by default, dark when the system
// asks for it, and a header toggle that remembers the choice for this site
// only. Runs before the stylesheets so the first paint already has the theme.
// The App's own appearance preference key is never read or written here.
(() => {
  const key = "vitlane.marketing.theme";
  const root = document.documentElement;
  const media = window.matchMedia("(prefers-color-scheme: dark)");
  const stored = () => {
    try {
      const value = localStorage.getItem(key);
      return value === "dark" || value === "light" ? value : null;
    } catch {
      return null;
    }
  };
  const apply = (theme) => {
    root.classList.toggle("dark", theme === "dark");
    root.dataset.theme = theme;
    document.querySelectorAll("[data-theme-toggle]").forEach((button) => {
      button.setAttribute("aria-pressed", String(theme === "dark"));
    });
  };
  const current = () => stored() ?? (media.matches ? "dark" : "light");
  apply(current());
  media.addEventListener?.("change", () => {
    if (!stored()) apply(current());
  });
  document.addEventListener("DOMContentLoaded", () => apply(current()));
  document.addEventListener("click", (event) => {
    const button = event.target instanceof Element ? event.target.closest("[data-theme-toggle]") : null;
    if (!button) return;
    const next = root.dataset.theme === "dark" ? "light" : "dark";
    try {
      localStorage.setItem(key, next);
    } catch {
      // Storage may be unavailable; the choice then lasts for this page only.
    }
    apply(next);
  });
})();
