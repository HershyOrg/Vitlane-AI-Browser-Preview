import { request } from "../../../../../shared/api/client";

export type APIUsageSummary = {
  inputTokens: number;
  outputTokens: number;
  totalTokens: number;
  requestCount: number;
  reservedMicros: number;
  settledMicros: number;
  spentMicros: number;
  usagePercent: number;
};

export type APIUsageDay = APIUsageSummary & {
  usageDate: string;
};

export type ServerAPIUsage = {
  enabled: boolean;
  timezone: "UTC";
  from: string;
  through: string;
  dailyLimitMicros: number;
  admissionLimitMicros: number;
  totals: APIUsageSummary;
  days: APIUsageDay[];
};

export type UnknownReservationOutcome = "SETTLED" | "RELEASED";

export type UnknownReservationResolutionInput = {
  reservationId: string;
  outcome: UnknownReservationOutcome;
  reasonDetail: string;
  evidenceReference: string;
  settledAmountMicros?: number;
};

export type UnknownReservationResolution = {
  reservationId: string;
  status: UnknownReservationOutcome;
  amountMicros: number;
  settledAmountMicros?: number;
  completedAt: string;
};

export function getServerAPIUsage(
  days: 7 | 30 | 90,
): Promise<ServerAPIUsage> {
  return request(`/api/v1/admin/managed-runner/usage?days=${days}`);
}

export function resolveUnknownReservation(
  input: UnknownReservationResolutionInput,
): Promise<UnknownReservationResolution> {
  const { reservationId, ...body } = input;
  return request(
    `/api/v1/admin/managed-runner/reservations/${encodeURIComponent(
      reservationId,
    )}/resolutions`,
    {
      method: "POST",
      body: JSON.stringify(body),
    },
  );
}
