-- The Agent authority reset destroys token and activation credentials and
-- terminalizes in-flight Product work. Reconstructing those values from
-- tombstones would create false authority, so rollback is restore-only.
DO $$
BEGIN
    RAISE EXCEPTION
        '000030_agent_auth_control_legacy_reset is irreversible; restore the approved pre-cutover database backup together with the previous application image before traffic resumes'
        USING ERRCODE='55000';
END
$$;
