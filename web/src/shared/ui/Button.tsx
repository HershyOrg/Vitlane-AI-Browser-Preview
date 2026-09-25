import type {
  AnchorHTMLAttributes,
  ButtonHTMLAttributes,
  ReactNode,
} from "react";
import {
  Button as ShadcnButton,
  buttonVariants,
} from "./primitives/button";
import { Spinner } from "./primitives/spinner";
import { cn } from "../lib/utils";
import "./design-system/components.css";

export type ButtonEmphasis =
  | "primary"
  | "secondary"
  | "tertiary"
  | "quiet"
  | "danger";

export type ButtonSize = "default" | "compact";

export interface ButtonProps
  extends Omit<ButtonHTMLAttributes<HTMLButtonElement>, "className"> {
  busy?: boolean;
  children: ReactNode;
  className?: string;
  emphasis?: ButtonEmphasis;
  size?: ButtonSize;
}

export interface ButtonLinkProps
  extends Omit<AnchorHTMLAttributes<HTMLAnchorElement>, "className"> {
  children: ReactNode;
  className?: string;
  emphasis?: Exclude<ButtonEmphasis, "danger">;
  size?: ButtonSize;
}

export function buttonClassName({
  className,
  emphasis = "secondary",
  size = "default",
}: Pick<ButtonProps, "className" | "emphasis" | "size"> = {}): string {
  return buttonVariants({
    className: compatibilityClassName({ className, emphasis, size }),
    size: size === "compact" ? "default" : "lg",
    variant: primitiveVariant(emphasis),
  });
}

export function Button({
  busy = false,
  children,
  className,
  disabled,
  emphasis = "secondary",
  size = "default",
  type = "button",
  ...props
}: ButtonProps) {
  return (
    <ShadcnButton
      {...props}
      aria-busy={busy || undefined}
      className={compatibilityClassName({ className, emphasis, size })}
      disabled={disabled || busy}
      size={size === "compact" ? "default" : "lg"}
      type={type}
      variant={primitiveVariant(emphasis)}
    >
      {busy && <Spinner aria-hidden="true" data-icon="inline-start" />}
      {children}
    </ShadcnButton>
  );
}

export function ButtonLink({
  children,
  className,
  emphasis = "secondary",
  size = "default",
  ...props
}: ButtonLinkProps) {
  return (
    <ShadcnButton
      asChild
      className={compatibilityClassName({ className, emphasis, size })}
      size={size === "compact" ? "default" : "lg"}
      variant={primitiveVariant(emphasis)}
    >
      <a {...props}>{children}</a>
    </ShadcnButton>
  );
}

// Still Water (ADR-0073): four emphases. secondary is a neutral soft fill with
// no outline, quiet and danger are text-only, and tertiary is an alias of
// secondary kept for one release.
function primitiveVariant(emphasis: ButtonEmphasis) {
  if (emphasis === "primary") return "default" as const;
  if (emphasis === "quiet" || emphasis === "danger") return "ghost" as const;
  return "secondary" as const;
}

function compatibilityClassName({
  className,
  emphasis,
  size,
}: Required<Pick<ButtonProps, "emphasis" | "size">> &
  Pick<ButtonProps, "className">) {
  return cn(
    "vt-button",
    `vt-button--${emphasis}`,
    size === "compact" && "vt-button--compact",
    className,
  );
}
