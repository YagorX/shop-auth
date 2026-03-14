-- Seed default applications (clients)

INSERT INTO apps (id, name, secret)
VALUES
  (1, 'web-client',    'web-secret'),
  (2, 'mobile-client', 'mobile-secret'),
  (100, 'test-app',    'test-secret')
ON CONFLICT (id) DO NOTHING;
