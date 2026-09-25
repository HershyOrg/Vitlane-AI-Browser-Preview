-- Support typed business communication cards remain inside the one
-- user-scoped conversation. They project Procurement/Payment/Logistics facts;
-- support_messages never becomes the source of those domain commands.

ALTER TABLE support_messages
    DROP CONSTRAINT support_messages_author_check;

ALTER TABLE support_messages
    ADD CONSTRAINT support_messages_author_check
        CHECK (author IN ('CUSTOMER','OPERATOR','SYSTEM')),
    ADD COLUMN content_kind TEXT NOT NULL DEFAULT 'TEXT'
        CHECK (content_kind IN ('TEXT','BUSINESS_CARD')),
    ADD COLUMN business_card_type TEXT,
    ADD COLUMN business_reference_type TEXT,
    ADD COLUMN business_reference_id TEXT,
    ADD COLUMN action_required BOOLEAN NOT NULL DEFAULT FALSE,
    ADD COLUMN resolves_card_id UUID,
    ADD COLUMN public_payload JSONB,
    ADD COLUMN request_hash TEXT
        CHECK (request_hash IS NULL OR request_hash ~ '^[0-9a-f]{64}$');

ALTER TABLE support_messages
    ADD CONSTRAINT support_messages_resolves_card_fk
        FOREIGN KEY (resolves_card_id)
        REFERENCES support_messages(id) ON DELETE RESTRICT,
    ADD CONSTRAINT support_messages_resolves_other_card_check
        CHECK (resolves_card_id IS NULL OR resolves_card_id <> id),
    ADD CONSTRAINT support_messages_content_shape_check CHECK (
        (
            content_kind = 'TEXT'
            AND author IN ('CUSTOMER','OPERATOR')
            AND business_card_type IS NULL
            AND business_reference_type IS NULL
            AND business_reference_id IS NULL
            AND action_required = FALSE
            AND resolves_card_id IS NULL
            AND public_payload IS NULL
        )
        OR
        (
            content_kind = 'BUSINESS_CARD'
            AND business_card_type IN (
                'PROCUREMENT_REQUEST','PROCUREMENT_RESPONSE','PROCUREMENT_DECISION',
                'REFUND_REQUEST','REFUND_DECISION','REFUND_STATUS',
                'PAYPAL_DISPUTE','PAYPAL_DISPUTE_STATUS',
                'DELIVERY_DELAY','DELIVERY_RESOLUTION'
            )
            AND business_reference_type IN (
                'PROCUREMENT','REFUND','PAYPAL_DISPUTE','DELIVERY'
            )
            AND (
                (business_reference_type='PROCUREMENT' AND business_card_type IN (
                    'PROCUREMENT_REQUEST','PROCUREMENT_RESPONSE','PROCUREMENT_DECISION'
                ))
                OR (business_reference_type='REFUND' AND business_card_type IN (
                    'REFUND_REQUEST','REFUND_DECISION','REFUND_STATUS'
                ))
                OR (business_reference_type='PAYPAL_DISPUTE' AND business_card_type IN (
                    'PAYPAL_DISPUTE','PAYPAL_DISPUTE_STATUS'
                ))
                OR (business_reference_type='DELIVERY' AND business_card_type IN (
                    'DELIVERY_DELAY','DELIVERY_RESOLUTION'
                ))
            )
            AND char_length(business_reference_id) BETWEEN 1 AND 160
            AND jsonb_typeof(public_payload) = 'object'
            AND octet_length(public_payload::text) <= 32768
            AND NOT (action_required AND resolves_card_id IS NOT NULL)
        )
    );

CREATE UNIQUE INDEX uq_support_messages_resolves_card
    ON support_messages(resolves_card_id)
    WHERE resolves_card_id IS NOT NULL;

-- Supports an owner-constrained attachment FK. The existing primary key is
-- still the canonical message identity.
CREATE UNIQUE INDEX uq_support_messages_id_user
    ON support_messages(id, user_id);

CREATE UNIQUE INDEX uq_support_messages_business_reference
    ON support_messages(
        id, user_id, business_reference_type, business_reference_id
    );

ALTER TABLE support_messages
    ADD CONSTRAINT support_messages_resolves_card_owner_fk
        FOREIGN KEY (resolves_card_id, user_id)
        REFERENCES support_messages(id, user_id) ON DELETE RESTRICT,
    ADD CONSTRAINT support_messages_resolves_card_reference_fk
        FOREIGN KEY (
            resolves_card_id, user_id,
            business_reference_type, business_reference_id
        ) REFERENCES support_messages(
            id, user_id, business_reference_type, business_reference_id
        ) ON DELETE RESTRICT;

CREATE UNIQUE INDEX uq_support_messages_open_action_reference
    ON support_messages(
        id, user_id, business_reference_type, business_reference_id,
        action_required
    );

