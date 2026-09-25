import { request } from "../../../../../shared/api/client";
import type { ShoppingSession } from "../../../../../shared/api/types";

export function getShoppingSession(
  sessionId: string,
): Promise<{ session: ShoppingSession }> {
  return request(`/api/v1/shopping-sessions/${sessionId}`);
}
