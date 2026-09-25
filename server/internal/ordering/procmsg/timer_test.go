package procmsg

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestTimerImmediateAppendCannotPublishProcessState(t *testing.T) {
	for _, event := range []ProcessEvent{
		{Source: SourcePayment, Type: EventTimerFired},
		{Source: SourceTimer, Type: EventMerchantOrderStateChanged},
	} {
		if inserted, err := AppendTimerEvent(context.Background(), nil, event, time.Now()); inserted || !errors.Is(err, ErrEventInvalid) {
			t.Fatal(inserted, err)
		}
	}
}
