package procmsg

import (
	"context"
	"encoding/json"
)

type RequestSubmitter interface {
	Submit(context.Context, ActionRequest) (RequestReceipt, error)
	Receipt(context.Context, string, string) (RequestReceipt, error)
}

// ActionInputs keeps the original private input in its owning product. Effects
// and events contain only an immutable reference and canonical hash.
type ActionInputs interface {
	Stage(context.Context, ActionRequest, any) (ActionRequest, error)
	Load(context.Context, ActionRequest) (json.RawMessage, error)
}
