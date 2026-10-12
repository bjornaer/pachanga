-- name: UpsertEmailCode :exec
INSERT INTO email_codes (user_id, purpose, code_hash, expires_at)
VALUES (@user_id, @purpose, @code_hash, @expires_at)
ON CONFLICT (user_id, purpose) DO UPDATE SET code_hash = EXCLUDED.code_hash, expires_at = EXCLUDED.expires_at, attempts = 0, updated_at = now();

-- name: BumpEmailCodeAttempts :one
UPDATE email_codes SET attempts = attempts + 1, updated_at = now()
WHERE user_id = @user_id AND purpose = @purpose
RETURNING code_hash, expires_at, attempts;

-- name: DeleteEmailCode :exec
DELETE FROM email_codes WHERE user_id = @user_id AND purpose = @purpose;

-- name: InsertRefreshToken :exec
INSERT INTO refresh_tokens (id, user_id, family_id, token_hash, audience, expires_at)
VALUES (@id, @user_id, @family_id, @token_hash, @audience, @expires_at);

-- name: GetRefreshToken :one
SELECT id, user_id, family_id, audience, expires_at, used_at, revoked_at FROM refresh_tokens WHERE token_hash = @token_hash FOR UPDATE;

-- name: MarkRefreshTokenUsed :exec
UPDATE refresh_tokens SET used_at = now(), updated_at = now() WHERE id = @id;

-- name: RevokeFamily :exec
UPDATE refresh_tokens SET revoked_at = now(), updated_at = now() WHERE family_id = @family_id AND revoked_at IS NULL;

-- name: RevokeUserTokens :exec
UPDATE refresh_tokens SET revoked_at = now(), updated_at = now() WHERE user_id = @user_id AND revoked_at IS NULL;

-- name: InsertAudit :exec
INSERT INTO audit_log (id, actor_id, action, entity, entity_id, diff, reason)
VALUES (@id, @actor_id, @action, @entity, @entity_id, @diff, @reason);
