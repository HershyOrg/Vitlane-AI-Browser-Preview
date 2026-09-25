package koreancatalog

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestOWNDetailCallsOverlapAndRespectRoundConcurrency(t *testing.T) {
	var mu sync.Mutex
	active, peak, calls := 0, 0, 0
	started := make(chan struct{}, 4)
	release := make(chan struct{})
	client := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		if strings.Contains(r.URL.Path, "product-details") {
			mu.Lock()
			active++
			calls++
			peak = max(peak, active)
			mu.Unlock()
			started <- struct{}{}
			select {
			case <-release:
			case <-r.Context().Done():
				return nil, r.Context().Err()
			}
			mu.Lock()
			active--
			mu.Unlock()
			return response(200, `{"status":"OK","data":{"offers":[]}}`, "application/json"), nil
		}
		return response(200, `{"status":"OK","data":{"products":[{"product_id":"1"},{"product_id":"2"},{"product_id":"3"},{"product_id":"4"},{"product_id":"5"}]}}`, "application/json"), nil
	})}
	g, err := New(Config{Enabled: true, OWNKey: "test", Control: &reviewControl{}, Client: client, DetailLimit: 4})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	ctx = context.WithValue(ctx, routeSlotsKey{}, make(chan struct{}, 2))
	done := make(chan error, 1)
	go func() { _, err := g.searchProducts(ctx, "milk"); done <- err }()
	for range 2 {
		select {
		case <-started:
		case <-ctx.Done():
			close(release)
			t.Fatal("details did not overlap")
		}
	}
	close(release)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if calls != 4 || peak != 2 {
		t.Fatal(fmt.Sprintf("calls=%d peak=%d", calls, peak))
	}
}
