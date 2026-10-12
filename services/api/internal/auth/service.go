// Package auth holds signup, login, token rotation, email codes, OAuth and admin login.
package auth

import (
	"context"
	"crypto/subtle"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/coreos/go-oidc/v3/oidc"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/pquerna/otp/totp"
	"github.com/riverqueue/river"

	"github.com/pachanga/pachanga/services/api/internal/db"
	"github.com/pachanga/pachanga/services/api/internal/notifications"
	"github.com/pachanga/pachanga/services/api/internal/platform"
)

const (
	codeTTL         = 15 * time.Minute
	codeMaxAttempts = 5
	purposeVerify   = "verify_email"
	purposeReset    = "password_reset"
)

// DeviceChecker reads the "banned device" bit (Apple DeviceCheck / Play Integrity device recall, spec §6.4).
type DeviceChecker interface {
	Banned(ctx context.Context, platform, token string) (bool, error)
}

// NoDeviceCheck is used until Apple/Google credentials exist (status.json Q3).
type NoDeviceCheck struct{}

func (NoDeviceCheck) Banned(context.Context, string, string) (bool, error) { return false, nil }

type Device struct{ Platform, Token string }

type Profile struct {
	DisplayName  string
	BirthDate    time.Time
	Gender       db.Gender
	Lat, Lng     float64
	Adult        bool
	TermsVersion string
	Locale       string
}

type Tokens struct {
	Access, Refresh string
	ExpiresIn       int
}

type Service struct {
	Pool    *pgxpool.Pool
	Q       *db.Queries
	River   *river.Client[pgx.Tx]
	Signer  Signer
	Terms   string
	Devices DeviceChecker
	OIDC    map[string]*oidc.IDTokenVerifier // "apple", "google"; missing = not configured
	Now     func() time.Time
}

var (
	errInvalidCreds = platform.Err(http.StatusUnauthorized, "invalid_credentials")
	errInvalidCode  = platform.Err(http.StatusBadRequest, "invalid_code")
	errInvalidToken = platform.Err(http.StatusUnauthorized, "invalid_refresh_token")
)

func (s *Service) validateProfile(p Profile) error {
	if !p.Adult {
		return platform.Err(http.StatusUnprocessableEntity, "adult_attestation_required")
	}
	if p.TermsVersion != s.Terms {
		return platform.Err(http.StatusUnprocessableEntity, "terms_version_mismatch")
	}
	// Rejected birth dates are never stored or logged (spec §4.1).
	if !IsAdult(p.BirthDate, s.Now()) {
		return &platform.Problem{Status: http.StatusUnprocessableEntity, Code: "underage", Detail: "This app is for adults only"}
	}
	if strings.TrimSpace(p.DisplayName) == "" {
		return platform.Err(http.StatusUnprocessableEntity, "display_name_required")
	}
	return nil
}

func (s *Service) initialStatus(ctx context.Context, d *Device) (db.UserStatus, error) {
	if d == nil {
		return db.UserStatusActive, nil
	}
	banned, err := s.Devices.Banned(ctx, d.Platform, d.Token)
	if err != nil || !banned {
		return db.UserStatusActive, err
	}
	return db.UserStatusPendingReview, nil
}

func (s *Service) Signup(ctx context.Context, email, password string, p Profile, d *Device) (Tokens, error) {
	if err := s.validateProfile(p); err != nil {
		return Tokens{}, err
	}
	if len(password) < 10 {
		return Tokens{}, platform.Err(http.StatusUnprocessableEntity, "password_too_short")
	}
	status, err := s.initialStatus(ctx, d)
	if err != nil {
		return Tokens{}, err
	}
	hash, err := HashPassword(password)
	if err != nil {
		return Tokens{}, err
	}
	var t Tokens
	err = s.tx(ctx, func(q *db.Queries, tx pgx.Tx) error {
		id, err := s.createUser(ctx, q, email, &hash, nil, nil, p, status)
		if err != nil {
			return err
		}
		if err := s.sendCode(ctx, q, tx, id, email, p.Locale, purposeVerify); err != nil {
			return err
		}
		t, err = s.issue(ctx, q, id, AudApp, uuid.Must(uuid.NewV7()))
		return err
	})
	return t, err
}

