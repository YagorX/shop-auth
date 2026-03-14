INSERT INTO roles (name, description)
VALUES
  ('user',  'default user role'),
  ('admin', 'administrator role')
ON CONFLICT (name) DO NOTHING;
