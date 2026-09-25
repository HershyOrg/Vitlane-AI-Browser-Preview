import { useCallback, useEffect, useState } from "react";
import { useCurrentUser } from "../../account/app/useCurrentUser";
import {
  Button,
  ButtonLink,
  FeedbackState,
  Input,
  Notice,
  PageHeader,
  Textarea,
} from "../../../shared/ui";
import {
  getSupportThread,
  handleSupportConversationWithoutReply,
  listSupportConversations,
  replySupportConversation,
  supportOperatorImageURL,
} from "../infra/supportOperatorApi";
import type {
  SupportBusinessCard,
  SupportConversation,
  SupportCustomerContact,
  SupportMessage,
} from "../domain/message";
import "./support-operator.css";
import { localizeFixedCopy, useLocale, type Localize } from "../../../shared/i18n";

type ConversationView = "AWAITING_REPLY" | "ACTION_REQUIRED" | "ALL";

export function customerLabel(customer?: SupportCustomerContact, l: Localize = localizeFixedCopy) {
  if (!customer || !customer.email) return l("Deleted user", "탈퇴한 사용자");
  return customer.displayName
    ? `${customer.displayName} · ${customer.email}`
    : customer.email;
}

// 고객 대화 운영(ADR-0059/0063) — 고객이 "메시지"로 보낸 내용에 사람이 직접
// 답한다. 탭 규칙(운영정합 2차 P5): 맨 왼쪽 탭(답변 대기)이 기본이다.
export function SupportOperatorPage() {
  const { l, locale } = useLocale();
  const timeFormat = new Intl.DateTimeFormat(locale, { dateStyle: "medium", timeStyle: "short" });
  const { user } = useCurrentUser();
  const allowed = Boolean(user?.marketingAdmin || user?.phase5Operator);
  const [view, setView] = useState<ConversationView>("AWAITING_REPLY");
  const [conversations, setConversations] = useState<SupportConversation[]>([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);
  const [selectedUserId, setSelectedUserId] = useState<string | null>(null);
  const [customer, setCustomer] = useState<SupportCustomerContact | undefined>();
  const [messages, setMessages] = useState<SupportMessage[]>([]);
  const [selectedAwaitingReply, setSelectedAwaitingReply] = useState(false);
  const [awaitingCustomerMessageId, setAwaitingCustomerMessageId] = useState<string>();
  const [selectedActionRequired, setSelectedActionRequired] = useState(false);
  const [drafts, setDrafts] = useState<Record<string, string>>({});
  const [replyImages, setReplyImages] = useState<Record<string, File[]>>({});
  const [sending, setSending] = useState(false);
  const [handlingWithoutReply, setHandlingWithoutReply] = useState(false);
  const [actionError, setActionError] = useState<string | null>(null);

  const loadConversations = useCallback(async () => {
    if (!allowed) {
      setLoading(false);
      return;
    }
    try {
      const result = await listSupportConversations(view);
      setConversations(result.conversations);
      setError(null);
    } catch {
      setError(l("We couldn't load customer conversations. Check your connection and try again.", "고객 대화 목록을 불러오지 못했습니다. 연결을 확인한 뒤 다시 시도해 주세요."));
    } finally {
      setLoading(false);
    }
  }, [allowed, l, view]);

  const loadThread = useCallback(async (userId: string) => {
    try {
      const result = await getSupportThread(userId);
      setCustomer(result.customer);
      setMessages(result.messages);
      setSelectedAwaitingReply(result.awaitingReply);
      setAwaitingCustomerMessageId(result.awaitingCustomerMessageId);
      setSelectedActionRequired(result.actionRequired);
      setActionError(null);
    } catch {
      setActionError(l("We couldn't load the conversation. Try again shortly.", "대화를 불러오지 못했습니다. 잠시 후 다시 시도해 주세요."));
    }
  }, [l]);

  useEffect(() => {
    setLoading(true);
    void loadConversations();
    const timer = window.setInterval(() => void loadConversations(), 15000);
    return () => window.clearInterval(timer);
  }, [loadConversations]);

  useEffect(() => {
    if (!selectedUserId) return;
    void loadThread(selectedUserId);
    const timer = window.setInterval(() => void loadThread(selectedUserId), 5000);
    return () => window.clearInterval(timer);
  }, [selectedUserId, loadThread]);

  async function sendReply() {
    if (!selectedUserId) return;
    const body = (drafts[selectedUserId] ?? "").trim();
    if (!body) return;
    setSending(true);
    try {
      await replySupportConversation(selectedUserId, body, replyImages[selectedUserId] ?? []);
      setDrafts((current) => ({ ...current, [selectedUserId]: "" }));
      setReplyImages((current) => ({ ...current, [selectedUserId]: [] }));
      await loadThread(selectedUserId);
      await loadConversations();
    } catch {
      setActionError(l("We couldn't send the reply. Try again.", "답변을 보내지 못했습니다. 다시 시도해 주세요."));
    } finally {
      setSending(false);
    }
  }

  async function handleWithoutReply() {
    if (!selectedUserId || !selectedAwaitingReply || !awaitingCustomerMessageId) return;
    setHandlingWithoutReply(true);
    try {
      await handleSupportConversationWithoutReply(selectedUserId, awaitingCustomerMessageId);
      setSelectedAwaitingReply(false);
      setAwaitingCustomerMessageId(undefined);
      await loadThread(selectedUserId);
      await loadConversations();
    } catch {
      setActionError(l("We couldn't mark this conversation handled. Refresh and try again.", "이 대화를 처리 완료로 바꾸지 못했습니다. 갱신한 뒤 다시 시도해 주세요."));
    } finally {
      setHandlingWithoutReply(false);
    }
  }

  if (!allowed) {
    return (
      <section className="catalog-ui-admin-main">
        <PageHeader
          eyebrow={l("Access", "접근 권한")}
          title={l("You can't view customer conversations", "고객 대화를 볼 수 없습니다")}
          description={l("This page is available only to authorized operators.", "이 화면은 허용된 운영자에게만 열립니다.")}
        />
        <ButtonLink href="/">{l("Go to shopping home", "구매 홈으로 이동")}</ButtonLink>
      </section>
    );
  }

  const views = [
    ["AWAITING_REPLY", l("Awaiting reply", "답변 대기")],
    ["ACTION_REQUIRED", l("Action required", "업무 처리 필요")],
    ["ALL", l("All", "전체")],
  ] as const;
  const awaitingCount = conversations.filter((item) => item.awaitingReply).length;
  const actionRequiredCount = conversations.filter((item) => item.actionRequired).length;
  const chronological = [...messages].reverse();

  return (
    // App 셸이 유일한 <main>을 소유한다 — 페이지 루트는 section이다.
    <section className="order-ui-operator-console support-operator">
      <PageHeader
        eyebrow="Support Ops"
        title={l("Customer conversations", "고객 대화")}
        description={l("Reply to customer messages and handle order action cards in one conversation. A conversation awaits reply until its latest customer message receives a reply or is marked as handled; customer actions use their own queue.", "고객 문의와 주문 행동 카드를 한 대화에서 처리합니다. 마지막 고객 메시지에 답변하거나 답변 완료로 표시하기 전까지 답변 대기로 표시되며, 고객 행동 요청은 별도 큐로 구분됩니다.")}
        secondaryActions={[{ label: l("Refresh now", "지금 갱신"), onClick: () => void loadConversations() }]}
        summary={
          <div className="order-ui-page-header-summary">
            <span>{l("Awaiting reply", "답변 대기")} <strong>{awaitingCount}</strong></span>
            <span>{l("Action required", "업무 처리 필요")} <strong>{actionRequiredCount}</strong></span>
            <span>{l("Conversations", "목록")} <strong>{conversations.length}</strong></span>
          </div>
        }
      />
      {error ? <Notice announce tone="danger">{error}</Notice> : null}
      {actionError ? <Notice announce tone="danger">{actionError}</Notice> : null}
      <div aria-label={l("Conversation view", "대화 보기")} className="workspace-history-views" role="group">
        {views.map(([value, label]) => (
          <Button
            aria-pressed={view === value}
            emphasis="quiet"
            key={value}
            onClick={() => setView(value)}
            size="compact"
            type="button"
          >
            {label}
          </Button>
        ))}
      </div>

      {loading && (
        <FeedbackState
          state="loading"
          description={l("Checking customer conversations.", "고객 대화를 확인하고 있습니다.")}
        />
      )}
      {!loading && !error && conversations.length === 0 && !selectedUserId && (
        <FeedbackState
          state="empty"
          title={view === "AWAITING_REPLY" ? l("No conversations are awaiting a reply", "답변을 기다리는 대화가 없습니다") : view === "ACTION_REQUIRED" ? l("No conversations require a business action", "업무 처리가 필요한 대화가 없습니다") : l("There are no customer conversations yet", "아직 고객 대화가 없습니다")}
          description={l("Customer messages and order cards appear here newest first.", "고객 메시지와 주문 카드가 최신 순서로 여기에 표시됩니다.")}
        />
      )}

      {/* 답변 직후 대화가 답변 대기 목록을 떠나도 열린 스레드는 유지한다 —
          목록이 비었다고 진행 중인 대화 화면을 뺏지 않는다. */}
      {!loading && (conversations.length > 0 || selectedUserId) && (
        <div className="support-operator__layout">
          <ul aria-label={l("Customer conversation list", "고객 대화 목록")} className="support-operator__list" data-testid="support-conversations">
            {conversations.map((conversation) => (
              <li key={conversation.userId}>
                <Button
                  aria-pressed={selectedUserId === conversation.userId}
                  className="support-operator__item"
                  emphasis="quiet"
                  onClick={() => {
                    setSelectedUserId(conversation.userId);
                    setMessages([]);
                    setCustomer(conversation.customer);
                    setSelectedAwaitingReply(conversation.awaitingReply);
                    setAwaitingCustomerMessageId(conversation.awaitingCustomerMessageId);
                    setSelectedActionRequired(Boolean(conversation.actionRequired));
                  }}
                  type="button"
                >
                  <span className="support-operator__item-head">
                    <strong>{customerLabel(conversation.customer, l)}</strong>
                    {conversation.awaitingReply ? (
                      <span className="support-operator__awaiting">{l("Awaiting reply", "답변 대기")}</span>
                    ) : null}
                    {conversation.actionRequired ? (
                      <span className="support-operator__awaiting">{l("Action required", "업무 처리 필요")}</span>
                    ) : null}
                  </span>
                  <span className="support-operator__preview">
                    {conversation.lastMessage.body}
                  </span>
                  <small>
                    {l("Latest sender: {sender}", "마지막 발신: {sender}", {
                      sender: conversation.lastMessage.author === "CUSTOMER"
                        ? l("Customer", "고객")
                        : conversation.lastMessage.author === "SYSTEM"
                          ? l("System", "시스템")
                          : l("Operator", "운영자"),
                    })} ·{" "}
                    {timeFormat.format(new Date(conversation.lastMessage.createdAt))}
                  </small>
                </Button>
              </li>
            ))}
          </ul>

          {selectedUserId ? (
            <section aria-label={l("Conversation thread", "대화 스레드")} className="support-operator__thread">
              <header>
                <strong>{customerLabel(customer, l)}</strong>
                {selectedAwaitingReply ? <span className="support-operator__awaiting">{l("Unhandled customer inquiry", "미처리 고객 문의")}</span> : null}
                {selectedActionRequired ? <span className="support-operator__awaiting">{l("Business action required", "업무 처리 필요")}</span> : null}
              </header>
              <ol className="support-operator__log" data-testid="support-thread-log">
                {chronological.map((message) => (
                  <li
                    className={[
                      "support-operator__bubble",
                      message.author === "CUSTOMER" ? "is-customer" : "is-operator",
                    ].join(" ")}
                    key={message.id}
                  >
                    <span className="support-operator__author">
                      {message.author === "CUSTOMER" ? l("Customer", "고객") : message.author === "SYSTEM" ? l("System", "시스템") : l("Operator", "운영자")}
                    </span>
                    <p>{message.body}</p>
                    {(message.attachments ?? []).length > 0 ? <div className="support-operator__images">
                      {(message.attachments ?? []).map((attachment, index) => <a href={supportOperatorImageURL(selectedUserId, attachment.id)} key={attachment.id} rel="noreferrer" target="_blank">
                        <img alt={l("Customer evidence image {number}", "고객 증거 이미지 {number}", { number: index + 1 })} height={attachment.height} loading="lazy" src={supportOperatorImageURL(selectedUserId, attachment.id)} width={attachment.width} />
                      </a>)}
                    </div> : null}
                    {message.businessCard ? <SupportOperatorBusinessCardView card={message.businessCard} l={l} /> : null}
                    {message.agencyOrderId ? (
                      <span className="support-operator__order-ref">
                        {l("Order attached", "주문 첨부")} · {message.agencyOrderId.slice(0, 8)}
                      </span>
                    ) : null}
                    <small>{timeFormat.format(new Date(message.createdAt))}</small>
                  </li>
                ))}
              </ol>
              <div className="support-operator__composer">
                <label>
                  {l("Send reply", "답변 보내기")}
                  <Textarea
                    maxLength={2000}
                    onChange={(event) =>
                      setDrafts((current) => ({
                        ...current,
                        [selectedUserId]: event.target.value,
                      }))
                    }
                    placeholder={l("Example: We found an inventory issue at the merchant. We'll update you again today.", "예: 확인해 보니 판매처 재고 문제였습니다. 오늘 중으로 다시 안내드릴게요.")}
                    value={drafts[selectedUserId] ?? ""}
                  />
                </label>
                <label>
                  {l("Attach evidence images", "증거 이미지 첨부")}
                  <Input
                    accept="image/jpeg,image/png"
                    aria-label={l("Attach reply evidence images", "답변 증거 이미지 첨부")}
                    key={`${selectedUserId}:${(replyImages[selectedUserId] ?? []).length}`}
                    multiple
                    onChange={(event) => {
                      const selected = Array.from(event.target.files ?? []);
                      const valid = selected.length <= 4 && selected.every((file) =>
                        (file.type === "image/jpeg" || file.type === "image/png") &&
                        file.size <= 5 * 1024 * 1024,
                      );
                      setReplyImages((current) => ({ ...current, [selectedUserId]: valid ? selected : [] }));
                      if (!valid) setActionError(l(
                        "Attach up to four JPEG or PNG images, no larger than 5 MB each.",
                        "JPEG 또는 PNG 이미지를 최대 4장, 장당 5MB 이하로 첨부해 주세요.",
                      ));
                      else setActionError(null);
                    }}
                    type="file"
                  />
                </label>
                {(replyImages[selectedUserId] ?? []).length > 0 ? <small>{l(
                  "{count} images attached",
                  "이미지 {count}장 첨부",
                  { count: replyImages[selectedUserId].length },
                )}</small> : null}
                <div className="support-operator__composer-actions">
                  {selectedAwaitingReply && awaitingCustomerMessageId ? (
                    <Button
                      busy={handlingWithoutReply}
                      className="support-operator__handle-without-reply"
                      disabled={sending}
                      emphasis="quiet"
                      onClick={() => void handleWithoutReply()}
                      type="button"
                    >
                      {l("Mark as handled", "답변 완료로 표시")}
                    </Button>
                  ) : null}
                  <Button
                    busy={sending}
                    disabled={handlingWithoutReply || !(drafts[selectedUserId] ?? "").trim()}
                    onClick={() => void sendReply()}
                    type="button"
                  >
                    {l("Send reply", "답변 발송")}
                  </Button>
                </div>
                {selectedAwaitingReply && awaitingCustomerMessageId ? <small>{l(
                  "No new message will be sent. Only this pending customer inquiry will be recorded as handled.",
                  "새 메시지를 보내지 않고 이 고객 문의만 처리 완료로 기록합니다.",
                )}</small> : null}
              </div>
            </section>
          ) : (
            <section aria-label={l("Conversation thread", "대화 스레드")} className="support-operator__thread is-idle">
              <FeedbackState
                state="empty"
                title={l("Select a conversation", "대화를 선택하세요")}
                description={l("Select a conversation from the left to open its messages and reply composer.", "왼쪽 목록에서 대화를 선택하면 메시지와 답변 입력이 열립니다.")}
              />
            </section>
          )}
        </div>
      )}
    </section>
  );
}

function supportOperatorCardLabel(type: SupportBusinessCard["type"], l: Localize) {
  const labels: Record<string, string> = {
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
  };
  return labels[type] ?? type;
}

function SupportOperatorBusinessCardView({ card, l }: { card: SupportBusinessCard; l: Localize }) {
  const payload = card.publicPayload ?? {};
  const response = objectField(payload, "response");
  const responseText = responseValue(response, l);
  const items = objectArrayField(payload, "items");
  return <section className="support-operator__business-card">
    <strong>{supportOperatorCardLabel(card.type, l)}{card.actionRequired ? ` · ${l("Action required", "업무 처리 필요")}` : ""}</strong>
    {stringField(payload, "prompt") ? <p>{stringField(payload, "prompt")}</p> : null}
    {stringField(payload, "publicContext") ? <p>{stringField(payload, "publicContext")}</p> : null}
    {stringField(payload, "publicRationale") ? <p>{stringField(payload, "publicRationale")}</p> : null}
    {responseText ? <p><strong>{l("Customer response", "고객 응답")}</strong> · {responseText}</p> : null}
    {items.length > 0 ? <ul>{items.map((item, index) => <li key={stringField(item, "itemId") || index}>
      {supportOperatorCardState(stringField(item, "state"), l) || l("Refund item", "환불 항목")}
      {stringField(item, "publicRationale") ? ` · ${stringField(item, "publicRationale")}` : ""}
    </li>)}</ul> : null}
    {numberField(payload, "delayedDays") > 0 ? <p>{l(
      "Delayed {count} days · the customer may cancel at no cost from the related order.",
      "{count}일 지연 · 고객은 관련 주문에서 비용 없이 취소할 수 있습니다.",
      { count: numberField(payload, "delayedDays") },
    )}</p> : null}
    {stringField(payload, "state") ? <small>{supportOperatorCardState(stringField(payload, "state"), l)}</small> : null}
  </section>;
}

function stringField(payload: Record<string, unknown>, key: string) {
  return typeof payload[key] === "string" ? payload[key] : "";
}

function numberField(payload: Record<string, unknown>, key: string) {
  return typeof payload[key] === "number" ? payload[key] : 0;
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

function supportOperatorCardState(state: string, l: Localize) {
  const labels: Record<string, string> = {
    PENDING: l("Waiting for customer", "고객 응답 대기"),
    ANSWERED: l("Answered", "답변 완료"),
    DECLINED: l("Declined", "거절됨"),
    FAILED_NO_RESPONSE: l("Closed after no response", "무응답 종료"),
    CANCELLED: l("Cancelled", "취소됨"),
    APPROVED: l("Approved", "승인됨"),
    REJECTED: l("Rejected", "거절됨"),
    OPEN: l("Open", "진행 중"),
    RESOLVED: l("Resolved", "종결됨"),
    ELIGIBLE_FOR_DELAY_CANCELLATION: l("Eligible for delay cancellation", "배송 지연 취소 가능"),
  };
  return labels[state] ?? state;
}
