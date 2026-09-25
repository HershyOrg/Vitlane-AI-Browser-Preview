import type { ComponentProps, ReactNode } from "react";
import { ChevronDown } from "lucide-react";
import { Button } from "./Button";
import {
  Collapsible,
  CollapsibleContent,
  CollapsibleTrigger,
} from "./primitives/collapsible";
import { cn } from "../lib/utils";

export type DisclosureProps = Omit<
  ComponentProps<typeof Collapsible>,
  "children"
> & {
  children: ReactNode;
  contentClassName?: string;
  summary: ReactNode;
};

export function Disclosure({
  children,
  className,
  contentClassName,
  defaultOpen,
  open,
  onOpenChange,
  summary,
  ...props
}: DisclosureProps) {
  return (
    <Collapsible
      className={cn("vt-disclosure", className)}
      defaultOpen={defaultOpen}
      open={open}
      onOpenChange={onOpenChange}
      {...props}
    >
      <CollapsibleTrigger asChild>
        <Button
          className="vt-disclosure__trigger"
          emphasis="quiet"
          type="button"
        >
          <span>{summary}</span>
          <ChevronDown aria-hidden="true" />
        </Button>
      </CollapsibleTrigger>
      <CollapsibleContent
        className={cn("vt-disclosure__content", contentClassName)}
        forceMount
      >
        {children}
      </CollapsibleContent>
    </Collapsible>
  );
}
