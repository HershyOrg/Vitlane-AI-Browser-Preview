package app

import (
	"context"
	"time"
)

// ResearchRoundOutcome is the durable per-Round record written in the same
// transaction that completes the Round. It answers the operator questions the
// pool's latest-only coverage cannot: which source failed in which Round, how
// many products were observed versus admitted, and how many of the admitted
// products carry an evaluation.
type ResearchRoundOutcome struct {
	RoundID          string
	UserID           string
	CurationID       string
	TargetID         string
	AttemptID        string
	Country          string
	Mode             string
	ObservedCount    int
	DuplicateCount   int
	RejectedCount    int
	AdmittedCount    int
	EvaluatedCount   int
	UnevaluatedCount int
	SourceCoverage   []SourceCoverage
	Duration         time.Duration
	CompletedAt      time.Time
}

// roundOutcomeRepository is optional: a repository that cannot persist
// outcomes does not block Round completion.
type roundOutcomeRepository interface {
	SaveRoundOutcome(context.Context, ResearchRoundOutcome) error
}

const ResearchRoundSummarySchemaVersion = "vitlane.research-round-summary.v1"

// ResearchRoundSummary is the operator read model over a recent window. Every
// number comes from durable ledgers (research_rounds, research_round_outcomes,
// intelligence_steps, research_catalog_api_calls); nothing is sampled.
type ResearchRoundSummary struct {
	Routes        []ResearchRouteSummary      `json:"routes"`
	SchemaVersion string                      `json:"schemaVersion"`
	WindowDays    int                         `json:"windowDays"`
	Since         time.Time                   `json:"since"`
	GeneratedAt   time.Time                   `json:"generatedAt"`
	Rounds        []ResearchRoundStatusCount  `json:"rounds"`
	Failures      []ResearchRoundFailureCount `json:"failures"`
	Sources       []ResearchRoundSourceCount  `json:"sources"`
	APICalls      []CatalogAPICallCount       `json:"apiCalls"`
	Admitted      ResearchAdmittedSummary     `json:"admitted"`
	Evaluation    ResearchEvaluationSummary   `json:"evaluation"`
	// Steps, Attempts and Reservations answer the performance questions the
	// Round row cannot: how long each pipeline stage takes, which attempts
	// ended in QUOTA_EXCEEDED or EFFECT_UNKNOWN before the Round's final
	// reason, and how much model budget is held, settled or stuck UNKNOWN.
	Steps        []ResearchStepDuration    `json:"steps"`
	Attempts     []ResearchAttemptCount    `json:"attempts"`
	Reservations []ManagedReservationCount `json:"reservations"`
}

// ResearchStepDuration is the wall time of one Intelligence step kind over
// research jobs in the window. Percentiles use completed steps only.
type ResearchStepDuration struct {
	Kind       string  `json:"kind"`
	Count      int64   `json:"count"`
	P50Seconds float64 `json:"p50Seconds"`
	P95Seconds float64 `json:"p95Seconds"`
}

// ResearchAttemptCount groups research attempts by status and failure code.
// A Round keeps only its final reason, so an attempt that stopped on the user
// budget or parked as EFFECT_UNKNOWN is visible only here.
type ResearchAttemptCount struct {
	Status      string `json:"status"`
	FailureCode string `json:"failureCode"`
	Count       int64  `json:"count"`
}

// ManagedReservationCount groups the managed model budget ledger by
// reservation status. UNKNOWN rows keep their headroom held for the day.
type ManagedReservationCount struct {
	Status       string `json:"status"`
	Count        int64  `json:"count"`
	AmountMicros int64  `json:"amountMicros"`
}

type ResearchRoundStatusCount struct {
	Country string `json:"country"`
	Status  string `json:"status"`
	Count   int64  `json:"count"`
}

// ResearchRoundFailureCount groups failed Rounds by the recorded reason and by
// the pipeline step that was open when the attempt gave up. StepKind is empty
// when no Intelligence step recorded the failure.
type ResearchRoundFailureCount struct {
	FailureCode string `json:"failureCode"`
	StepKind    string `json:"stepKind"`
	Retryable   bool   `json:"retryable"`
	Count       int64  `json:"count"`
}

type ResearchRoundSourceCount struct {
	Country    string `json:"country"`
	Source     string `json:"source"`
	Status     string `json:"status"`
	ReasonCode string `json:"reasonCode"`
	Count      int64  `json:"count"`
}

// CatalogAPICallCount groups the provider call ledger by API product and
// outcome. Non-billable rows are local admission denials, not upstream calls.
type CatalogAPICallCount struct {
	APIID    string `json:"apiId"`
	Outcome  string `json:"outcome"`
	Billable bool   `json:"billable"`
	Count    int64  `json:"count"`
}

type ResearchAdmittedBucket struct {
	Label string `json:"label"`
	Count int64  `json:"count"`
}

type ResearchAdmittedSummary struct {
	Rounds  int64                    `json:"rounds"`
	Median  float64                  `json:"median"`
	Buckets []ResearchAdmittedBucket `json:"buckets"`
}

type ResearchEvaluationSummary struct {
	Evaluated   int64 `json:"evaluated"`
	Unevaluated int64 `json:"unevaluated"`
}

type ResearchRoundSummaryReader interface {
	ReadResearchRoundSummary(context.Context, time.Time, time.Time) (ResearchRoundSummary, error)
}

type ResearchRouteSummary struct {
	RouteID       string  `json:"routeId"`
	PolicyVersion string  `json:"policyVersion"`
	Vertical      string  `json:"productVertical"`
	Decision      string  `json:"decision"`
	Reason        string  `json:"reason"`
	Count         int64   `json:"count"`
	MeanPressure  float64 `json:"meanPressure"`
}
