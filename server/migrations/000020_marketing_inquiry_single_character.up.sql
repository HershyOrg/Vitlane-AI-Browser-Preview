ALTER TABLE marketing_inquiries
    DROP CONSTRAINT IF EXISTS marketing_inquiries_message_check;

ALTER TABLE marketing_inquiries
    ADD CONSTRAINT marketing_inquiries_message_check
    CHECK (char_length(btrim(message)) BETWEEN 1 AND 2000);
