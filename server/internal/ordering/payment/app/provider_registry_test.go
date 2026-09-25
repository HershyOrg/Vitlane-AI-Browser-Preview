package app

import "testing"

func TestProviderRegistrySeparatesEnvironmentsAndEffectGates(t *testing.T) {
	sandbox := &fakeProvider{}
	live := &fakeProvider{}
	registry, err := NewProviderRegistry(
		ProviderRegistration{Environment: "sandbox", Client: sandbox, WebhookID: "WH-S", IssueEnabled: true, CaptureEnabled: true},
		ProviderRegistration{Environment: "live", Client: live, WebhookID: "WH-L"},
	)
	if err != nil {
		t.Fatal(err)
	}
	gotSandbox, ok := registry.Get("SANDBOX")
	if !ok || gotSandbox.Client != sandbox || !gotSandbox.CaptureEnabled {
		t.Fatalf("sandbox registration=%+v found=%v", gotSandbox, ok)
	}
	gotLive, ok := registry.Get("LIVE")
	if !ok || gotLive.Client != live || gotLive.IssueEnabled || gotLive.CaptureEnabled {
		t.Fatalf("dormant Live registration=%+v found=%v", gotLive, ok)
	}
}
