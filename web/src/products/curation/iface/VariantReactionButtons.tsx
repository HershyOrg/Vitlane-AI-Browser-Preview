import { Pin, ThumbsDown, ThumbsUp } from "lucide-react";
import { Button } from "../../../shared/ui";
import { useLocale } from "../../../shared/i18n";
import type { VariantInteraction } from "../domain/candidatePresentation";

export function VariantReactionButtons({ value, disabled, onChange, subject = "VARIANT" }: {
  value: VariantInteraction;
  disabled?: boolean;
  subject?: "VARIANT" | "PRODUCT";
  onChange: (patch: Partial<VariantInteraction>) => void;
}) {
  const { l } = useLocale();
  return <section className="catalog-ui-variant-interactions" aria-label={subject === "PRODUCT" ? l("Reaction to this product", "이 상품 반응") : l("Reaction to selected option", "선택한 옵션 반응")}>
    <Button type="button" size="compact" emphasis="quiet" data-reaction="pin" aria-pressed={value.pinned} disabled={disabled} onClick={() => onChange({ pinned: !value.pinned })}>
      <Pin size={15} aria-hidden="true" /> {value.pinned ? l("Unpin", "Pin 해제") : l("Pin", "Pin")}
    </Button>
    <Button type="button" size="compact" emphasis="quiet" data-reaction="like" aria-pressed={value.sentiment === "LIKE"} disabled={disabled} onClick={() => onChange({ sentiment: value.sentiment === "LIKE" ? "NONE" : "LIKE" })}>
      <ThumbsUp size={15} aria-hidden="true" /> {l("Like", "좋아요")}
    </Button>
    <Button type="button" size="compact" emphasis="quiet" data-reaction="dislike" aria-pressed={value.sentiment === "DISLIKE"} disabled={disabled} onClick={() => onChange({ sentiment: value.sentiment === "DISLIKE" ? "NONE" : "DISLIKE" })}>
      <ThumbsDown size={15} aria-hidden="true" /> {l("Dislike", "싫어요")}
    </Button>
  </section>;
}
