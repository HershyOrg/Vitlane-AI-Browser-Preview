DO $$
BEGIN
    RAISE EXCEPTION
        '000031_wallet_identity_kyc_redesign is an irreversible destructive cutover; restore the approved pre-cutover backup before traffic resumes';
END
$$;
