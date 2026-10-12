package auth

import (
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestIsAdult(t *testing.T) {
	now := time.Date(2026, 10, 12, 0, 0, 0, 0, time.UTC)
	cases := []struct {
		birth string
		want  bool
	}{
		{"2008-10-12", true},  // 18th birthday today
		{"2008-10-13", false}, // tomorrow
		{"1990-01-01", true},
		{"2008-02-29", true}, // leap: 2026-03-01 < now
		{"2010-01-01", false},
	}
	for _, c := range cases {
		b, _ := time.Parse(time.DateOnly, c.birth)
		if got := IsAdult(b, now); got != c.want {
			t.Errorf("IsAdult(%s) = %v, want %v", c.birth, got, c.want)
		}
	}
}

func TestPassword(t *testing.T) {
	h, err := HashPassword("correct horse battery")
	if err != nil {
		t.Fatal(err)
	}
	if !VerifyPassword("correct horse battery", h) || VerifyPassword("wrong", h) || VerifyPassword("x", "garbage") {
		t.Fatal("verify mismatch")
	}
}

func TestAccessToken(t *testing.T) {
	s := Signer{Secret: []byte("01234567890123456789012345678901")}
	id := uuid.Must(uuid.NewV7())
	now := time.Now()
	tok, _ := s.Access(id, AudApp, now)
	if got, err := s.Parse(tok, AudApp); err != nil || got != id {
		t.Fatalf("parse: %v %v", got, err)
	}
	if _, err := s.Parse(tok, AudAdmin); err == nil {
		t.Fatal("app token accepted as admin")
	}
	old, _ := s.Access(id, AudApp, now.Add(-AccessTTL-time.Second))
	if _, err := s.Parse(old, AudApp); err == nil {
		t.Fatal("expired token accepted")
	}
	if _, err := (Signer{Secret: []byte("other-secret-other-secret-other!")}).Parse(tok, AudApp); err == nil {
		t.Fatal("wrong secret accepted")
	}
}

func TestRoundLocation(t *testing.T) {
	lat, lng := RoundLocation(40.41678, -3.70379)
	if lat != 40.415 || lng != -3.705 {
		t.Fatalf("got %v %v", lat, lng)
	}
}
