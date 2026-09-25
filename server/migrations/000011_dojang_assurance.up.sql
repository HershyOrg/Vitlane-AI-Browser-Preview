ALTER TABLE identity_assurances
    DROP CONSTRAINT IF EXISTS identity_assurances_level_check;

ALTER TABLE identity_assurances
    ADD CONSTRAINT identity_assurances_level_check
        CHECK (level IN (
            'DOJANG_VERIFIED_ADDRESS',
            'DOJANG_TEST_FAUCET',
            'MOCK_KYC'
        ));
