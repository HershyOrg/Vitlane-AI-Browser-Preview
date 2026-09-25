DO $$
BEGIN
    RAISE EXCEPTION
        '000032_single_wallet_kyc_model is an irreversible destructive cutover; restore the approved pre-cutover backup before traffic resumes';
END
$$;
