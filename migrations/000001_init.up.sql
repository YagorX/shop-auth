-- Extensions
CREATE EXTENSION IF NOT EXISTS pgcrypto;

-- updated_at helper
CREATE OR REPLACE FUNCTION set_updated_at()
RETURNS TRIGGER AS $$
BEGIN
  NEW.updated_at = NOW();
  RETURN NEW;
END;
$$ LANGUAGE plpgsql;

-- ROLES
CREATE TABLE IF NOT EXISTS roles (
  id          BIGSERIAL PRIMARY KEY,
  name        VARCHAR(50) NOT NULL UNIQUE,
  description TEXT
);

-- USERS
CREATE TABLE IF NOT EXISTS users (
  id            BIGSERIAL PRIMARY KEY,
  uuid          UUID NOT NULL UNIQUE DEFAULT gen_random_uuid(),
  username      VARCHAR(100) NOT NULL UNIQUE,
  email         VARCHAR(255) UNIQUE,
  password_hash TEXT NOT NULL,
  role_id       BIGINT REFERENCES roles(id),
  is_active     BOOLEAN NOT NULL DEFAULT TRUE,
  created_at    TIMESTAMPTZ NOT NULL DEFAULT NOW(),
  updated_at    TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

DROP TRIGGER IF EXISTS trg_users_set_updated_at ON users;
CREATE TRIGGER trg_users_set_updated_at
BEFORE UPDATE ON users
FOR EACH ROW
EXECUTE FUNCTION set_updated_at();

CREATE INDEX IF NOT EXISTS idx_users_role_id ON users(role_id);

-- AUDIT LOGS
CREATE TABLE IF NOT EXISTS audit_logs (
  id         BIGSERIAL PRIMARY KEY,
  user_uuid  UUID REFERENCES users(uuid),
  action     VARCHAR(100) NOT NULL,        -- REGISTER, LOGIN_OK, LOGIN_FAIL, REFRESH_OK, REFRESH_FAIL, LOGOUT, ...
  ip_address INET,
  user_agent TEXT,
  details    JSONB,
  created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_audit_user_uuid ON audit_logs(user_uuid);
CREATE INDEX IF NOT EXISTS idx_audit_created_at ON audit_logs(created_at);
CREATE INDEX IF NOT EXISTS idx_audit_action ON audit_logs(action);

-- USER SESSIONS (refresh token sessions)
CREATE TABLE IF NOT EXISTS user_sessions (
  id                 BIGSERIAL PRIMARY KEY,
  user_uuid          UUID NOT NULL REFERENCES users(uuid) ON DELETE CASCADE,

  refresh_token_hash TEXT NOT NULL UNIQUE,     -- sha256(refresh)
  replaced_by_hash   TEXT,                     -- for rotation/reuse detection

  expires_at         TIMESTAMPTZ NOT NULL,
  created_at         TIMESTAMPTZ NOT NULL DEFAULT NOW(),
  revoked_at         TIMESTAMPTZ,

  ip_address         INET,
  user_agent         TEXT,
  device_id          TEXT,
  app_id             BIGINT
);

CREATE INDEX IF NOT EXISTS idx_user_sessions_user_uuid ON user_sessions(user_uuid);
CREATE INDEX IF NOT EXISTS idx_user_sessions_expires_at ON user_sessions(expires_at);
CREATE INDEX IF NOT EXISTS idx_user_sessions_active ON user_sessions(user_uuid) WHERE revoked_at IS NULL;

-- SYSTEM METRICS
CREATE TABLE IF NOT EXISTS system_metrics (
  id           BIGSERIAL PRIMARY KEY,
  metric_name  VARCHAR(100) NOT NULL,
  metric_value DOUBLE PRECISION NOT NULL,
  labels       JSONB,
  created_at   TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_metrics_name ON system_metrics(metric_name);
CREATE INDEX IF NOT EXISTS idx_metrics_created_at ON system_metrics(created_at);

-- APPS (clients)
CREATE TABLE IF NOT EXISTS apps (
  id         BIGSERIAL PRIMARY KEY,
  name       VARCHAR(100) NOT NULL UNIQUE,
  secret     TEXT NOT NULL,
  created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
  updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

DROP TRIGGER IF EXISTS trg_apps_set_updated_at ON apps;
CREATE TRIGGER trg_apps_set_updated_at
BEFORE UPDATE ON apps
FOR EACH ROW
EXECUTE FUNCTION set_updated_at();

CREATE INDEX IF NOT EXISTS idx_apps_name ON apps(name);

INSERT INTO roles (name, description)
VALUES
  ('user',  'default user role'),
  ('admin', 'administrator role')
ON CONFLICT (name) DO NOTHING;

-- Seed default applications (clients)

INSERT INTO apps (id, name, secret)
VALUES
  (1, 'web-client',    'web-secret'),
  (2, 'mobile-client', 'mobile-secret'),  
  (100, 'test-app',    'test-secret')
ON CONFLICT (id) DO NOTHING;