-- Mutable open-pointer projection over immutable card messages. Its primary
-- key serializes one open action for an owning user/reference; resolving a
-- card deletes only this pointer while retaining both communication records.
CREATE TABLE support_open_business_actions (
    user_id UUID NOT NULL REFERENCES users(id) ON DELETE RESTRICT,
    reference_type TEXT NOT NULL CHECK (
        reference_type IN ('PROCUREMENT','REFUND','PAYPAL_DISPUTE','DELIVERY')
    ),
    reference_id TEXT NOT NULL CHECK (char_length(reference_id) BETWEEN 1 AND 160),
    card_message_id UUID NOT NULL,
    action_required BOOLEAN NOT NULL DEFAULT TRUE CHECK (action_required),
    opened_at TIMESTAMPTZ NOT NULL,
    PRIMARY KEY (user_id, reference_type, reference_id),
    UNIQUE (card_message_id),
    CONSTRAINT support_open_business_actions_card_reference_fk
        FOREIGN KEY (
            card_message_id, user_id, reference_type, reference_id,
            action_required
        ) REFERENCES support_messages(
            id, user_id, business_reference_type, business_reference_id,
            action_required
        ) ON DELETE CASCADE
);

-- ADR-0062 ordinary no-reply handling can reference CUSTOMER/TEXT only. The
-- composite FK makes it impossible for a direct SQL writer to use that record
-- to close a typed business action card.
CREATE UNIQUE INDEX uq_support_messages_id_author_kind
    ON support_messages(id, author, content_kind);

ALTER TABLE support_no_reply_resolutions
    DROP CONSTRAINT support_no_reply_resolutions_customer_message_id_fkey,
    ADD COLUMN customer_author TEXT NOT NULL DEFAULT 'CUSTOMER'
        CHECK (customer_author='CUSTOMER'),
    ADD COLUMN content_kind TEXT NOT NULL DEFAULT 'TEXT'
        CHECK (content_kind='TEXT'),
    ADD CONSTRAINT support_no_reply_resolutions_customer_text_fk
        FOREIGN KEY (customer_message_id, customer_author, content_kind)
        REFERENCES support_messages(id, author, content_kind) ON DELETE CASCADE;

CREATE TABLE support_image_attachments (
    id UUID PRIMARY KEY,
    message_id UUID NOT NULL,
    user_id UUID NOT NULL,
    ordinal SMALLINT NOT NULL CHECK (ordinal BETWEEN 1 AND 4),
    media_type TEXT NOT NULL CHECK (media_type IN ('image/jpeg','image/png')),
    width INTEGER NOT NULL CHECK (width > 0),
    height INTEGER NOT NULL CHECK (height > 0),
    byte_size INTEGER NOT NULL CHECK (byte_size BETWEEN 1 AND 5242880),
    content_sha256 TEXT NOT NULL CHECK (content_sha256 ~ '^[0-9a-f]{64}$'),
    ciphertext BYTEA,
    nonce BYTEA,
    key_version TEXT,
    ciphertext_fingerprint TEXT,
    created_at TIMESTAMPTZ NOT NULL,
    retention_until TIMESTAMPTZ,
    legal_hold BOOLEAN NOT NULL DEFAULT FALSE,
    legal_hold_reason_hash TEXT,
    purged_at TIMESTAMPTZ,
    CONSTRAINT support_image_attachments_message_owner_fk
        FOREIGN KEY (message_id, user_id)
        REFERENCES support_messages(id, user_id) ON DELETE CASCADE,
    CONSTRAINT support_image_attachments_message_ordinal_unique
        UNIQUE (message_id, ordinal),
    CONSTRAINT support_image_attachments_pixel_cap_check
        CHECK (width::BIGINT * height::BIGINT <= 24000000),
    CONSTRAINT support_image_attachments_retention_check
        CHECK (retention_until IS NULL OR retention_until > created_at),
    CONSTRAINT support_image_attachments_legal_hold_check
        CHECK (NOT legal_hold OR char_length(legal_hold_reason_hash) >= 16),
    CONSTRAINT support_image_attachments_ciphertext_lifecycle_check CHECK (
        (
            purged_at IS NULL
            AND ciphertext IS NOT NULL AND octet_length(ciphertext) > 0
            AND nonce IS NOT NULL AND octet_length(nonce) > 0
            AND char_length(key_version) > 0
            AND char_length(ciphertext_fingerprint) > 0
        )
        OR
        (
            purged_at IS NOT NULL
            AND ciphertext IS NULL AND nonce IS NULL
            AND key_version IS NULL AND ciphertext_fingerprint IS NULL
        )
    )
);

CREATE INDEX idx_support_image_attachments_owner
    ON support_image_attachments(user_id, created_at DESC, id);

CREATE INDEX idx_support_image_attachments_retention
    ON support_image_attachments(retention_until, id)
    WHERE purged_at IS NULL AND legal_hold = FALSE
      AND retention_until IS NOT NULL;

CREATE INDEX idx_support_messages_action_required
    ON support_messages(user_id, created_at DESC, id DESC)
    WHERE content_kind='BUSINESS_CARD' AND action_required=TRUE;
