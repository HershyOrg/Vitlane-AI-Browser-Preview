package infra

import "crypto/rand"

type CryptoSecretGenerator struct{}

func (CryptoSecretGenerator) Generate(size int) ([]byte, error) {
	value := make([]byte, size)
	if _, err := rand.Read(value); err != nil {
		return nil, err
	}
	return value, nil
}
