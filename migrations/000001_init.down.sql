DROP TABLE IF EXISTS system_metrics;
DROP TABLE IF EXISTS user_sessions;
DROP TABLE IF EXISTS audit_logs;
DROP TABLE IF EXISTS users;
DROP TABLE IF EXISTS roles;

DROP FUNCTION IF EXISTS set_updated_at();
-- pgcrypto extension можно не удалять (обычно оставляют), но если хочешь:
-- DROP EXTENSION IF EXISTS pgcrypto;
