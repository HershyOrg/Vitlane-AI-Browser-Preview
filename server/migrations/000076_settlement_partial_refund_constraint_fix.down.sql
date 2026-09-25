-- migration 75의 legacy REFUND hard cutover가 irreversible이고, 구 CHECK 복원은
-- REFUND_PARTIAL을 다시 막는다. 유효한 이전 schema가 없으므로 rollback을 가장하지
-- 않고 forward-only 경계를 명시한다.
DO $$
BEGIN
    RAISE EXCEPTION
        '000076 settlement partial-refund constraint fix is irreversible';
END
$$;
