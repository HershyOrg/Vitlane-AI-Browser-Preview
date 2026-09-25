package pii

import (
	"bytes"
	"context"
	"errors"
	"testing"
)

const (
	keyringTestKeyOne = "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA="
	keyringTestKeyTwo = "u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7u7s="
)

func TestKeyringRotatesWritesAndKeepsOldDecryption(t *testing.T) {
	ctx := context.Background()
	original, err := NewKeyringFromSingle(keyringTestKeyOne, "v1")
	if err != nil {
		t.Fatal(err)
	}
	sealed, err := original.Encrypt(ctx, []byte("Seoul, secret street 42"), "user-1")
	if err != nil {
		t.Fatal(err)
	}
	if sealed.KeyVersion != "v1" {
		t.Fatalf("key version=%q", sealed.KeyVersion)
	}

	rotated, err := NewKeyring(
		"v2:"+keyringTestKeyTwo+", v1:"+keyringTestKeyOne, "v2",
	)
	if err != nil {
		t.Fatal(err)
	}
	if rotated.ActiveVersion() != "v2" {
		t.Fatalf("active=%q", rotated.ActiveVersion())
	}
	// Old ciphertext keeps decrypting during rotation.
	plaintext, err := rotated.Decrypt(ctx, sealed, "user-1")
	if err != nil || !bytes.Equal(plaintext, []byte("Seoul, secret street 42")) {
		t.Fatalf("old-version decrypt=%q err=%v", plaintext, err)
	}
	// New writes carry the new version and old keyrings cannot read them.
	resealed, err := rotated.Encrypt(ctx, plaintext, "user-1")
	if err != nil || resealed.KeyVersion != "v2" {
		t.Fatalf("resealed=%#v err=%v", resealed, err)
	}
	if _, err := original.Decrypt(ctx, resealed, "user-1"); !errors.Is(
		err, ErrEncryptionKeyInvalid,
	) {
		t.Fatalf("v1-only keyring accepted v2 ciphertext: %v", err)
	}
	// Binding stays enforced across the ring.
	if _, err := rotated.Decrypt(ctx, sealed, "user-2"); err == nil {
		t.Fatal("wrong binding must not decrypt")
	}
}

func TestKeyringRejectsBrokenConfiguration(t *testing.T) {
	cases := []struct {
		name, keys, active string
	}{
		{"missing active", "v1:" + keyringTestKeyOne, "v2"},
		{"duplicate version", "v1:" + keyringTestKeyOne + ",v1:" + keyringTestKeyTwo, "v1"},
		{"malformed entry", keyringTestKeyOne, "v1"},
		{"empty", "", "v1"},
	}
	for _, testCase := range cases {
		if _, err := NewKeyring(testCase.keys, testCase.active); err == nil {
			t.Fatalf("%s: expected error", testCase.name)
		}
	}
}