func (s *Service) createUser(ctx context.Context, q *db.Queries, email string, hash, appleSub, googleSub *string, p Profile, status db.UserStatus) (uuid.UUID, error) {
	lat, lng := RoundLocation(p.Lat, p.Lng)
	var verified *time.Time
	if appleSub != nil || googleSub != nil {
		now := s.Now()
		verified = &now
	}
	locale := p.Locale
	if locale == "" {
		locale = "es-ES"
	}
	id, err := q.CreateUser(ctx, db.CreateUserParams{
		ID: uuid.Must(uuid.NewV7()), Email: email, EmailVerifiedAt: verified, PasswordHash: hash,
		AppleSub: appleSub, GoogleSub: googleSub, DisplayName: strings.TrimSpace(p.DisplayName),
		BirthDate: pgtype.Date{Time: p.BirthDate, Valid: true}, Gender: p.Gender, Lat: lat, Lng: lng,
		Locale: locale, Status: status, TermsVersionAccepted: p.TermsVersion,
	})
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == "23505" {
		return uuid.Nil, platform.Err(http.StatusConflict, "email_taken")
	}
	return id, err
}

func (s *Service) Login(ctx context.Context, email, password string) (Tokens, error) {
	u, err := s.Q.GetUserByEmail(ctx, email)
	if errors.Is(err, pgx.ErrNoRows) || (err == nil && u.PasswordHash == nil) {
		VerifyPassword(password, dummyHash)
		return Tokens{}, errInvalidCreds
	}
	if err != nil {
		return Tokens{}, err
	}
	if !VerifyPassword(password, *u.PasswordHash) {
		return Tokens{}, errInvalidCreds
	}
	if u.Status == db.UserStatusBanned {
		return Tokens{}, platform.Err(http.StatusForbidden, "banned")
	}
	return s.issue(ctx, s.Q, u.ID, AudApp, uuid.Must(uuid.NewV7()))
}

func (s *Service) AdminLogin(ctx context.Context, email, password, code string) (Tokens, error) {
	u, err := s.Q.GetUserByEmail(ctx, email)
	if errors.Is(err, pgx.ErrNoRows) {
		VerifyPassword(password, dummyHash)
		return Tokens{}, errInvalidCreds
	}
	if err != nil {
		return Tokens{}, err
	}
	ok := u.PasswordHash != nil && VerifyPassword(password, *u.PasswordHash) &&
		u.Role == db.UserRoleSuperAdmin && u.TotpSecret != nil && totp.Validate(code, *u.TotpSecret) &&
		u.Status == db.UserStatusActive
	if !ok {
		return Tokens{}, errInvalidCreds
	}
	var t Tokens
	err = s.tx(ctx, func(q *db.Queries, _ pgx.Tx) error {
		if err := q.InsertAudit(ctx, db.InsertAuditParams{ID: uuid.Must(uuid.NewV7()), ActorID: &u.ID, Action: "admin.login", Entity: "user", EntityID: &u.ID}); err != nil {
			return err
		}
		t, err = s.issue(ctx, q, u.ID, AudAdmin, uuid.Must(uuid.NewV7()))
		return err
	})
	return t, err
}

// Refresh rotates a refresh token. Presenting an already-used token revokes the whole family (reuse detection).
func (s *Service) Refresh(ctx context.Context, token string) (Tokens, error) {
	var t Tokens
	var reused bool
	err := s.tx(ctx, func(q *db.Queries, _ pgx.Tx) error {
		rt, err := q.GetRefreshToken(ctx, s.Signer.hash("refresh", token))
		if errors.Is(err, pgx.ErrNoRows) {
			return errInvalidToken
		}
		if err != nil {
			return err
		}
		if rt.RevokedAt != nil || !rt.ExpiresAt.After(s.Now()) {
			return errInvalidToken
		}
		if rt.UsedAt != nil {
			reused = true
			return q.RevokeFamily(ctx, rt.FamilyID) // commit the revocation, then report the error
		}
		st, err := q.GetUserStatus(ctx, rt.UserID)
		if err != nil || st.Status == db.UserStatusBanned {
			return errInvalidToken
		}
		if err := q.MarkRefreshTokenUsed(ctx, rt.ID); err != nil {
			return err
		}
		t, err = s.issue(ctx, q, rt.UserID, rt.Audience, rt.FamilyID)
		return err
	})
	if err == nil && reused {
		return Tokens{}, platform.Err(http.StatusUnauthorized, "refresh_token_reused")
	}
	return t, err
}

