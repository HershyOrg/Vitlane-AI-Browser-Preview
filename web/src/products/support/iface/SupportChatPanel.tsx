import { ProcessRequestPending, ProcessRequestRejected, getProcessReceipt, type ProcessReceipt } from "../../ordering/infra/orderProcessApi";
import { processProgressLabel } from "../../ordering/app/processPresentation";
import {
  type CSSProperties,
  type KeyboardEvent as ReactKeyboardEvent,
  type PointerEvent as ReactPointerEvent,
  useCallback,
  useEffect,
  useRef,
  useState,
} from "react";
import { Link } from "react-router";
import { Button, Input, NativeSelect, NativeSelectOption, Notice, Textarea, sheetGrabberProps, useSwipeDismiss } from "../../../shared/ui";
import { localizeFixedCopy, useLocale, type Localize } from "../../../shared/i18n";
import { listAgencyOrders } from "../../ordering/infra/agencyOrderApi";
import type { SupportBusinessCard, SupportMessage } from "../domain/message";
import { mergeMessages } from "../domain/thread";
import {
  listSupportMessages,
  markSupportRead,
  respondToProcurementRequest,
  sendSupportMessage,
  supportImageURL,
} from "../infra/supportApi";
import { SupportMascot } from "./SupportMascot";
import "./support-chat.css";

export const openSupportChatEvent = "vitlane:open-support-chat";
export const supportSummaryRefreshEvent = "vitlane:support-summary-refresh";

// 위젯 상태 기계: hidden(위젯 없음) → open(런처+창) ⇄ launcher(런처만).
// 런처 클릭·Esc는 창만 토글하고, 창 헤더의 명시적 X만 위젯 전체를 지운다 —
// 상시 떠 있는 버블이 거슬리면 지울 수 있어야 한다. 재진입은 프로필 메뉴
// "메시지". 주문 안내·행동 카드와 고객 문의가 같은 단일 대화에 쌓인다.
type WidgetState = "hidden" | "launcher" | "open";

type AttachableOrder = { id: string; label: string; issuedAt: string };
type SupportChatSize = { width: number; height: number };
type SupportChatResizeBounds = {
  minWidth: number;
  minHeight: number;
  maxWidth: number;
  maxHeight: number;
};
type SupportChatWindowStyle = CSSProperties & {
  "--support-chat-width"?: string;
  "--support-chat-height"?: string;
};

const supportChatSizeStorageKey = "vitlane.support-chat-size.v1";
const supportChatMinimumSize = { width: 320, height: 352 };
const supportChatViewportMargin = 16;
const supportChatKeyboardStep = 16;

export function clampSupportChatSize(
  size: SupportChatSize,
  bounds: SupportChatResizeBounds,
): SupportChatSize {
  const maxWidth = Math.max(1, bounds.maxWidth);
  const maxHeight = Math.max(1, bounds.maxHeight);
  const minWidth = Math.min(bounds.minWidth, maxWidth);
  const minHeight = Math.min(bounds.minHeight, maxHeight);
  return {
    width: Math.round(Math.min(maxWidth, Math.max(minWidth, size.width))),
    height: Math.round(Math.min(maxHeight, Math.max(minHeight, size.height))),
  };
}

function readSupportChatSize(): SupportChatSize | undefined {
  if (typeof window === "undefined") return undefined;
  try {
    const parsed = JSON.parse(
      window.localStorage.getItem(supportChatSizeStorageKey) ?? "null",
    ) as Partial<SupportChatSize> | null;
    if (
      parsed &&
      Number.isFinite(parsed.width) &&
      Number.isFinite(parsed.height) &&
      Number(parsed.width) > 0 &&
      Number(parsed.height) > 0
    ) {
      return { width: Number(parsed.width), height: Number(parsed.height) };
    }
  } catch {
    // Invalid presentation state falls back to the responsive CSS default.
  }
  return undefined;
}

function persistSupportChatSize(size?: SupportChatSize) {
  try {
    if (size) {
      window.localStorage.setItem(supportChatSizeStorageKey, JSON.stringify(size));
    } else {
      window.localStorage.removeItem(supportChatSizeStorageKey);
    }
  } catch {
    // Resizing remains available for this session when storage is blocked.
  }
}

