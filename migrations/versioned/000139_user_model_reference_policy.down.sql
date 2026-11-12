DO $$
BEGIN
    IF EXISTS (SELECT 1 FROM user_model_reference_policy WHERE policy_version >= 1) THEN
        RAISE EXCEPTION 'User model reference policy is active. Automatic downgrade is unsupported; preserve current data and review model bindings.';
    END IF;
END $$;
DROP TABLE user_model_reference_policy;
