package pii

import (
	"context"
	"encoding/base64"
	"testing"
)

func TestAESGCMEncryptsAndAuthenticatesShippingPayload(t *testing.T) {
	key := base64.StdEncoding.EncodeToString(make([]byte, 32))
	cipher, err := NewAESGCM(key, "test-v1")
	if err != nil {
		t.Fatal(err)
	}
	plaintext := []byte(`{"recipientName":"Private User","addressLine1":"1 Test Road"}`)
	first, err := cipher.Encrypt(context.Background(), plaintext, "user-1")
	if err != nil {
		t.Fatal(err)
	}
	second, err := cipher.Encrypt(context.Background(), plaintext, "user-1")
	if err != nil {
		t.Fatal(err)
	}
	if string(first.Ciphertext) == string(plaintext) ||
		string(first.Ciphertext) == string(second.Ciphertext) ||
		first.Fingerprint == second.Fingerprint {
		t.Fatal("ciphertext or randomized fingerprint boundary is invalid")
	}
	decrypted, err := cipher.Decrypt(context.Background(), first, "user-1")
	if err != nil || string(decrypted) != string(plaintext) {
		t.Fatalf("decrypt=%q err=%v", decrypted, err)
	}
	tampered := first
	tampered.Fingerprint = "hmac-sha256:tampered"
	if _, err := cipher.Decrypt(context.Background(), tampered, "user-1"); err == nil {
		t.Fatal("tampered shipping fingerprint was accepted")
	}
	if _, err := cipher.Decrypt(context.Background(), first, "user-2"); err == nil {
		t.Fatal("shipping ciphertext was accepted for a different user binding")
	}
}
