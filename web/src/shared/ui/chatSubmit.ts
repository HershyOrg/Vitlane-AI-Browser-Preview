import type { KeyboardEvent } from "react";

/** Enter submits; Shift+Enter and IME confirmation stay inside the input. */
export function submitChatOnEnter(event: KeyboardEvent<HTMLTextAreaElement>) {
  if (event.key !== "Enter" || event.shiftKey || event.nativeEvent.isComposing || event.keyCode === 229) return;
  event.preventDefault();
  event.currentTarget.form?.requestSubmit();
}
