-- name: CreateUser :one
INSERT INTO users (id, email, email_verified_at, password_hash, apple_sub, google_sub, display_name, birth_date, gender,
  home_location, locale, status, adult_attested_at, terms_version_accepted)
VALUES (@id, lower(@email), @email_verified_at, @password_hash, @apple_sub, @google_sub, @display_name, @birth_date, @gender,
  ST_SetSRID(ST_MakePoint(@lng::float8, @lat::float8), 4326)::geography, @locale, @status, now(), @terms_version_accepted)
RETURNING id;

-- name: GetUserByEmail :one
SELECT id, password_hash, status, role, totp_secret FROM users WHERE lower(email) = lower(@email) AND deleted_at IS NULL;

-- name: GetUserByProviderSub :one
SELECT id, status FROM users
WHERE deleted_at IS NULL AND ((@provider::text = 'apple' AND apple_sub = @sub) OR (@provider::text = 'google' AND google_sub = @sub));

-- name: LinkProviderSub :exec
UPDATE users SET
  apple_sub = CASE WHEN @provider::text = 'apple' THEN @sub ELSE apple_sub END,
  google_sub = CASE WHEN @provider::text = 'google' THEN @sub ELSE google_sub END,
  email_verified_at = coalesce(email_verified_at, now()),
  updated_at = now()
WHERE id = @id;

-- name: GetMe :one
SELECT id, email, email_verified_at, display_name, birth_date, gender, locale, status, role,
  ST_Y(home_location::geometry)::float8 AS lat, ST_X(home_location::geometry)::float8 AS lng
FROM users WHERE id = @id AND deleted_at IS NULL;

-- name: GetUserStatus :one
SELECT status, role FROM users WHERE id = @id AND deleted_at IS NULL;

-- name: MarkEmailVerified :exec
UPDATE users SET email_verified_at = now(), updated_at = now() WHERE id = @id;

-- name: SetPasswordHash :exec
UPDATE users SET password_hash = @password_hash, updated_at = now() WHERE id = @id;

-- name: SetTOTPSecret :exec
UPDATE users SET totp_secret = @totp_secret, role = 'super_admin', updated_at = now() WHERE id = @id;
