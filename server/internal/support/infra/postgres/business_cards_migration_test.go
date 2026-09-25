package postgres_test

import (
	"os"
	"strings"
	"testing"
)

func TestSupportBusinessCardMigrationKeepsTypedQueuesAndEncryptedImages(t *testing.T) {
	payload, err := os.ReadFile("../../../../migrations/000084_support_business_cards_and_images.up.sql")
	if err != nil {
		t.Fatal(err)
	}
	source := string(payload)
	for _, required := range []string{
		"content_kind IN ('TEXT','BUSINESS_CARD')",
		"'PROCUREMENT_REQUEST'",
		"'REFUND_REQUEST'",
		"'PAYPAL_DISPUTE'",
		"'DELIVERY_DELAY'",
		"action_required BOOLEAN NOT NULL",
		"support_open_business_actions",
		"PRIMARY KEY (user_id, reference_type, reference_id)",
		"support_open_business_actions_card_reference_fk",
		"support_messages_resolves_card_reference_fk",
		"support_no_reply_resolutions_customer_text_fk",
		"support_image_attachments",
		"ordinal BETWEEN 1 AND 4",
		"byte_size BETWEEN 1 AND 5242880",
		"width::BIGINT * height::BIGINT <= 24000000",
		"ciphertext BYTEA",
		"retention_until TIMESTAMPTZ",
		"legal_hold BOOLEAN NOT NULL",
		"purged_at TIMESTAMPTZ",
	} {
		if !strings.Contains(source, required) {
			t.Fatalf("migration missing %q", required)
		}
	}
	for _, forbidden := range []string{
		"original_filename", "plaintext", "object_store", "s3_",
	} {
		if strings.Contains(strings.ToLower(source), forbidden) {
			t.Fatalf("migration contains forbidden plaintext/generic-file field %q", forbidden)
		}
	}
}
