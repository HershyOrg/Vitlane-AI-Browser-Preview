(() => {
  const localMarketingHosts = new Set([
    "127.0.0.1",
    "host.docker.internal",
    "localhost",
    "marketing.localhost",
    "::1",
  ]);
  const localAppHost = window.location.hostname === "marketing.localhost"
    ? "127.0.0.1"
    : window.location.hostname;
  const appBaseURL = localMarketingHosts.has(window.location.hostname)
    ? `${window.location.protocol}//${localAppHost}${window.location.port ? `:${window.location.port}` : ""}`
    : "https://app.vitlane.com";

  document.querySelectorAll("[data-vitlane-app-path]").forEach((link) => {
    const appPath = link.getAttribute("data-vitlane-app-path") || "/";
    link.href = new URL(appPath, appBaseURL).href;
  });
})();
