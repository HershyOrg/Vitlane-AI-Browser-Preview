package pii

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"strings"

	accountapp "github.com/vitlane/vitlane/server/internal/account/app"
)

var ErrEncryptionKeyInvalid = errors.New("PII_ENCRYPTION_KEY_INVALID")

type AESGCM struct {
	aead       cipher.AEAD
	hmacKey    []byte
	keyVersion string
}

func NewAESGCM(encodedKey, keyVersion string) (*AESGCM, error) {
	key, err := decodeKey(strings.TrimSpace(encodedKey))
	if err != nil || len(key) != 32 || strings.TrimSpace(keyVersion) == "" {
		return nil, ErrEncryptionKeyInvalid
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	hmacKey := sha256.Sum256(append(append([]byte(nil), key...), []byte(":shipping-hmac")...))
	return &AESGCM{aead: aead, hmacKey: hmacKey[:], keyVersion: keyVersion}, nil
}

func (c *AESGCM) Encrypt(
	_ context.Context,
	plaintext []byte,
	binding string,
) (accountapp.EncryptedPII, error) {
	binding = strings.TrimSpace(binding)
	if binding == "" {
		return accountapp.EncryptedPII{}, ErrEncryptionKeyInvalid
	}
	nonce := make([]byte, c.aead.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return accountapp.EncryptedPII{}, err
	}
	associatedData := []byte(c.keyVersion + ":shipping-user:" + binding)
	ciphertext := c.aead.Seal(nil, nonce, plaintext, associatedData)
	mac := hmac.New(sha256.New, c.hmacKey)
	_, _ = mac.Write(associatedData)
	_, _ = mac.Write(nonce)
	_, _ = mac.Write(ciphertext)
	return accountapp.EncryptedPII{
		Ciphertext: ciphertext,
		Nonce:      nonce, KeyVersion: c.keyVersion,
		Fingerprint: "hmac-sha256:" + hex.EncodeToString(mac.Sum(nil)),
	}, nil
}

func (c *AESGCM) Decrypt(
	_ context.Context,
	encrypted accountapp.EncryptedPII,
	binding string,
) ([]byte, error) {
	binding = strings.TrimSpace(binding)
	if encrypted.KeyVersion != c.keyVersion || binding == "" {
		return nil, fmt.Errorf("%w: unsupported key version %q", ErrEncryptionKeyInvalid, encrypted.KeyVersion)
	}
	associatedData := []byte(encrypted.KeyVersion + ":shipping-user:" + binding)
	mac := hmac.New(sha256.New, c.hmacKey)
	_, _ = mac.Write(associatedData)
	_, _ = mac.Write(encrypted.Nonce)
	_, _ = mac.Write(encrypted.Ciphertext)
	expected := "hmac-sha256:" + hex.EncodeToString(mac.Sum(nil))
	if !hmac.Equal([]byte(expected), []byte(encrypted.Fingerprint)) {
		return nil, ErrEncryptionKeyInvalid
	}
	return c.aead.Open(
		nil, encrypted.Nonce, encrypted.Ciphertext, associatedData,
	)
}

func decodeKey(value string) ([]byte, error) {
	if decoded, err := base64.StdEncoding.DecodeString(value); err == nil {
		return decoded, nil
	}
	if decoded, err := hex.DecodeString(strings.TrimPrefix(value, "0x")); err == nil {
		return decoded, nil
	}
	return nil, ErrEncryptionKeyInvalid
}
