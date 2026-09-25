import type { SupportMessage } from "./message";

// 폴링 병합(ADR-0059) — 서버 목록(최신순)과 로컬 optimistic 추가를 id로
// 합치고 시간 오름차순으로 정렬해 렌더 순서를 만든다.
export function mergeMessages(
  existing: SupportMessage[],
  incoming: SupportMessage[],
): SupportMessage[] {
  const byId = new Map<string, SupportMessage>();
  for (const message of existing) byId.set(message.id, message);
  // 서버 응답이 항상 권위다 — 같은 id는 최신 서버 상태(readAt 등)로 덮는다.
  for (const message of incoming) byId.set(message.id, message);
  return [...byId.values()].sort((left, right) => {
    const byTime = left.createdAt.localeCompare(right.createdAt);
    return byTime !== 0 ? byTime : left.id.localeCompare(right.id);
  });
}

// 위젯이 열려 있는 동안의 안 읽음 판단 — 답변(비고객 발신) 중 읽음 워터마크가
// 아직 없는 메시지 수다. 전역 뱃지는 서버 summary COUNT가 권위다.
export function unreadReplyCount(messages: SupportMessage[]): number {
  return messages.filter(
    (message) => message.author !== "CUSTOMER" && !message.readAt,
  ).length;
}
