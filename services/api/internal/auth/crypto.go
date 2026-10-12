package auth

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"math/big"
	"strings"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
	"golang.org/x/crypto/argon2"
)

// OWASP argon2id minimum: m=19 MiB, t=2, p=1.
const (
	argonTime    = 2
	argonMemory  = 19 * 1024
	argonThreads = 1
	argonKeyLen  = 32
)

func HashPassword(pw string) (string, error) {
	salt := make([]byte, 16)
	if _, err := rand.Read(salt); err != nil {
		return "", err
	}
	key := argon2.IDKey([]byte(pw), salt, argonTime, argonMemory, argonThreads, argonKeyLen)
	return fmt.Sprintf("$argon2id$v=19$m=%d,t=%d,p=%d$%s$%s", argonMemory, argonTime, argonThreads,
		base64.RawStdEncoding.EncodeToString(salt), base64.RawStdEncoding.EncodeToString(key)), nil
}

func VerifyPassword(pw, encoded string) bool {
	parts := strings.Split(encoded, "$")
	if len(parts) != 6 || parts[1] != "argon2id" {
		return false
	}
	var m, t uint32
	var p uint8
	if _, err := fmt.Sscanf(parts[3], "m=%d,t=%d,p=%d", &m, &t, &p); err != nil {
		return false
	}
	salt, err1 := base64.RawStdEncoding.DecodeString(parts[4])
	want, err2 := base64.RawStdEncoding.DecodeString(parts[5])
	if err1 != nil || err2 != nil {
		return false
	}
	got := argon2.IDKey([]byte(pw), salt, t, m, p, uint32(len(want)))
	return subtle.ConstantTimeCompare(got, want) == 1
}

// dummyHash keeps login timing uniform for unknown emails.
var dummyHash, _ = HashPassword("dummy-password-for-timing")

const (
	AudApp     = "app"
	AudAdmin   = "admin"
	AccessTTL  = 15 * time.Minute
	RefreshTTL = 30 * 24 * time.Hour
)

type Signer struct{ Secret []byte }

func (s Signer) Access(userID uuid.UUID, aud string, now time.Time) (string, error) {
	return jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.RegisteredClaims{
		Subject:   userID.String(),
		Audience:  jwt.ClaimStrings{aud},
		IssuedAt:  jwt.NewNumericDate(now),
		ExpiresAt: jwt.NewNumericDate(now.Add(AccessTTL)),
	}).SignedString(s.Secret)
}

func (s Signer) Parse(token, aud string) (uuid.UUID, error) {
	var c jwt.RegisteredClaims
	_, err := jwt.ParseWithClaims(token, &c, func(*jwt.Token) (any, error) { return s.Secret, nil },
		jwt.WithValidMethods([]string{"HS256"}), jwt.WithAudience(aud), jwt.WithExpirationRequired())
	if err != nil {
		return uuid.Nil, err
	}
	return uuid.Parse(c.Subject)
}

// HMAC keyed with the server secret so leaked DB rows can't be brute-forced offline.
func (s Signer) hash(parts ...string) string {
	h := hmac.New(sha256.New, s.Secret)
	h.Write([]byte(strings.Join(parts, "|")))
	return hex.EncodeToString(h.Sum(nil))
}

func newRefreshToken() string {
	b := make([]byte, 32)
	_, _ = rand.Read(b)
	return base64.RawURLEncoding.EncodeToString(b)
}

func newCode() string {
	n, _ := rand.Int(rand.Reader, big.NewInt(1_000_000))
	return fmt.Sprintf("%06d", n.Int64())
}

// IsAdult reports whether someone born on birth is at least 18 on day now.
func IsAdult(birth, now time.Time) bool {
	return !birth.AddDate(18, 0, 0).After(now)
}

// RoundLocation snaps a point to a ~500 m grid (spec §8 data minimisation).
// ponytail: fixed 0.005° grid (~550 m lat, ~420 m lng at Madrid); use a geohash if precision per latitude matters.
func RoundLocation(lat, lng float64) (float64, float64) {
	r := func(v float64) float64 { return float64(int64(v/0.005+copysignHalf(v))) * 0.005 }
	return r(lat), r(lng)
}

func copysignHalf(v float64) float64 {
	if v < 0 {
		return -0.5
	}
	return 0.5
}
