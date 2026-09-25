package pii_test

import (
	"context"
	"testing"

	accountapp "github.com/vitlane/vitlane/server/internal/account/app"
	supportpii "github.com/vitlane/vitlane/server/internal/support/infra/pii"
)

type recordingCipher struct {
	binding string
}

func (c *recordingCipher) Encrypt(
	_ context.Context,
	plaintext []byte,
	binding string,
) (accountapp.EncryptedPII, error) {
	c.binding = binding
	return accountapp.EncryptedPII{
		Ciphertext: append([]byte("sealed:"), plaintext...),
		Nonce:      []byte("nonce"), KeyVersion: "v2", Fingerprint: "fingerprint",
	}, nil
}

func (c *recordingCipher) Decrypt(
	_ context.Context,
	encrypted accountapp.EncryptedPII,
	binding string,
) ([]byte, error) {
	c.binding = binding
	return append([]byte(nil), encrypted.Ciphertext[len("sealed:"):]...), nil
}

func TestAdapterUsesExistingPIIKeyringContractAndBinding(t *testing.T) {
	delegate := &recordingCipher{}
	adapter := supportpii.NewAdapter(delegate)
	encrypted, err := adapter.Encrypt(
		context.Background(), []byte("image"), "support-image:v1:u:m:a",
	)
	if err != nil || encrypted.KeyVersion != "v2" ||
		delegate.binding != "support-image:v1:u:m:a" {
		t.Fatalf("encrypted=%+v binding=%q err=%v", encrypted, delegate.binding, err)
	}
	plaintext, err := adapter.Decrypt(
		context.Background(), encrypted, "support-image:v1:u:m:a",
	)
	if err != nil || string(plaintext) != "image" ||
		delegate.binding != "support-image:v1:u:m:a" {
		t.Fatalf("plaintext=%q binding=%q err=%v", plaintext, delegate.binding, err)
	}
}
