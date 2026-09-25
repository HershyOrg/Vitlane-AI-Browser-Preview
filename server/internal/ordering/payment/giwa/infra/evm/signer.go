package evm

import (
	"crypto/ecdsa"
	"fmt"
	"strings"

	"github.com/ethereum/go-ethereum/crypto"
)

type PrivateKeySigner struct {
	key *ecdsa.PrivateKey
}

func NewPrivateKeySignerFromFile(path string) (*PrivateKeySigner, error) {
	key, err := crypto.LoadECDSA(strings.TrimSpace(path))
	if err != nil {
		return nil, fmt.Errorf("load EVM signer key: %w", err)
	}
	return &PrivateKeySigner{key: key}, nil
}

func (s *PrivateKeySigner) Address() string {
	return crypto.PubkeyToAddress(s.key.PublicKey).Hex()
}

func (s *PrivateKeySigner) SignDigest(digest []byte) ([]byte, error) {
	if len(digest) != 32 {
		return nil, fmt.Errorf("EIP-712 digest must be 32 bytes")
	}
	return crypto.Sign(digest, s.key)
}

func (s *PrivateKeySigner) PrivateKey() *ecdsa.PrivateKey {
	return s.key
}
