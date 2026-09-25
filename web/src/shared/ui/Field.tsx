import {
  cloneElement,
  type HTMLAttributes,
  type ReactElement,
  type ReactNode,
} from "react";
import "./design-system/components.css";

type FieldControlProps = {
  "aria-describedby"?: string;
  "aria-invalid"?: boolean;
  "aria-required"?: boolean;
  id?: string;
};

export interface FieldProps extends HTMLAttributes<HTMLDivElement> {
  children: ReactElement<FieldControlProps>;
  error?: string;
  hint?: ReactNode;
  id: string;
  label: ReactNode;
  required?: boolean;
}

export function Field({
  children,
  className,
  error,
  hint,
  id,
  label,
  required = false,
  ...props
}: FieldProps) {
  const controlId = children.props.id ?? id;
  const hintId = hint ? `${id}-hint` : undefined;
  const errorId = error ? `${id}-error` : undefined;
  const describedBy = [
    children.props["aria-describedby"],
    hintId,
    errorId,
  ]
    .filter(Boolean)
    .join(" ") || undefined;
  const control = cloneElement(children, {
    id: controlId,
    "aria-describedby": describedBy,
    "aria-invalid": error ? true : children.props["aria-invalid"],
    "aria-required": required || children.props["aria-required"] || undefined,
  });

  return (
    <div
      {...props}
      className={["vt-field", className ?? ""].filter(Boolean).join(" ")}
    >
      <label className="vt-field__label" htmlFor={controlId}>
        {label}
        {required && <span aria-hidden="true"> *</span>}
      </label>
      <div className="vt-field__control">{control}</div>
      {hint && (
        <div className="vt-field__hint" id={hintId}>
          {hint}
        </div>
      )}
      {error && (
        <div className="vt-field__error" id={errorId}>
          {error}
        </div>
      )}
    </div>
  );
}
