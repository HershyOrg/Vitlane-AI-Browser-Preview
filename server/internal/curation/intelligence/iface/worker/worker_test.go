package worker

import (
	"testing"
	"time"
)

func TestIdleDispatchCadenceDoesNotHotPoll(t *testing.T) {
	if dispatchInterval < 3*time.Second {
		t.Fatalf("dispatch interval hot-polls an idle database: %s", dispatchInterval)
	}
}
