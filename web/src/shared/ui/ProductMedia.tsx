import { useState } from "react";
import "./design-system/components.css";
import { useLocale } from "../i18n";

export type ProductMediaState = "ready" | "loading" | "missing" | "error";

export interface ProductMediaProps {
  alt: string;
  caption?: string;
  src?: string;
  state?: ProductMediaState;
}

export function ProductMedia({
  alt,
  caption,
  src,
  state,
}: ProductMediaProps) {
  const { l } = useLocale();
  const [failedSource, setFailedSource] = useState<string | null>(null);
  const resolvedState: ProductMediaState =
    state ??
    (src
      ? failedSource === src
        ? "error"
        : "ready"
      : "missing");
  const label =
    resolvedState === "loading"
      ? l("Loading image for {alt}", "{alt} 이미지를 불러오는 중", { alt })
      : resolvedState === "error"
        ? l("Image unavailable for {alt}", "{alt} 이미지를 불러올 수 없음", { alt })
        : l("No image for {alt}", "{alt} 이미지 없음", { alt });

  return (
    <figure className={`vt-product-media is-${resolvedState}`}>
      {resolvedState === "ready" && src ? (
        <img
          src={src}
          alt={alt}
          loading="lazy"
          decoding="async"
          fetchPriority="low"
          referrerPolicy="no-referrer"
          onError={() => setFailedSource(src)}
        />
      ) : (
        <div
          className="vt-product-media__placeholder"
          role="img"
          aria-label={label}
          aria-busy={resolvedState === "loading" || undefined}
        >
          <span aria-hidden="true">{l("V", "V")}</span>
          <strong>
            {resolvedState === "loading"
              ? l("Loading image", "이미지 불러오는 중")
              : resolvedState === "error"
                ? l("Image unavailable", "이미지 확인 불가")
                : l("No image", "이미지 없음")}
          </strong>
        </div>
      )}
      {caption && <figcaption>{caption}</figcaption>}
    </figure>
  );
}
