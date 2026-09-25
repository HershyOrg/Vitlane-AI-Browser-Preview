-- The sidebar lists a user's active Curations newest first and pages by the
-- (created_at, id) pair. One partial index serves the first page, "more" and
-- the check for Curations created since the newest one a tab shows, so each
-- read walks one page of index entries however many Curations the user has
-- (ADR-0079).
CREATE INDEX curations_user_active_created_idx
    ON curations (user_id, created_at DESC, id DESC)
    WHERE archived_at IS NULL;
