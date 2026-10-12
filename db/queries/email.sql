-- name: EmailUsageToday :one
SELECT coalesce((SELECT sent FROM email_provider_usage WHERE provider = @provider AND day = current_date), 0)::int AS sent;

-- name: BumpEmailUsage :exec
INSERT INTO email_provider_usage (provider, day, sent) VALUES (@provider, current_date, 1)
ON CONFLICT (provider, day) DO UPDATE SET sent = email_provider_usage.sent + 1;
