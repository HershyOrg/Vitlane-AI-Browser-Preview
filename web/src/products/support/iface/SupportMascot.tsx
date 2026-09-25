import { useId, type SVGProps } from "react";

// Vitlane ghost mascot (2026-09-14): the original silhouette, oval eyes and
// V band stay; the mouth is gone and every color is a Still Water token so the
// mascot follows light/dark like the rest of the shell. Decorative only.
const body =
  "M60 12C36 12 21 30 21 55v32c0 4.6 5.4 6.9 8.6 3.7l4.8-4.8 6.6 6.6c2 2 5.2 2 7.2 0l6.2-6.2 5.6 5.6 5.6-5.6 6.2 6.2c2 2 5.2 2 7.2 0l6.6-6.6 4.8 4.8c3.2 3.2 8.6.9 8.6-3.7V55C99 30 84 12 60 12Z";

export function SupportMascot({ className, ...props }: SVGProps<SVGSVGElement>) {
  // One mascot per avatar, launcher and welcome state can share a page, so the
  // clip-path id must be unique per instance (duplicate ids fail the catalog
  // regression and break the clip for every copy but the first).
  const clipId = `support-mascot-${useId().replace(/[^a-zA-Z0-9_-]/g, "")}`;
  return (
    <svg
      aria-hidden="true"
      className={["support-mascot", className].filter(Boolean).join(" ")}
      focusable="false"
      viewBox="0 0 120 120"
      {...props}
    >
      <defs>
        <clipPath id={clipId}>
          <path d={body} />
        </clipPath>
      </defs>
      <path d={body} fill="var(--vt-semantic-color-surface-base)" />
      <g clipPath={`url(#${clipId})`}>
        <path
          d="M20 28 60 64 100 28"
          fill="none"
          stroke="var(--vt-semantic-color-action-brand)"
          strokeWidth="15"
        />
      </g>
      <path
        d={body}
        fill="none"
        stroke="var(--vt-semantic-color-text-accent)"
        strokeLinejoin="round"
        strokeWidth="4"
      />
      <ellipse cx="46" cy="70" fill="var(--vt-semantic-color-text-accent)" rx="3.6" ry="8" />
      <ellipse cx="74" cy="70" fill="var(--vt-semantic-color-text-accent)" rx="3.6" ry="8" />
    </svg>
  );
}
