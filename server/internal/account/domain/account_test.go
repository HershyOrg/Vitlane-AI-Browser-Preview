package domain

import (
	"testing"
	"time"
)

func TestTestBuyerProfileContainsNoRealPIIAndHasStableSnapshot(t *testing.T) {
	now := time.Date(2026, 7, 24, 0, 0, 0, 0, time.UTC)
	first, err := NewTestBuyerProfile("profile-1", "user-1", now)
	if err != nil {
		t.Fatal(err)
	}
	second, err := NewTestBuyerProfile("profile-2", "user-2", now.Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if first.ProfileKind != BuyerProfileKindTest || first.ContainsRealPII ||
		!first.IsDefault || first.SnapshotHash == "" {
		t.Fatalf("unsafe TEST profile: %#v", first)
	}
	if first.SnapshotHash != second.SnapshotHash {
		t.Fatal("the same fixture version must produce the same content snapshot hash")
	}
}

func TestWalletOwnershipProofIsImmutableAndTimeBound(t *testing.T) {
	now := time.Date(2026, 7, 24, 0, 0, 0, 0, time.UTC)
	attempt, err := NewWalletRegistrationAttempt(
		"attempt-1",
		"user-1",
		"0x1111111111111111111111111111111111111111",
		"eip155:91342",
		"https://test.vitlane.example",
		"message",
		[]byte("message-hash"),
		[]byte("nonce-hash"),
		"attempt-operation-1",
		"0xrequest",
		now,
	)
	if err != nil {
		t.Fatal(err)
	}
	proof, err := NewWalletOwnershipProof(
		"proof-1", "wallet-1", attempt, "0xmessage", now,
	)
	if err != nil {
		t.Fatal(err)
	}
	if !proof.ValidAt(now.Add(23*time.Hour)) ||
		proof.ValidAt(now.Add(24*time.Hour)) {
		t.Fatalf("ownership proof validity boundary is wrong: %#v", proof)
	}
	if !proof.FreshAt(now.Add(10*time.Minute), 10*time.Minute) ||
		proof.FreshAt(now.Add(10*time.Minute+time.Nanosecond), 10*time.Minute) {
		t.Fatalf("ownership proof freshness boundary is wrong: %#v", proof)
	}
}

func TestShippingAddressNormalizesAndMasksWithoutReturningPII(t *testing.T) {
	address, err := (ShippingAddress{
		RecipientName: "  Private User  ",
		AddressLine1:  "  123 Test Road  ",
		City:          " Seoul ",
		Region:        " Seoul ",
		PostalCode:    " 06234 ",
		Country:       " kr ",
		Phone:         " 010-0000-0000 ",
	}).Normalize()
	if err != nil {
		t.Fatal(err)
	}
	if address.Country != "KR" || address.RecipientName != "Private User" {
		t.Fatalf("shipping normalization mismatch: %#v", address)
	}
	if got := address.MaskedSummary(); got != "KR · •••34" {
		t.Fatalf("shipping mask=%q", got)
	}
}

func TestShippingAddressRejectsIncompleteOrMalformedInput(t *testing.T) {
	for name, address := range map[string]ShippingAddress{
		"missing address": {
			RecipientName: "Private User", City: "Seoul", Region: "Seoul",
			PostalCode: "06234", Country: "KR",
		},
		"invalid country": {
			RecipientName: "Private User", AddressLine1: "123 Test Road",
			City: "Seoul", Region: "Seoul", PostalCode: "06234", Country: "KOR",
		},
		"invalid postal code": {
			RecipientName: "Private User", AddressLine1: "123 Test Road",
			City: "Seoul", Region: "Seoul", PostalCode: "*", Country: "KR",
		},
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := address.Normalize(); err != ErrShippingAddressInvalid {
				t.Fatalf("expected invalid shipping address, got %v", err)
			}
		})
	}
}