function supportChatResizeBounds(rect: DOMRect): SupportChatResizeBounds {
  return {
    minWidth: supportChatMinimumSize.width,
    minHeight: supportChatMinimumSize.height,
    // The window is anchored at its bottom-right. Keep its top and left edges
    // inside the viewport while the top-left handle moves.
    maxWidth: rect.right - supportChatViewportMargin,
    maxHeight: rect.bottom - supportChatViewportMargin,
  };
}

// 주문 표시 라벨 — 추적 화면과 같은 관례(첫 상품명 외 N건).
export function orderAttachLabel(
  lines: Array<{ productTitle: string }>,
  l: Localize = localizeFixedCopy,
): string {
  if (lines.length === 0) return l("Order", "주문");
  return lines.length > 1
    ? l("{title} and {count} more", "{title} 외 {count}건", {
        title: lines[0].productTitle,
        count: lines.length - 1,
      })
    : lines[0].productTitle;
}

export function SupportChatPanel({ unread }: { unread: number }) {
  const { l, locale } = useLocale();
  const timeFormat = new Intl.DateTimeFormat(locale, {
    dateStyle: "medium",
    timeStyle: "short",
  });
  const [widget, setWidget] = useState<WidgetState>("hidden");
  const [messages, setMessages] = useState<SupportMessage[]>([]);
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [draft, setDraft] = useState("");
  const [sending, setSending] = useState(false);
  // 주문 첨부(ADR-0059 §5 — 운영자와 대칭, 자기 주문만 서버가 허용).
  const [attachOpen, setAttachOpen] = useState(false);
  const [attachable, setAttachable] = useState<AttachableOrder[] | null>(null);
  const [attachError, setAttachError] = useState<string | null>(null);
  const [attached, setAttached] = useState<AttachableOrder | null>(null);
  const [images, setImages] = useState<File[]>([]);
  const [imageError, setImageError] = useState<string | null>(null);
  const [chatSize, setChatSize] = useState<SupportChatSize | undefined>(
    readSupportChatSize,
  );
  const [resizing, setResizing] = useState(false);
  const logRef = useRef<HTMLOListElement>(null);
  const imageInputRef = useRef<HTMLInputElement>(null);
  const windowRef = useRef<HTMLElement>(null);
  // On phones the window sits above the launcher like a sheet: dragging its
  // header down folds it back into the launcher, the same as Escape.
  const windowSheet = useSwipeDismiss(windowRef, {
    direction: "down",
    media: "(max-width: 45rem)",
    enabled: widget === "open",
    from: "handle",
    onDismiss: () => setWidget("launcher"),
  });
  const resizeRef = useRef<{
    pointerId: number;
    startX: number;
    startY: number;
    startSize: SupportChatSize;
    bounds: SupportChatResizeBounds;
    lastSize: SupportChatSize;
  } | undefined>(undefined);

  useEffect(() => {
    function open() {
      setWidget("open");
    }
    window.addEventListener(openSupportChatEvent, open);
    return () => window.removeEventListener(openSupportChatEvent, open);
  }, []);

  const load = useCallback(async () => {
    try {
      const result = await listSupportMessages();
      setMessages((current) => mergeMessages(current, result.messages));
      setError(null);
      // 창이 열려 있는 동안 도착한 답변은 읽음 워터마크로 봉인하고 전역
      // 뱃지를 즉시 갱신한다(권위는 서버 summary COUNT).
      if (result.messages.some((item) => item.author !== "CUSTOMER" && !item.readAt)) {
        await markSupportRead();
        window.dispatchEvent(new CustomEvent(supportSummaryRefreshEvent));
      }
    } catch {
      setError(l("We couldn't load the conversation. Try again shortly.", "대화를 불러오지 못했습니다. 잠시 후 다시 시도해 주세요."));
    } finally {
      setLoading(false);
    }
  }, [l]);

  useEffect(() => {
    if (widget !== "open") return;
    setLoading(true);
    void load();
    const timer = window.setInterval(() => void load(), 5000);
    return () => window.clearInterval(timer);
  }, [widget, load]);

  useEffect(() => {
    if (widget !== "open") return;
    function onKeyDown(event: KeyboardEvent) {
      if (event.key === "Escape" && !event.defaultPrevented) {
        setWidget("launcher");
      }
    }
    window.addEventListener("keydown", onKeyDown);
    return () => window.removeEventListener("keydown", onKeyDown);
  }, [widget]);

  useEffect(() => {
    if (widget === "open") return;
    resizeRef.current = undefined;
    setResizing(false);
  }, [widget]);

  useEffect(() => {
    const log = logRef.current;
    if (log) log.scrollTop = log.scrollHeight;
  }, [messages.length, widget]);

  async function send() {
    const body = draft.trim();
    if (!body || sending) return;
    setSending(true);
    try {
      const { message } = await sendSupportMessage(body, attached?.id ?? "", images);
      setMessages((current) => mergeMessages(current, [message]));
      setDraft("");
      setAttached(null);
      setImages([]);
      if (imageInputRef.current) imageInputRef.current.value = "";
      setAttachOpen(false);
      setError(null);
    } catch {
      setError(l("We couldn't send the message. Try again.", "메시지를 보내지 못했습니다. 다시 시도해 주세요."));
    } finally {
      setSending(false);
    }
  }

  function selectImages(files: FileList | null) {
    const selected = Array.from(files ?? []);
    const validTypes = new Set(["image/jpeg", "image/png"]);
    if (selected.length > 4 || selected.some((file) =>
      !validTypes.has(file.type) || file.size > 5 * 1024 * 1024)) {
      setImages([]);
      setImageError(l(
        "Attach up to four JPEG or PNG images, no larger than 5 MB each.",
        "JPEG 또는 PNG 이미지를 최대 4장, 장당 5MB 이하로 첨부해 주세요.",
      ));
      if (imageInputRef.current) imageInputRef.current.value = "";
      return;
    }
    setImages(selected);
    setImageError(null);
  }

  async function toggleAttach() {
    const next = !attachOpen;
    setAttachOpen(next);
    if (!next || attachable !== null) return;
    try {
      const result = await listAgencyOrders({ view: "ALL", limit: 10 });
      setAttachable(result.agencyOrders.map((projection) => ({
        id: projection.agencyOrder.id,
        label: orderAttachLabel(projection.agencyOrder.lines, l),
        issuedAt: projection.agencyOrder.issuedAt,
      })));
      setAttachError(null);
    } catch {
      setAttachError(l("We couldn't load the order list.", "주문 목록을 불러오지 못했습니다."));
    }
  }

  function startResize(event: ReactPointerEvent<HTMLDivElement>) {
    if (event.button !== 0) return;
    const panel = windowRef.current;
    if (!panel) return;
    const rect = panel.getBoundingClientRect();
    const startSize = { width: rect.width, height: rect.height };
    resizeRef.current = {
      pointerId: event.pointerId,
      startX: event.clientX,
      startY: event.clientY,
      startSize,
      bounds: supportChatResizeBounds(rect),
      lastSize: startSize,
    };
    event.currentTarget.setPointerCapture?.(event.pointerId);
    setResizing(true);
    event.preventDefault();
  }

  function resizeWithPointer(event: ReactPointerEvent<HTMLDivElement>) {
    const drag = resizeRef.current;
    if (!drag || drag.pointerId !== event.pointerId) return;
    const next = clampSupportChatSize(
      {
        width: drag.startSize.width + drag.startX - event.clientX,
        height: drag.startSize.height + drag.startY - event.clientY,
      },
      drag.bounds,
    );
    drag.lastSize = next;
    setChatSize(next);
    event.preventDefault();
  }

  function finishResize(event: ReactPointerEvent<HTMLDivElement>) {
    const drag = resizeRef.current;
    if (!drag || drag.pointerId !== event.pointerId) return;
    persistSupportChatSize(drag.lastSize);
    resizeRef.current = undefined;
    if (event.currentTarget.hasPointerCapture?.(event.pointerId)) {
      event.currentTarget.releasePointerCapture?.(event.pointerId);
    }
    setResizing(false);
  }

  function resizeWithKeyboard(event: ReactKeyboardEvent<HTMLDivElement>) {
    if (event.key === "Home") {
      setChatSize(undefined);
      persistSupportChatSize();
      event.preventDefault();
      return;
    }
    const delta = event.shiftKey
      ? supportChatKeyboardStep * 3
      : supportChatKeyboardStep;
    const changes: Record<string, SupportChatSize> = {
      ArrowLeft: { width: delta, height: 0 },
      ArrowRight: { width: -delta, height: 0 },
      ArrowUp: { width: 0, height: delta },
      ArrowDown: { width: 0, height: -delta },
    };
    const change = changes[event.key];
    const panel = windowRef.current;
    if (!change || !panel) return;
    const rect = panel.getBoundingClientRect();
    const next = clampSupportChatSize(
      {
        width: (chatSize?.width ?? rect.width) + change.width,
        height: (chatSize?.height ?? rect.height) + change.height,
      },
      supportChatResizeBounds(rect),
    );
    setChatSize(next);
    persistSupportChatSize(next);
    event.preventDefault();
  }

  if (widget === "hidden") return null;

  const resolvedCardIDs = new Set(messages.flatMap((message) =>
    message.businessCard?.resolvesCardId ? [message.businessCard.resolvesCardId] : [],
  ));
  const windowStyle: SupportChatWindowStyle | undefined = chatSize
    ? {
        "--support-chat-width": `${chatSize.width}px`,
        "--support-chat-height": `${chatSize.height}px`,
      }
    : undefined;

  return (
    <div className="support-chat" data-testid="support-chat-widget">
      {widget === "open" && (
        <section
          aria-label={l("Vitlane Messages", "Vitlane 메시지")}
          className={`support-chat__window${resizing ? " is-resizing" : ""}`}
          ref={windowRef}
          role="dialog"
          style={windowStyle}
        >
          <div
            aria-label={l("Resize Messages window", "메시지 창 크기 조절")}
            aria-valuetext={
              chatSize
                ? l(
                    "{width} by {height} pixels",
                    "너비 {width}, 높이 {height} 픽셀",
                    chatSize,
                  )
                : l("Default responsive size", "기본 반응형 크기")
            }
            className="support-chat__resize-handle"
            onKeyDown={resizeWithKeyboard}
            onPointerCancel={finishResize}
            onPointerDown={startResize}
            onPointerMove={resizeWithPointer}
            onPointerUp={finishResize}
            role="separator"
            tabIndex={0}
            title={l(
              "Drag to resize. Use arrow keys to adjust, or Home to reset.",
              "드래그해 크기를 조절하세요. 방향키로 조절하고 Home 키로 초기화할 수 있습니다.",
            )}
          >
            <ResizeHandleIcon />
          </div>
          {windowSheet ? <div {...sheetGrabberProps} /> : null}
          <header className="support-chat__header" data-swipe-dismiss="handle">
            <SupportMascot className="support-chat__avatar" />
            <div className="support-chat__title">
              <strong>{l("Messages", "메시지")}</strong>
              <small>{l("Order updates and replies, all in one place", "주문 진행 안내와 문의 답변을 한곳에서 확인하세요")}</small>
            </div>
            <Button
              aria-label={l("Close Messages", "메시지 닫기")}
              className="support-chat__close"
              emphasis="quiet"
              onClick={() => setWidget("hidden")}
              size="compact"
              type="button"
            >
              <CloseIcon />
            </Button>
          </header>
          {error ? (
            <p className="support-chat__error" role="alert">{error}</p>
          ) : null}
          <ol className="support-chat__log" data-testid="support-chat-log" ref={logRef}>
            {messages.length === 0 && !loading ? (
              <li className="support-chat__welcome">
                <SupportMascot className="support-chat__welcome-mascot" />
                <p>
                  {l(
                    "Order updates, requests that need your response, and replies from Vitlane appear here. You can also send a question at any time.",
                    "주문 진행 안내, 응답이 필요한 요청, Vitlane의 답변이 여기에 표시됩니다. 궁금한 점도 언제든 남길 수 있어요.",
                  )}
                </p>
              </li>
            ) : null}
            {messages.map((message) => (
              <li
                className={[
                  "support-chat__bubble",
                  message.author === "CUSTOMER" ? "is-user" : "is-vitlane",
                ].join(" ")}
                key={message.id}
              >
                {message.author !== "CUSTOMER" ? (
                  <SupportMascot className="support-chat__bubble-avatar" />
                ) : null}
                <div className="support-chat__bubble-body">
                  <p>{message.body}</p>
                  {message.businessCard ? (
                    <SupportBusinessCardView
                      agencyOrderId={message.agencyOrderId}
                      card={message.businessCard}
                      onResolved={load}
                      resolved={resolvedCardIDs.has(message.id)}
                    />
                  ) : null}
                  {(message.attachments ?? []).length > 0 ? (
                    <div className="support-chat__images">
                      {(message.attachments ?? []).map((attachment, index) => (
                        <a
                          href={supportImageURL(attachment.id)}
                          key={attachment.id}
                          rel="noreferrer"
                          target="_blank"
                        >
                          <img
                            alt={l("Attached evidence image {number}", "첨부 증거 이미지 {number}", { number: index + 1 })}
                            height={attachment.height}
                            loading="lazy"
                            src={supportImageURL(attachment.id)}
                            width={attachment.width}
                          />
                        </a>
                      ))}
                    </div>
                  ) : null}
                  {message.agencyOrderId ? (
                    // 서버→사용자 단방향 주문 링크(ADR-0059 §5): 구조화 참조가
                    // 있을 때만 렌더한다. 본문 텍스트는 파싱하지 않는다.
                    <Link
                      className="support-chat__order-ref"
                      to={`/agencyOrder/${message.agencyOrderId}`}
                    >
                      {l("View related order", "관련 주문 보기")}
                    </Link>
                  ) : null}
                  <time dateTime={message.createdAt}>
                    {timeFormat.format(new Date(message.createdAt))}
                  </time>
                </div>
              </li>
            ))}
          </ol>
          {attachOpen ? (
            <div className="support-chat__attach" data-testid="support-attach-picker">
              <strong>{l("Choose an order for this inquiry", "문의할 주문 선택")}</strong>
              {attachError ? <small role="alert">{attachError}</small> : null}
              {attachable !== null && attachable.length === 0 ? (
                <small>{l("No orders are available to attach yet.", "첨부할 주문이 아직 없습니다.")}</small>
              ) : null}
              <ul>
                {(attachable ?? []).map((order) => (
                  <li key={order.id}>
                    <Button
                      aria-pressed={attached?.id === order.id}
                      emphasis="quiet"
                      onClick={() => {
                        setAttached(order);
                        setAttachOpen(false);
                      }}
                      size="compact"
                      type="button"
                    >
                      <span>{order.label}</span>
                      <small>
                        {timeFormat.format(new Date(order.issuedAt))}
                      </small>
                    </Button>
                  </li>
                ))}
              </ul>
            </div>
          ) : null}
          {attached ? (
            <div className="support-chat__attach-chip">
              <span>{l("Attached order", "주문 첨부")} · {attached.label}</span>
              <Button
                aria-label={l("Remove attached order", "주문 첨부 해제")}
                emphasis="quiet"
                onClick={() => setAttached(null)}
                size="compact"
                type="button"
              >
                <CloseIcon />
              </Button>
            </div>
          ) : null}
          {images.length > 0 ? (
            <div className="support-chat__image-chips">
              <span>{l("{count} images attached", "이미지 {count}장 첨부", { count: images.length })}</span>
              <Button
                aria-label={l("Remove attached images", "이미지 첨부 해제")}
                emphasis="quiet"
                onClick={() => {
                  setImages([]);
                  setImageError(null);
                  if (imageInputRef.current) imageInputRef.current.value = "";
                }}
                size="compact"
                type="button"
              >
                <CloseIcon />
              </Button>
            </div>
          ) : null}
          <div className="support-chat__composer">
            <div className="support-chat__composer-tools">
              <Button
                aria-expanded={attachOpen}
                aria-label={l("Attach order", "주문 첨부")}
                className="support-chat__attach-toggle"
                emphasis="quiet"
                onClick={() => void toggleAttach()}
                size="compact"
                type="button"
              >
                <OrderIcon /> <span>{l("Order", "주문")}</span>
              </Button>
              <Input
                accept="image/jpeg,image/png"
                aria-label={l("Choose evidence images", "증거 이미지 선택")}
                className="support-chat__file-input"
                multiple
                onChange={(event) => selectImages(event.target.files)}
                ref={imageInputRef}
                type="file"
              />
              <Button
                aria-label={l("Attach evidence images", "증거 이미지 첨부")}
                className="support-chat__image-toggle"
                emphasis="quiet"
                onClick={() => imageInputRef.current?.click()}
                size="compact"
                type="button"
              >
                <ImageIcon /> <span>{l("Picture", "사진")}</span>
              </Button>
            </div>
            {imageError ? (
              <p className="vt-field__error" role="status">{imageError}</p>
            ) : null}
            <div className="support-chat__composer-row">
              <Textarea
                aria-label={l("Message", "메시지 입력")}
                maxLength={2000}
                onChange={(event) => setDraft(event.target.value)}
                onKeyDown={(event) => {
                  if (
                    event.key === "Enter" &&
                    !event.shiftKey &&
                    !event.nativeEvent.isComposing
                  ) {
                    event.preventDefault();
                    void send();
                  }
                }}
                placeholder={l("Ask us anything", "무엇이든 편하게 물어보세요")}
                rows={1}
                value={draft}
              />
              <Button
                aria-label={l("Send message", "메시지 보내기")}
                busy={sending}
                disabled={!draft.trim()}
                onClick={() => void send()}
                type="button"
              >
                {l("Send", "보내기")}
              </Button>
            </div>
          </div>
        </section>
      )}
      <Button
        aria-expanded={widget === "open"}
        aria-label={l("Open Messages", "메시지 열기")}
        className="support-chat__launcher"
        emphasis="quiet"
        onClick={() =>
          setWidget((current) => (current === "open" ? "launcher" : "open"))
        }
        type="button"
      >
        <SupportMascot className="support-chat__launcher-mascot" />
        {unread > 0 && widget !== "open" ? (
          <span aria-hidden="true" className="support-chat__launcher-dot" />
        ) : null}
      </Button>
    </div>
  );
}

