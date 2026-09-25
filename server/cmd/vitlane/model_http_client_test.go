package main

import (
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestManagedModelHeaderWaitUsesModelDeadline(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(40 * time.Millisecond)
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()
	shared := http.DefaultTransport.(*http.Transport).Clone()
	shared.ResponseHeaderTimeout = 10 * time.Millisecond
	client, err := managedModelHTTPClient(shared, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer client.CloseIdleConnections()
	response, err := client.Get(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	_, _ = io.Copy(io.Discard, response.Body)
	if shared.ResponseHeaderTimeout != 10*time.Millisecond {
		t.Fatal("mutated shared transport")
	}
	if client.Timeout != time.Second || client.Transport.(*http.Transport).ResponseHeaderTimeout != time.Second {
		t.Fatal("model timeout mismatch")
	}
}
