export type RouteChunkRecoveryEnvironment = {
  pathname: string;
  search: string;
  storage: Pick<Storage, "getItem" | "removeItem" | "setItem">;
  reload: () => void;
};

const recoveryPrefix = "vitlane.route-chunk-reload.v1";

export function isRouteChunkLoadError(error: unknown): boolean {
  const message = error instanceof Error ? error.message : String(error ?? "");
  const normalized = message.toLowerCase();
  return normalized.includes("failed to fetch dynamically imported module")
    || normalized.includes("error loading dynamically imported module")
    || normalized.includes("importing a module script failed")
    || normalized.includes("failed to load module script")
    || normalized.includes("chunkloaderror")
    || normalized.includes("loading chunk");
}

export function routeChunkRecoveryKey(
  routeKey: string,
  environment: Pick<RouteChunkRecoveryEnvironment, "pathname" | "search">,
): string {
  return `${recoveryPrefix}:${routeKey}:${environment.pathname}${environment.search}`;
}

export function attemptRouteChunkReload(
  routeKey: string,
  error: unknown,
  providedEnvironment?: RouteChunkRecoveryEnvironment,
): boolean {
  if (!isRouteChunkLoadError(error)) return false;
  const environment = providedEnvironment ?? browserEnvironment();
  if (!environment) return false;
  const key = routeChunkRecoveryKey(routeKey, environment);

  try {
    if (environment.storage.getItem(key) === "attempted") return false;
    environment.storage.setItem(key, "attempted");
    environment.reload();
    return true;
  } catch {
    try {
      environment.storage.removeItem(key);
    } catch {
      // Storage can be unavailable in privacy-restricted browser contexts.
    }
    return false;
  }
}

export function clearRouteChunkReload(
  routeKey: string,
  providedEnvironment?: RouteChunkRecoveryEnvironment,
) {
  const environment = providedEnvironment ?? browserEnvironment();
  if (!environment) return;
  try {
    environment.storage.removeItem(routeChunkRecoveryKey(routeKey, environment));
  } catch {
    // The route itself loaded, so unavailable session storage needs no fallback.
  }
}

export async function loadRouteModule<Module>(
  routeKey: string,
  importer: () => Promise<Module>,
  environment?: RouteChunkRecoveryEnvironment,
): Promise<Module> {
  try {
    const module = await importer();
    clearRouteChunkReload(routeKey, environment);
    return module;
  } catch (error) {
    attemptRouteChunkReload(routeKey, error, environment);
    throw error;
  }
}

function browserEnvironment(): RouteChunkRecoveryEnvironment | undefined {
  if (typeof window === "undefined") return undefined;
  try {
    return {
      pathname: window.location.pathname,
      search: window.location.search,
      storage: window.sessionStorage,
      reload: () => window.location.reload(),
    };
  } catch {
    return undefined;
  }
}