function SupportBusinessCardView({ card, agencyOrderId, resolved, onResolved }: {
  card: SupportBusinessCard;
  agencyOrderId?: string;
  resolved: boolean;
  onResolved: () => Promise<void>;
}) {
  const { l, locale } = useLocale();
  const payload = card.publicPayload ?? {};
  const [text, setText] = useState("");
  const [choice, setChoice] = useState("");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string>();
  const [pendingReceipt,setPendingReceipt]=useState<ProcessReceipt>();
  useEffect(()=>{
    if(!pendingReceipt || ["COMPLETED","REJECTED"].includes(pendingReceipt.outcome))return;
    let disposed=false;
    const timer=window.setInterval(async()=>{
      try{
        const receipt=await getProcessReceipt(pendingReceipt);
        if(disposed)return;
        setPendingReceipt(receipt);
        if(receipt.outcome==="COMPLETED"){await onResolved();window.dispatchEvent(new CustomEvent(supportSummaryRefreshEvent));}
      }catch{/* A lost refresh never resubmits the saved response. */}
    },2000);
    return()=>{disposed=true;window.clearInterval(timer);};
  },[pendingReceipt,onResolved]);
  const requestID = stringField(payload, "requestId") || card.reference.id;
  const responseType = stringField(payload, "responseType");
  const options = arrayField(payload, "responseOptions");
  const version = numberField(payload, "version");
  const response = objectField(payload, "response");
  const recordedResponse = responseValue(response, l);
  const refundItems = objectArrayField(payload, "items");
  const delayedDays = numberField(payload, "delayedDays");
  const amountMinor = numberField(payload, "amountMinor");
  const currency = stringField(payload, "currency");
  const actionOpen = card.type === "PROCUREMENT_REQUEST" && card.actionRequired && !resolved && (!pendingReceipt || pendingReceipt.outcome==="REJECTED");

  async function respond(response: Record<string, unknown>, decline = false) {
    if (!agencyOrderId || !requestID || version < 1 || busy) return;
    setBusy(true); setError(undefined);
    try {
      await respondToProcurementRequest(agencyOrderId, requestID, version, response, decline);
      await onResolved();
      window.dispatchEvent(new CustomEvent(supportSummaryRefreshEvent));
    } catch (caught) {
      if(caught instanceof ProcessRequestPending || caught instanceof ProcessRequestRejected){setPendingReceipt(caught.receipt);return;}
      setError(l("We couldn't send this response. Refresh Messages and try again.", "이 응답을 보내지 못했습니다. 메시지를 새로고침한 뒤 다시 시도해 주세요."));
    } finally {
      setBusy(false);
    }
  }

  return <section className={`support-chat__business-card type-${card.type.toLowerCase()}`}>
    <strong>{supportCardTitle(card.type, l)}</strong>
    {pendingReceipt ? <p role="status">{processProgressLabel(pendingReceipt,l)}</p> : null}
    {stringField(payload, "prompt") ? <p>{stringField(payload, "prompt")}</p> : null}
    {stringField(payload, "publicContext") ? <p>{stringField(payload, "publicContext")}</p> : null}
    {stringField(payload, "publicRationale") ? <p>{stringField(payload, "publicRationale")}</p> : null}
    {recordedResponse ? <p><strong>{l("Your response", "내 응답")}</strong> · {recordedResponse}</p> : null}
    {refundItems.length > 0 ? <ul>{refundItems.map((item, index) => <li key={stringField(item, "itemId") || index}>
      <strong>{supportCardState(stringField(item, "state"), l)}</strong>
      {stringField(item, "publicRationale") ? ` · ${stringField(item, "publicRationale")}` : ""}
    </li>)}</ul> : null}
    {delayedDays > 0 ? <p>{l(
      "This order has been delayed for {count} days. You may cancel it at no cost from the related order.",
      "이 주문은 {count}일 지연되었습니다. 관련 주문에서 비용 없이 취소할 수 있습니다.",
      { count: delayedDays },
    )}</p> : null}
    {amountMinor > 0 && currency ? <p><strong>{l("Refunded amount", "환불액")}</strong> · {new Intl.NumberFormat(locale, { style: "currency", currency }).format(amountMinor / 100)}</p> : null}
    {stringField(payload, "providerRefundReference") ? <small>{l("Provider refund reference", "Provider 환불 reference")} · {stringField(payload, "providerRefundReference")}</small> : null}
    {stringField(payload, "state") ? <small>{supportCardState(stringField(payload, "state"), l)}</small> : null}
    {stringField(payload, "dueAt") ? <small>{l("Reply by {date}", "응답 기한 {date}", { date: new Date(stringField(payload, "dueAt")).toLocaleString(locale) })}</small> : null}
    {actionOpen && responseType === "TEXT" ? <>
      <Textarea aria-label={l("Your answer", "답변")} maxLength={2000} value={text} onChange={(event) => setText(event.target.value)} />
      <Button busy={busy} disabled={!text.trim()} onClick={() => void respond({ text: text.trim() })} size="compact" type="button">{l("Send answer", "답변 보내기")}</Button>
    </> : null}
    {actionOpen && responseType === "SINGLE_CHOICE" ? <>
      <NativeSelect aria-label={l("Choose an answer", "답변 선택")} value={choice} onChange={(event) => setChoice(event.target.value)}>
        <NativeSelectOption value="">{l("Choose", "선택")}</NativeSelectOption>
        {options.map((option) => <NativeSelectOption key={option} value={option}>{option}</NativeSelectOption>)}
      </NativeSelect>
      <Button busy={busy} disabled={!choice} onClick={() => void respond({ choice })} size="compact" type="button">{l("Send answer", "답변 보내기")}</Button>
    </> : null}
    {actionOpen && responseType === "BOOLEAN_CONSENT" ? <div className="support-chat__business-actions">
      <Button busy={busy} onClick={() => void respond({ accepted: true })} size="compact" type="button">{l("I agree", "동의합니다")}</Button>
      <Button busy={busy} emphasis="quiet" onClick={() => void respond({}, true)} size="compact" type="button">{l("Decline", "거절")}</Button>
    </div> : null}
    {resolved ? <small>{l("Response completed", "응답 완료")}</small> : null}
    {error ? <Notice tone="danger">{error}</Notice> : null}
  </section>;
}

