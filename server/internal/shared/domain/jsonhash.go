package domain

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
)

// CanonicalJSONHash hashes JSON meaning rather than object key order. It is
// suitable for snapshots that pass through PostgreSQL jsonb before an adapter
// verifies them.
func CanonicalJSONHash(value any) (string, error) {
	payload, err := json.Marshal(value)
	if err != nil {
		return "", err
	}
	return CanonicalJSONHashBytes(payload)
}

func CanonicalJSONHashBytes(payload []byte) (string, error) {
	decoder := json.NewDecoder(bytes.NewReader(payload))
	decoder.UseNumber()
	var value any
	if err := decoder.Decode(&value); err != nil {
		return "", err
	}
	canonical, err := json.Marshal(value)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(canonical)
	return "0x" + hex.EncodeToString(sum[:]), nil
}
