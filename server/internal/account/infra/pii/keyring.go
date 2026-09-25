package pii

import (
	"context"
	"fmt"
	"sort"
	"strings"

	accountapp "github.com/vitlane/vitlane/server/internal/account/app"
)

// Keyring holds every PII key version that may still decrypt stored
// ciphertext plus the single active version for new writes (ADR-0040 §8).
// Rotation adds the new key as active, an online re-encryption pass rewrites
// old rows, and only then may the old version be removed from configuration.
type Keyring struct {
	active    *AESGCM
	byVersion map[string]*AESGCM
}

// NewKeyring parses "v2:<base64>,v1:<base64>" (versions must be unique) and
// requires activeVersion to be present. A single-key deployment is the
// one-entry special case built by NewKeyringFromSingle.
func NewKeyring(encodedKeys string, activeVersion string) (*Keyring, error) {
	entries := strings.Split(strings.TrimSpace(encodedKeys), ",")
	byVersion := make(map[string]*AESGCM, len(entries))
	for _, entry := range entries {
		entry = strings.TrimSpace(entry)
		if entry == "" {
			continue
		}
		version, encoded, found := strings.Cut(entry, ":")
		version = strings.TrimSpace(version)
		if !found || version == "" {
			return nil, fmt.Errorf(
				"%w: keyring entry needs <version>:<key>", ErrEncryptionKeyInvalid,
			)
		}
		if _, exists := byVersion[version]; exists {
			return nil, fmt.Errorf(
				"%w: duplicate key version %q", ErrEncryptionKeyInvalid, version,
			)
		}
		cipher, err := NewAESGCM(encoded, version)
		if err != nil {
			return nil, fmt.Errorf("key version %q: %w", version, err)
		}
		byVersion[version] = cipher
	}
	active, ok := byVersion[strings.TrimSpace(activeVersion)]
	if !ok {
		return nil, fmt.Errorf(
			"%w: active version %q is not in the keyring",
			ErrEncryptionKeyInvalid, activeVersion,
		)
	}
	return &Keyring{active: active, byVersion: byVersion}, nil
}

// NewKeyringFromSingle keeps the existing single-key configuration working
// unchanged: one version, active.
func NewKeyringFromSingle(encodedKey, keyVersion string) (*Keyring, error) {
	cipher, err := NewAESGCM(encodedKey, keyVersion)
	if err != nil {
		return nil, err
	}
	return &Keyring{
		active:    cipher,
		byVersion: map[string]*AESGCM{cipher.keyVersion: cipher},
	}, nil
}

func (k *Keyring) ActiveVersion() string {
	return k.active.keyVersion
}

// Versions lists every configured version, sorted for stable readbacks.
func (k *Keyring) Versions() []string {
	versions := make([]string, 0, len(k.byVersion))
	for version := range k.byVersion {
		versions = append(versions, version)
	}
	sort.Strings(versions)
	return versions
}

func (k *Keyring) Encrypt(
	ctx context.Context,
	plaintext []byte,
	binding string,
) (accountapp.EncryptedPII, error) {
	return k.active.Encrypt(ctx, plaintext, binding)
}

func (k *Keyring) Decrypt(
	ctx context.Context,
	encrypted accountapp.EncryptedPII,
	binding string,
) ([]byte, error) {
	cipher, ok := k.byVersion[encrypted.KeyVersion]
	if !ok {
		return nil, fmt.Errorf(
			"%w: unsupported key version %q",
			ErrEncryptionKeyInvalid, encrypted.KeyVersion,
		)
	}
	return cipher.Decrypt(ctx, encrypted, binding)
}