function supportCardTitle(type: SupportBusinessCard["type"], l: Localize) {
  return {
    PROCUREMENT_REQUEST: l("Procurement question", "구매대행 질문"),
    PROCUREMENT_RESPONSE: l("Procurement response", "구매대행 응답"),
    PROCUREMENT_DECISION: l("Procurement decision", "구매대행 판단"),
    ORDER_CANCELLATION: l("Order cancelled", "주문 취소"),
    ORDER_CANCELLATION_DECLINED: l("Cancellation declined", "취소 요청 거절"),
    REFUND_REQUEST: l("Refund request", "환불 요청"),
    REFUND_DECISION: l("Refund decision", "환불 판단"),
    REFUND_STATUS: l("Refund status", "환불 상태"),
    PAYPAL_DISPUTE: l("PayPal dispute", "PayPal 분쟁"),
    PAYPAL_DISPUTE_STATUS: l("PayPal dispute update", "PayPal 분쟁 업데이트"),
    DELIVERY_DELAY: l("Delivery delay", "배송 지연"),
    DELIVERY_RESOLUTION: l("Delivery decision", "배송 판단"),
  }[type];
}

function supportCardState(state: string, l: Localize) {
  const labels: Record<string, string> = {
    PENDING: l("Waiting for your response", "응답 대기"),
    ANSWERED: l("Answered", "답변 완료"),
    DECLINED: l("Declined", "거절됨"),
    FAILED_NO_RESPONSE: l("Closed after no response", "무응답 종료"),
    CANCELLED: l("Cancelled", "취소됨"),
    APPROVED: l("Approved", "승인됨"),
    REJECTED: l("Rejected", "거절됨"),
    OPEN: l("Open", "진행 중"),
    RESOLVED: l("Resolved", "종결됨"),
    ELIGIBLE_FOR_DELAY_CANCELLATION: l("Cancellation available after delay", "배송 지연 취소 가능"),
  };
  return labels[state] ?? state;
}

