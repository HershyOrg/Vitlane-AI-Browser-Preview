import type { ReactNode } from "react";
import type { ConversationMessage } from "../domain/conversation";
import { useLocale, type UILocale } from "../../../shared/i18n";

export function CurationConversationBubble({
  message,
  locale,
  children,
}: {
  message: ConversationMessage;
  locale: UILocale;
  children?: ReactNode;
}) {
  const { l } = useLocale();
  return (
    <div
      className={`curation-conversation-bubble is-${message.role.toLowerCase()}`}
      data-conversation-message-id={message.id}
      data-conversation-presentation={message.diff ? "diff" : "message"}
    >
      <header>
        <strong>
          {message.role === "VITLANE" && message.diff
            ? l("Vitlane", "Vitlane")
            : message.title || l("Me", "나")}
        </strong>
        <time dateTime={message.createdAt}>
          {formatTimelineTime(message.createdAt, locale)}
        </time>
      </header>
      <p>{message.body}</p>
      {children}
    </div>
  );
}

export function formatTimelineTime(value: string, locale: UILocale) {
  const date = new Date(value);
  if (Number.isNaN(date.valueOf())) return value;
  return new Intl.DateTimeFormat(locale, {
    month: "numeric",
    day: "numeric",
    hour: "2-digit",
    minute: "2-digit",
  }).format(date);
}