func (s *Service) Logout(ctx context.Context, token string) error {
	rt, err := s.Q.GetRefreshToken(ctx, s.Signer.hash("refresh", token))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	return s.Q.RevokeFamily(ctx, rt.FamilyID)
}

func (s *Service) VerifyEmail(ctx context.Context, userID uuid.UUID, code string) error {
	if err := s.consumeCode(ctx, userID, purposeVerify, code); err != nil {
		return err
	}
	return s.Q.MarkEmailVerified(ctx, userID)
}

func (s *Service) ResendVerification(ctx context.Context, userID uuid.UUID) error {
	me, err := s.Q.GetMe(ctx, userID)
	if err != nil {
		return err
	}
	if me.EmailVerifiedAt != nil {
		return nil
	}
	return s.tx(ctx, func(q *db.Queries, tx pgx.Tx) error {
		return s.sendCode(ctx, q, tx, userID, me.Email, me.Locale, purposeVerify)
	})
}

// RequestPasswordReset always succeeds so callers can't probe which emails exist.
func (s *Service) RequestPasswordReset(ctx context.Context, email string) error {
	u, err := s.Q.GetUserByEmail(ctx, email)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	me, err := s.Q.GetMe(ctx, u.ID)
	if err != nil {
		return err
	}
	return s.tx(ctx, func(q *db.Queries, tx pgx.Tx) error {
		return s.sendCode(ctx, q, tx, u.ID, me.Email, me.Locale, purposeReset)
	})
}

func (s *Service) ConfirmPasswordReset(ctx context.Context, email, code, newPassword string) error {
	u, err := s.Q.GetUserByEmail(ctx, email)
	if errors.Is(err, pgx.ErrNoRows) {
		return errInvalidCode
	}
	if err != nil {
		return err
	}
	if err := s.consumeCode(ctx, u.ID, purposeReset, code); err != nil {
		return err
	}
	hash, err := HashPassword(newPassword)
	if err != nil {
		return err
	}
	return s.tx(ctx, func(q *db.Queries, _ pgx.Tx) error {
		if err := q.SetPasswordHash(ctx, db.SetPasswordHashParams{ID: u.ID, PasswordHash: &hash}); err != nil {
			return err
		}
		return q.RevokeUserTokens(ctx, u.ID)
	})
}

// OAuth signs in with an Apple/Google ID token. Unknown accounts need profile (422 profile_required).
func (s *Service) OAuth(ctx context.Context, provider, idToken string, p *Profile, d *Device) (Tokens, error) {
	v := s.OIDC[provider]
	if v == nil {
		return Tokens{}, platform.Err(http.StatusServiceUnavailable, "provider_not_configured")
	}
	tok, err := v.Verify(ctx, idToken)
	if err != nil {
		return Tokens{}, platform.Err(http.StatusUnauthorized, "invalid_id_token")
	}
	var claims struct {
		Email         string `json:"email"`
		EmailVerified any    `json:"email_verified"` // Apple sends "true" as a string
	}
	if err := tok.Claims(&claims); err != nil {
		return Tokens{}, err
	}
	verified := claims.EmailVerified == true || claims.EmailVerified == "true"

	if u, err := s.Q.GetUserByProviderSub(ctx, db.GetUserByProviderSubParams{Provider: provider, Sub: &tok.Subject}); err == nil {
		if u.Status == db.UserStatusBanned {
			return Tokens{}, platform.Err(http.StatusForbidden, "banned")
		}
		return s.issue(ctx, s.Q, u.ID, AudApp, uuid.Must(uuid.NewV7()))
	} else if !errors.Is(err, pgx.ErrNoRows) {
		return Tokens{}, err
	}
	if claims.Email == "" || !verified {
		return Tokens{}, platform.Err(http.StatusUnprocessableEntity, "email_not_verified_by_provider")
	}
	// Existing email account: link the provider (provider vouches for the email).
	if u, err := s.Q.GetUserByEmail(ctx, claims.Email); err == nil {
		if u.Status == db.UserStatusBanned {
			return Tokens{}, platform.Err(http.StatusForbidden, "banned")
		}
		if err := s.Q.LinkProviderSub(ctx, db.LinkProviderSubParams{Provider: provider, Sub: &tok.Subject, ID: u.ID}); err != nil {
			return Tokens{}, err
		}
		return s.issue(ctx, s.Q, u.ID, AudApp, uuid.Must(uuid.NewV7()))
	} else if !errors.Is(err, pgx.ErrNoRows) {
		return Tokens{}, err
	}
	if p == nil {
		return Tokens{}, platform.Err(http.StatusUnprocessableEntity, "profile_required")
	}
	if err := s.validateProfile(*p); err != nil {
		return Tokens{}, err
	}
	status, err := s.initialStatus(ctx, d)
	if err != nil {
		return Tokens{}, err
	}
	var apple, google *string
	if provider == "apple" {
		apple = &tok.Subject
	} else {
		google = &tok.Subject
	}
	var t Tokens
	err = s.tx(ctx, func(q *db.Queries, _ pgx.Tx) error {
		id, err := s.createUser(ctx, q, claims.Email, nil, apple, google, *p, status)
		if err != nil {
			return err
		}
		t, err = s.issue(ctx, q, id, AudApp, uuid.Must(uuid.NewV7()))
		return err
	})
	return t, err
}

