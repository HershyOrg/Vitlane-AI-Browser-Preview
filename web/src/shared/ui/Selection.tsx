import type { ReactNode } from "react";
import { cn } from "../lib/utils";
import { ToggleGroup, ToggleGroupItem } from "./primitives/toggle-group";
import "./design-system/components.css";

// Still Water (ADR-0073, component-contracts 3-1): a single-choice group whose
// selected option is `surface.selected` + `text.accent` with no ring or
// outline. Built on the Radix toggle group so keyboard and ARIA come for free.
export interface SelectionOption {
  ariaLabel?: string;
  disabled?: boolean;
  label: ReactNode;
  value: string;
}

export interface SelectionProps {
  ariaLabel: string;
  className?: string;
  onChange: (value: string) => void;
  options: readonly SelectionOption[];
  value: string;
}

export function Selection({
  ariaLabel,
  className,
  onChange,
  options,
  value,
}: SelectionProps) {
  return (
    <ToggleGroup
      aria-label={ariaLabel}
      className={cn("vt-selection", className)}
      onValueChange={(next) => {
        // Radix reports "" when the current option is pressed again; a
        // single choice never becomes empty.
        if (next) onChange(next);
      }}
      spacing={0}
      type="single"
      value={value}
    >
      {options.map((option) => (
        <ToggleGroupItem
          aria-label={option.ariaLabel}
          className="vt-selection__option"
          disabled={option.disabled}
          key={option.value}
          value={option.value}
        >
          {option.label}
        </ToggleGroupItem>
      ))}
    </ToggleGroup>
  );
}
