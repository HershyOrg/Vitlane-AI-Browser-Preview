import type { ReactNode } from "react";
import { Button, type ButtonEmphasis } from "./Button";
import { Disclosure } from "./Disclosure";
import {
  ProductMedia,
  type ProductMediaProps,
} from "./ProductMedia";
import "./design-system/components.css";
import { useLocale } from "../i18n";

export type CandidateCardState =
  | "default"
  | "in-cart"
  | "configuring"
  | "pinned"
  | "configuration-required"
  | "unavailable"
  | "loading";

export interface CandidateCardAction {
  busy?: boolean;
  disabled?: boolean;
  emphasis?: ButtonEmphasis;
  label: string;
  onAction?: () => void;
}

export interface CandidateCardIconAction
  extends Omit<CandidateCardAction, "emphasis"> {
  kind: "pin" | "like" | "dislike";
  pressed: boolean;
}

export interface CandidateCardSignals {
  pinned?: boolean;
  liked?: boolean;
  disliked?: boolean;
  // Self-reported purchase on any option of this Candidate (ADR-0075). It is
  // orthogonal to pin/like/dislike and never means an order was placed.
  purchased?: boolean;
}

export interface CandidateCardProps {
  comparison?: ReactNode;
  density?: "default" | "compact";
  actions?: CandidateCardAction[];
  cardActionLabel?: string;
  details?: ReactNode;
  detailsLabel?: string;
  iconActions?: CandidateCardIconAction[];
  iconActionsPlacement?: "body" | "corner";
  media: ProductMediaProps;
  merchant?: string;
  name: string;
  onCardAction?: () => void;
  optionSummary?: string;
  price: string;
  priceAccessory?: ReactNode;
  priceNote?: ReactNode;
  productUrl?: string;
  primaryAction?: CandidateCardAction;
  recommendation?: string;
  secondaryAction?: CandidateCardAction;
  signals?: CandidateCardSignals;
  sourceBadge?: ReactNode;
  statusMessage?: ReactNode;
  state?: CandidateCardState;
}

