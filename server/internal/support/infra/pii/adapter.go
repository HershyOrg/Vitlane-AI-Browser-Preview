// Package pii adapts the existing versioned PII keyring contract to Support
// image encryption without giving Support a plaintext storage fallback.
package pii

import (
	"context"

	accountapp "github.com/vitlane/vitlane/server/internal/account/app"
	supportdomain "github.com/vitlane/vitlane/server/internal/support/domain"
)

type Adapter struct {
	delegate accountapp.PIICipher
}

func NewAdapter(delegate accountapp.PIICipher) *Adapter {
	return &Adapter{delegate: delegate}
}

func (a *Adapter) Encrypt(
	ctx context.Context,
	plaintext []byte,
	binding string,
) (supportdomain.EncryptedImage, error) {
	encrypted, err := a.delegate.Encrypt(ctx, plaintext, binding)
	if err != nil {
		return supportdomain.EncryptedImage{}, err
	}
	return supportdomain.EncryptedImage{
		Ciphertext: encrypted.Ciphertext, Nonce: encrypted.Nonce,
		KeyVersion: encrypted.KeyVersion, Fingerprint: encrypted.Fingerprint,
	}, nil
}

func (a *Adapter) Decrypt(
	ctx context.Context,
	encrypted supportdomain.EncryptedImage,
	binding string,
) ([]byte, error) {
	return a.delegate.Decrypt(ctx, accountapp.EncryptedPII{
		Ciphertext: encrypted.Ciphertext, Nonce: encrypted.Nonce,
		KeyVersion: encrypted.KeyVersion, Fingerprint: encrypted.Fingerprint,
	}, binding)
}
