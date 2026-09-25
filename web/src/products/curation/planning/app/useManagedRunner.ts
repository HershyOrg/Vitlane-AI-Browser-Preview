import { useEffect, useState } from "react";
import {
  fetchManagedRunnerCapability,
  type ManagedRunnerCapability,
} from "../infra/planningApi";

/**
 * Reads whether the Server can run MANAGED work right now.
 *
 * The Web never decides this locally. A build flag would go stale the moment
 * the runner is disabled or the daily server budget runs out, and the user
 * would submit an intent that can never execute.
 */
export function useManagedRunner(): {
  capability: ManagedRunnerCapability | null;
  loading: boolean;
} {
  const [capability, setCapability] = useState<ManagedRunnerCapability | null>(
    null,
  );
  const [loading, setLoading] = useState(true);

  useEffect(() => {
    let active = true;
    fetchManagedRunnerCapability()
      .then((result) => {
        if (active) setCapability(result);
      })
      .catch(() => {
        // A capability read failure must not block the composer. Treating it
        // as "not offerable" leaves the user on the external path, which is
        // the behaviour that existed before the runner.
        if (active) setCapability(null);
      })
      .finally(() => {
        if (active) setLoading(false);
      });
    return () => {
      active = false;
    };
  }, []);

  return { capability, loading };
}

/** Formats USD micros for display, e.g. 3100 -> "$0.0031". */
export function formatMicros(micros: number): string {
  if (!Number.isFinite(micros) || micros <= 0) return "$0";
  return `$${(micros / 1_000_000).toFixed(4).replace(/0+$/, "").replace(/\.$/, "")}`;
}
