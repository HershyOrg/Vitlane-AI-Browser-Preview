import type { HTMLAttributes, ReactNode } from "react";
import { cn } from "../lib/utils";
import "./design-system/components.css";

// Still Water (ADR-0073, component-contracts 3-1): a chip is sentence-case
// caption text. State lives in the dot only (progress still, done text,
// waiting an empty circle, failure danger); a TEST/LIVE boundary is a soft
// tone surface with tone text; "needs attention" is the danger soft surface.
export type ChipTone = "progress" | "done" | "waiting" | "failed";
export type ChipMode = "test" | "live";

export interface ChipProps
  extends Omit<HTMLAttributes<HTMLSpanElement>, "className"> {
  attention?: boolean;
  children: ReactNode;
  className?: string;
  mode?: ChipMode;
  tone?: ChipTone;
}

export function Chip({
  attention = false,
  children,
  className,
  mode,
  tone,
  ...props
}: ChipProps) {
  return (
    <span
      {...props}
      className={cn(
        "vt-chip",
        mode && `vt-chip--mode is-${mode}`,
        attention && "vt-chip--attention",
        className,
      )}
      data-slot="chip"
      data-tone={tone}
    >
      {children}
    </span>
  );
}
