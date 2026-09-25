package postgres

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	accountapp "github.com/vitlane/vitlane/server/internal/account/app"
	sharedapp "github.com/vitlane/vitlane/server/internal/shared/app"
	sharedpostgres "github.com/vitlane/vitlane/server/internal/shared/infra/postgres"
)

type CapabilityVault struct {
	database *sharedpostgres.Database
	cipher   accountapp.PIICipher
	clock    sharedapp.Clock
	ids      sharedapp.IDGenerator
}

func NewCapabilityVault(database *sharedpostgres.Database, cipher accountapp.PIICipher,
	clock sharedapp.Clock, ids sharedapp.IDGenerator) *CapabilityVault {
	return &CapabilityVault{database: database, cipher: cipher, clock: clock, ids: ids}
}

func (v *CapabilityVault) Save(ctx context.Context, userID, sessionID, shopDomain, kind, raw string, expiresAt time.Time) (string, error) {
	encrypted, err := v.cipher.Encrypt(ctx, []byte(raw), userID)
	if err != nil {
		return "", err
	}
	var id string
	err = v.database.Queryer(ctx).QueryRowContext(ctx, `
		INSERT INTO agency_order_provider_capabilities(
			id,user_id,order_sheet_session_id,shop_domain,capability_kind,
			encrypted_payload,payload_nonce,key_version,payload_hmac,created_at,expires_at
		) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11)
		ON CONFLICT(order_sheet_session_id,shop_domain,capability_kind)
		DO UPDATE SET encrypted_payload=EXCLUDED.encrypted_payload,
			payload_nonce=EXCLUDED.payload_nonce,key_version=EXCLUDED.key_version,
			payload_hmac=EXCLUDED.payload_hmac,expires_at=EXCLUDED.expires_at
		RETURNING id
	`, v.ids.NewID(), userID, sessionID, shopDomain, kind, encrypted.Ciphertext,
		encrypted.Nonce, encrypted.KeyVersion, encrypted.Fingerprint, v.clock.Now(), expiresAt).Scan(&id)
	if err != nil {
		return "", fmt.Errorf("save provider capability: %w", err)
	}
	return id, nil
}

func (v *CapabilityVault) Load(ctx context.Context, userID, safeRef, kind string) (string, error) {
	var encrypted accountapp.EncryptedPII
	var owner string
	var expiresAt time.Time
	err := v.database.Queryer(ctx).QueryRowContext(ctx, `
		SELECT user_id,encrypted_payload,payload_nonce,key_version,payload_hmac,expires_at
		FROM agency_order_provider_capabilities
		WHERE id=$1 AND user_id=$2 AND capability_kind=$3
	`, safeRef, userID, kind).Scan(&owner, &encrypted.Ciphertext, &encrypted.Nonce,
		&encrypted.KeyVersion, &encrypted.Fingerprint, &expiresAt)
	if errors.Is(err, sql.ErrNoRows) || !v.clock.Now().Before(expiresAt) {
		return "", fmt.Errorf("provider capability unavailable")
	}
	if err != nil {
		return "", err
	}
	payload, err := v.cipher.Decrypt(ctx, encrypted, owner)
	if err != nil {
		return "", err
	}
	return string(payload), nil
}
