package procmsg

import "errors"

var ErrRequestNotFound = errors.New("ORDER_PROCESS_REQUEST_NOT_FOUND")

// Guidance is presentation-neutral. Web translates reason/action codes in both
// locales. WAIT is not retry permission, and ACCEPTED is not purchase completion.
type Guidance struct {
	ReasonCode     string `json:"reasonCode,omitempty"`
	WaitingFor     string `json:"waitingFor,omitempty"`
	CustomerAction string `json:"customerAction"`
	OperatorAction string `json:"operatorAction"`
}

type RequestReceipt struct {
	SchemaVersion   string      `json:"schemaVersion"`
	AgencyOrderID   string      `json:"agencyOrderId"`
	RequestID       string      `json:"requestId"`
	FlowID          string      `json:"flowId"`
	MerchantOrderID string      `json:"merchantOrderId,omitempty"`
	Kind            RequestKind `json:"kind"`
	Outcome         string      `json:"outcome"`
	Guidance        Guidance    `json:"guidance"`
}

// RequestSubmitter accepts authenticated inputs and returns a durable receipt.
// Implementations cannot be called inside an Owner transaction.
