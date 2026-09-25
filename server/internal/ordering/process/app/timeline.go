package app

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/vitlane/vitlane/server/internal/ordering/procmsg"
	"strings"
	"time"
)

// 운영자 timeline(ADR-0070 §4.7 — PR-7): 한 주문의 결정 원장(order_process_
// decisions) ⋈ 이벤트 ⋈ Effect를 시간순으로 읽는 read-only 사영이다. 리듀서가
// "무엇을 보고 무엇을 결정해 무엇을 시켰는가"를 결정 version 단위로 되짚을
// 수 있게 한다(타임트래블). payload는 procmsg 규격상 PII를 담지 않는다.

var ErrTimelineNotFound = errors.New("ORDER_PROCESS_TIMELINE_NOT_FOUND")

type TimelineEvent struct {
	FlowID              string          `json:"flowId,omitempty"`
	CausationEffectID   string          `json:"causationEffectId,omitempty"`
	SourceEntityVersion int64           `json:"sourceEntityVersion,omitempty"`
	ID                  int64           `json:"id"`
	Seq                 int64           `json:"seq"`
	Source              string          `json:"source"`
	Type                string          `json:"type"`
	Payload             json.RawMessage `json:"payload"`
	OccurredAt          time.Time       `json:"occurredAt"`
	RecordedAt          time.Time       `json:"recordedAt"`
	AppliedVersion      *int64          `json:"appliedVersion,omitempty"`
}

type TimelineEffect struct {
	procmsg.ProcessEffect
	DeliveryState string     `json:"deliveryState"`
	ClaimVersion  int64      `json:"claimVersion"`
	AttemptCount  int        `json:"attemptCount"`
	NextAttemptAt time.Time  `json:"nextAttemptAt"`
	ConsumedAt    *time.Time `json:"consumedAt,omitempty"`
}

type TimelineDecision struct {
	Version        int64           `json:"version"`
	SeqFrom        int64           `json:"seqFrom"`
	SeqTo          int64           `json:"seqTo"`
	StageBefore    string          `json:"stageBefore,omitempty"`
	StageAfter     string          `json:"stageAfter"`
	TerminalReason string          `json:"terminalReason,omitempty"`
	LastReasonCode string          `json:"lastReasonCode,omitempty"`
	StageChanged   bool            `json:"stageChanged"`
	MerchantOrders json.RawMessage `json:"merchantOrders"`
	Effects        json.RawMessage `json:"effects"`
	WakeAt         *time.Time      `json:"wakeAt,omitempty"`
	ProcessState   json.RawMessage `json:"processState"`
	DecidedAt      time.Time       `json:"decidedAt"`
}

type TimelineProcess struct {
	State          string     `json:"state"`
	TerminalReason string     `json:"terminalReason,omitempty"`
	LastReasonCode string     `json:"lastReasonCode,omitempty"`
	Version        int64      `json:"version"`
	LastAppliedSeq int64      `json:"lastAppliedSeq"`
	WakeAt         *time.Time `json:"wakeAt,omitempty"`
	UpdatedAt      time.Time  `json:"updatedAt"`
}

type OrderTimeline struct {
	Requests      []procmsg.RequestReceipt `json:"requests"`
	AgencyOrderID string                   `json:"agencyOrderId"`
	Process       TimelineProcess          `json:"process"`
	Decisions     []TimelineDecision       `json:"decisions"`
	Events        []TimelineEvent          `json:"events"`
	Effects       []TimelineEffect         `json:"effects"`
}

// TimelineRepository는 선택 확장이다 — 개입 저장소가 구현하면 timeline 열람이
// 열린다.
type TimelineRepository interface {
	LoadTimeline(ctx context.Context, agencyOrderID string) (OrderTimeline, bool, error)
}

func (s *OrderProcessor) Timeline(ctx context.Context, agencyOrderID string) (OrderTimeline, error) {
	agencyOrderID = strings.TrimSpace(agencyOrderID)
	if agencyOrderID == "" {
		return OrderTimeline{}, procmsg.ErrRequestInvalid
	}
	repository, ok := s.store.(TimelineRepository)
	if !ok {
		return OrderTimeline{}, ErrTimelineNotFound
	}
	timeline, found, err := repository.LoadTimeline(ctx, agencyOrderID)
	if err != nil {
		return OrderTimeline{}, err
	}
	if !found {
		return OrderTimeline{}, ErrTimelineNotFound
	}
	return timeline, nil
}