function stringField(payload: Record<string, unknown>, key: string) {
  return typeof payload[key] === "string" ? payload[key] : "";
}
function numberField(payload: Record<string, unknown>, key: string) {
  return typeof payload[key] === "number" ? payload[key] : 0;
}
function arrayField(payload: Record<string, unknown>, key: string) {
  return Array.isArray(payload[key]) ? payload[key].filter((value): value is string => typeof value === "string") : [];
}

function objectField(payload: Record<string, unknown>, key: string) {
  const value = payload[key];
  return value && typeof value === "object" && !Array.isArray(value)
    ? value as Record<string, unknown>
    : {};
}

function objectArrayField(payload: Record<string, unknown>, key: string) {
  return Array.isArray(payload[key])
    ? payload[key].filter((value): value is Record<string, unknown> => Boolean(value) && typeof value === "object" && !Array.isArray(value))
    : [];
}

function responseValue(response: Record<string, unknown>, l: Localize) {
  if (typeof response.text === "string" && response.text.trim()) return response.text;
  if (typeof response.choice === "string" && response.choice.trim()) return response.choice;
  if (typeof response.accepted === "boolean") return response.accepted ? l("Agreed", "동의함") : l("Declined", "거절함");
  return "";
}

function ResizeHandleIcon() {
  return (
    <svg aria-hidden="true" viewBox="0 0 20 20">
      <path d="M4 12 12 4M4 17 17 4M10 17l7-7" />
    </svg>
  );
}

function ImageIcon() {
  return <svg aria-hidden="true" viewBox="0 0 20 20"><path d="M3 4.5h14v11H3zM5.5 13l3-3 2 2 1.5-1.5 2.5 2.5M7 8h.01" /></svg>;
}

function CloseIcon() {
  return (
    <svg aria-hidden="true" viewBox="0 0 20 20">
      <path d="m5 5 10 10M15 5 5 15" />
    </svg>
  );
}

function OrderIcon() {
  return (
    <svg aria-hidden="true" viewBox="0 0 20 20">
      <path d="M4 6.5 10 3l6 3.5v7L10 17l-6-3.5Zm6 3.5 6-3.5M10 10 4 6.5M10 10v7" />
    </svg>
  );
}
