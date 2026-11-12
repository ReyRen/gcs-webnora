CREATE TEMP TRIGGER reject_active_user_model_policy_downgrade
BEFORE DELETE ON user_model_reference_policy
WHEN OLD.policy_version >= 1
BEGIN
    SELECT RAISE(ABORT, 'User model reference policy is active; automatic downgrade is unsupported. Preserve current data and review model bindings.');
END;
DELETE FROM user_model_reference_policy;
DROP TABLE user_model_reference_policy;