export function CandidateCard({
  comparison,
  density = "default",
  actions = [],
  cardActionLabel,
  details,
  detailsLabel,
  iconActions = [],
  iconActionsPlacement = "body",
  media,
  merchant,
  name,
  onCardAction,
  optionSummary,
  price,
  priceAccessory,
  priceNote,
  productUrl,
  primaryAction,
  recommendation,
  secondaryAction,
  signals,
  sourceBadge,
  statusMessage,
  state = "default",
}: CandidateCardProps) {
  const { l } = useLocale();
  const signalDescriptors = [
    { key: "pinned", kind: "pin", label: l("Has a pinned option", "고정한 옵션 있음") },
    { key: "liked", kind: "like", label: l("Has a liked option", "좋아요한 옵션 있음") },
    { key: "disliked", kind: "dislike", label: l("Has a disliked option", "별로예요한 옵션 있음") },
    { key: "purchased", kind: "purchased", label: l("Marked purchased", "구매함") },
  ] as const;
  const stateLabels: Partial<Record<CandidateCardState, string>> = {
    configuring: l("Checking options", "옵션 확인 중"),
    pinned: l("PIN", "PIN"),
    "configuration-required": l("Options required", "옵션 확인 필요"),
    unavailable: l("Unavailable", "정보 확인 불가"),
    loading: l("Loading", "불러오는 중"),
  };
  const resolvedDetailsLabel = detailsLabel ?? l(
    "More product information",
    "상품 정보 더보기",
  );
  const isLoading = state === "loading";
  const activeSignals = signalDescriptors.filter(
    ({ key }) => signals?.[key],
  );
  const purchaseActions = density === "compact"
    ? [primaryAction, secondaryAction]
    : [secondaryAction, primaryAction];
  const reactionActions = iconActions.length > 0 && (
          <div
            className="vt-candidate-card__icon-actions"
            aria-label={l("Reactions for {name}", "{name} 반응", { name })}
          >
            {iconActions.map(({ kind, label, onAction, pressed, ...action }) => (
              <Button
                key={kind}
                className={`vt-candidate-card__icon-action is-${kind}`}
                emphasis="quiet"
                aria-label={label}
                aria-pressed={pressed}
                title={label}
                onClick={onAction}
                {...action}
                disabled={isLoading || action.disabled}
              >
                <CandidateActionIcon kind={kind} />
                <span className="vt-visually-hidden">{label}</span>
              </Button>
            ))}
          </div>
        );
  return (
    <article
      className={`vt-candidate-card is-${state}${density === "compact" ? " is-compact" : ""}`}
      aria-busy={isLoading || undefined}
    >
      {onCardAction && !isLoading && (
        <Button
          className="vt-candidate-card__hit-area"
          emphasis="quiet"
          aria-label={cardActionLabel ?? l("Select {name}", "{name} 선택", { name })}
          onClick={onCardAction}
        >
          <span className="vt-visually-hidden">
            {cardActionLabel ?? l("Select {name}", "{name} 선택", { name })}
          </span>
        </Button>
      )}
      {iconActionsPlacement === "corner" && reactionActions}
      {activeSignals.length > 0 && (
        <div className="vt-candidate-card__signals">
          {activeSignals.map(({ kind, label }) => (
            <span
              key={kind}
              className={`vt-candidate-card__signal is-${kind}`}
            >
              <CandidateActionIcon kind={kind} />
              <span className="vt-visually-hidden">{label}</span>
            </span>
          ))}
        </div>
      )}
      {productUrl && !isLoading ? (
        <a
          className="vt-candidate-card__media-link"
          href={productUrl}
          target="_blank"
          rel="noreferrer"
          title={productUrl}
          aria-label={l(
            "Open the product page for {name} in a new tab",
            "{name} 상품 페이지 열기, 새 창",
            { name },
          )}
        >
          <ProductMedia {...media} state={media.state} />
        </a>
      ) : (
        <ProductMedia
          {...media}
          state={isLoading ? "loading" : media.state}
        />
      )}
      <div className="vt-candidate-card__body">
        <div className="vt-candidate-card__meta">
          <span>{merchant ?? l("Merchant unavailable", "판매처 확인 필요")}</span>
          {stateLabels[state] && <strong>{stateLabels[state]}</strong>}
          {density === "compact" && state === "in-cart" && <strong>{l("In cart", "담김")}</strong>}
        </div>
        <div className="vt-candidate-card__identity">
          <h3 className="vt-candidate-card__title" title={name}>
            {isLoading ? (
              name
            ) : productUrl ? (
              <a href={productUrl} target="_blank" rel="noreferrer">
                {name}
              </a>
              ) : name}
          </h3>
          {sourceBadge ? (
            <div className="vt-candidate-card__source">{sourceBadge}</div>
          ) : null}
          {productUrl && !isLoading && (
            <a
              className="vt-candidate-card__url"
              href={productUrl}
              target="_blank"
              rel="noreferrer"
              title={productUrl}
            >
              {productUrl}
            </a>
          )}
        </div>
        <div className="vt-candidate-card__price-row">
        <p className="vt-candidate-card__price">
          {isLoading ? l("Checking price", "가격 확인 중") : <>{price}{priceNote}</>}
        </p>
        {priceAccessory}
        </div>
        {comparison}
        {recommendation && (
          <p className="vt-candidate-card__evidence">{recommendation}</p>
        )}
        {optionSummary ? (
          <p className="vt-candidate-card__option">
            <strong className={density === "compact" ? "vt-visually-hidden" : undefined}>{l("Options", "옵션")}</strong>
            <span>{optionSummary}</span>
          </p>
        ) : null}
        {iconActionsPlacement === "body" && reactionActions}
        <div className="vt-candidate-card__actions">
          {actions.map(({ emphasis = "quiet", label, onAction, ...action }) => (
            <Button
              key={label}
              emphasis={emphasis}
              onClick={onAction}
              {...action}
              disabled={isLoading || action.disabled}
            >
              {label}
            </Button>
          ))}
        </div>
        {(primaryAction || secondaryAction) && (
          <div className="vt-candidate-card__primary-action">
            {purchaseActions.filter((action): action is CandidateCardAction => Boolean(action)).map((action) => (
              <Button
                key={action === primaryAction ? "primary" : "secondary"}
                emphasis={action.emphasis ?? (action === primaryAction ? "primary" : "secondary")}
                onClick={action.onAction}
                disabled={isLoading || action.disabled}
                busy={action.busy}
              >
                {action.label}
              </Button>
            ))}
          </div>
        )}
        {statusMessage && (
          <div className="vt-candidate-card__status" role="status">
            {statusMessage}
          </div>
        )}
        {details && !isLoading && (
          <Disclosure
            className="vt-candidate-card__details"
            summary={resolvedDetailsLabel}
          >
            {details}
          </Disclosure>
        )}
      </div>
    </article>
  );
}

function CandidateActionIcon({
  kind,
}: {
  kind: CandidateCardIconAction["kind"] | "purchased";
}) {
  if (kind === "purchased") {
    return (
      <svg aria-hidden="true" viewBox="0 0 24 24">
        <path d="m5.5 12.5 4.5 4.5 8.5-9.5" />
      </svg>
    );
  }
  if (kind === "pin") {
    return (
      <svg aria-hidden="true" viewBox="0 0 24 24">
        <path d="m8 4 8 0-1 5 3 3v2h-5v6l-1 1-1-1v-6H6v-2l3-3-1-5Z" />
      </svg>
    );
  }
  return (
    <svg
      aria-hidden="true"
      className={kind === "dislike" ? "is-reversed" : undefined}
      viewBox="0 0 24 24"
    >
      <path d="M8 11V4H5v7h3Zm2 0 2-7h2c2 0 3 1 3 3v4h3v2l-3 6h-7V11Z" />
    </svg>
  );
}
