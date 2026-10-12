CREATE EXTENSION IF NOT EXISTS postgis;
CREATE EXTENSION IF NOT EXISTS pg_trgm;

CREATE TYPE gender AS ENUM ('male', 'female', 'non_binary', 'prefer_not_to_say');
CREATE TYPE user_status AS ENUM ('active', 'pending_review', 'suspended', 'banned', 'deleted');
CREATE TYPE user_role AS ENUM ('user', 'super_admin');
CREATE TYPE preferred_foot AS ENUM ('left', 'right', 'both');

CREATE TABLE users (
  id uuid PRIMARY KEY,
  email text NOT NULL,
  email_verified_at timestamptz,
  password_hash text,
  apple_sub text UNIQUE,
  google_sub text UNIQUE,
  display_name text NOT NULL CHECK (char_length(display_name) BETWEEN 1 AND 50),
  avatar_url text,
  bio text CHECK (char_length(bio) <= 280),
  birth_date date NOT NULL,
  gender gender NOT NULL,
  show_gender boolean NOT NULL DEFAULT false,
  preferred_positions text[] NOT NULL DEFAULT '{}' CHECK (preferred_positions <@ ARRAY['GK','DEF','MID','FWD']),
  preferred_foot preferred_foot,
  home_location geography(Point, 4326) NOT NULL,
  locale text NOT NULL DEFAULT 'es-ES',
  status user_status NOT NULL DEFAULT 'active',
  role user_role NOT NULL DEFAULT 'user',
  totp_secret text,
  adult_attested_at timestamptz NOT NULL,
  terms_version_accepted text NOT NULL,
  created_at timestamptz NOT NULL DEFAULT now(),
  updated_at timestamptz NOT NULL DEFAULT now(),
  deleted_at timestamptz
);
CREATE UNIQUE INDEX users_email_key ON users (lower(email));

CREATE TABLE email_codes (
  user_id uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  purpose text NOT NULL CHECK (purpose IN ('verify_email', 'password_reset')),
  code_hash text NOT NULL,
  expires_at timestamptz NOT NULL,
  attempts int NOT NULL DEFAULT 0,
  created_at timestamptz NOT NULL DEFAULT now(),
  updated_at timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY (user_id, purpose)
);

CREATE TABLE refresh_tokens (
  id uuid PRIMARY KEY,
  user_id uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  family_id uuid NOT NULL,
  token_hash text NOT NULL UNIQUE,
  audience text NOT NULL,
  expires_at timestamptz NOT NULL,
  used_at timestamptz,
  revoked_at timestamptz,
  created_at timestamptz NOT NULL DEFAULT now(),
  updated_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX refresh_tokens_family_idx ON refresh_tokens (family_id);

CREATE TABLE email_provider_usage (
  provider text NOT NULL,
  day date NOT NULL,
  sent int NOT NULL DEFAULT 0,
  PRIMARY KEY (provider, day)
);

CREATE TABLE config (
  key text PRIMARY KEY,
  value jsonb NOT NULL,
  created_at timestamptz NOT NULL DEFAULT now(),
  updated_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE audit_log (
  id uuid PRIMARY KEY,
  actor_id uuid REFERENCES users(id),
  action text NOT NULL,
  entity text NOT NULL,
  entity_id uuid,
  diff jsonb,
  reason text,
  at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX audit_log_entity_idx ON audit_log (entity, entity_id);