func (s *Service) issue(ctx context.Context, q *db.Queries, userID uuid.UUID, aud string, family uuid.UUID) (Tokens, error) {
	now := s.Now()
	access, err := s.Signer.Access(userID, aud, now)
	if err != nil {
		return Tokens{}, err
	}
	refresh := newRefreshToken()
	err = q.InsertRefreshToken(ctx, db.InsertRefreshTokenParams{
		ID: uuid.Must(uuid.NewV7()), UserID: userID, FamilyID: family,
		TokenHash: s.Signer.hash("refresh", refresh), Audience: aud, ExpiresAt: now.Add(RefreshTTL),
	})
	return Tokens{Access: access, Refresh: refresh, ExpiresIn: int(AccessTTL.Seconds())}, err
}

func (s *Service) sendCode(ctx context.Context, q *db.Queries, tx pgx.Tx, userID uuid.UUID, email, locale, purpose string) error {
	code := newCode()
	if err := q.UpsertEmailCode(ctx, db.UpsertEmailCodeParams{
		UserID: userID, Purpose: purpose, CodeHash: s.Signer.hash(purpose, userID.String(), code), ExpiresAt: s.Now().Add(codeTTL),
	}); err != nil {
		return err
	}
	_, err := s.River.InsertTx(ctx, tx, notifications.SendCodeArgs{To: email, Locale: locale, Purpose: purpose, Code: code}, nil)
	return err
}

// consumeCode counts the attempt first (committed even on failure), then checks expiry and value.
func (s *Service) consumeCode(ctx context.Context, userID uuid.UUID, purpose, code string) error {
	row, err := s.Q.BumpEmailCodeAttempts(ctx, db.BumpEmailCodeAttemptsParams{UserID: userID, Purpose: purpose})
	if errors.Is(err, pgx.ErrNoRows) {
		return errInvalidCode
	}
	if err != nil {
		return err
	}
	if row.Attempts > codeMaxAttempts {
		return platform.Err(http.StatusTooManyRequests, "too_many_attempts")
	}
	if !row.ExpiresAt.After(s.Now()) {
		return platform.Err(http.StatusBadRequest, "code_expired")
	}
	if subtle.ConstantTimeCompare([]byte(row.CodeHash), []byte(s.Signer.hash(purpose, userID.String(), code))) != 1 {
		return errInvalidCode
	}
	return s.Q.DeleteEmailCode(ctx, db.DeleteEmailCodeParams{UserID: userID, Purpose: purpose})
}

func (s *Service) tx(ctx context.Context, fn func(q *db.Queries, tx pgx.Tx) error) error {
	return pgx.BeginFunc(ctx, s.Pool, func(tx pgx.Tx) error { return fn(s.Q.WithTx(tx), tx) })
}
