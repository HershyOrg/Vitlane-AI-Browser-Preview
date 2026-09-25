-- Fail closed instead of silently losing new communication evidence during a
-- rollback to the pre-card schema.
DO $$
BEGIN
    IF EXISTS (SELECT 1 FROM support_image_attachments) OR EXISTS (
        SELECT 1 FROM support_messages
        WHERE content_kind='BUSINESS_CARD' OR author='SYSTEM'
    ) THEN
        RAISE EXCEPTION 'cannot rollback support business cards/images with retained evidence';
    END IF;
END $$;

DROP TABLE support_image_attachments;
DROP INDEX idx_support_messages_action_required;
DROP TABLE support_open_business_actions;
DROP INDEX uq_support_messages_open_action_reference;
ALTER TABLE support_messages
    DROP CONSTRAINT support_messages_resolves_card_reference_fk,
    DROP CONSTRAINT support_messages_resolves_card_owner_fk;
ALTER TABLE support_no_reply_resolutions
    DROP CONSTRAINT support_no_reply_resolutions_customer_text_fk,
    DROP COLUMN content_kind,
    DROP COLUMN customer_author,
    ADD CONSTRAINT support_no_reply_resolutions_customer_message_id_fkey
        FOREIGN KEY (customer_message_id)
        REFERENCES support_messages(id) ON DELETE CASCADE;
DROP INDEX uq_support_messages_id_author_kind;
DROP INDEX uq_support_messages_business_reference;
DROP INDEX uq_support_messages_id_user;
DROP INDEX uq_support_messages_resolves_card;

ALTER TABLE support_messages
    DROP CONSTRAINT support_messages_content_shape_check,
    DROP CONSTRAINT support_messages_resolves_other_card_check,
    DROP CONSTRAINT support_messages_resolves_card_fk,
    DROP CONSTRAINT support_messages_author_check,
    DROP COLUMN request_hash,
    DROP COLUMN public_payload,
    DROP COLUMN resolves_card_id,
    DROP COLUMN action_required,
    DROP COLUMN business_reference_id,
    DROP COLUMN business_reference_type,
    DROP COLUMN business_card_type,
    DROP COLUMN content_kind;

ALTER TABLE support_messages
    ADD CONSTRAINT support_messages_author_check
        CHECK (author IN ('CUSTOMER','OPERATOR'));
