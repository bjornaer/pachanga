// Command seed creates Madrid demo data for manual QA. Idempotent. Never run against prod.
package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"os"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/pachanga/pachanga/services/api/internal/auth"
	"github.com/pachanga/pachanga/services/api/internal/db"
	"github.com/pachanga/pachanga/services/api/internal/platform"
)

const (
	password   = "pachanga123"
	adminEmail = "admin@pachanga.local"
	adminTOTP  = "PACHANGADEVTOTPSECRETXX2" // dev only; base32
)

// Madrid neighbourhoods (lat, lng).
var areas = []struct {
	name     string
	lat, lng float64
}{
	{"Lavapiés", 40.4087, -3.7010}, {"Chamberí", 40.4340, -3.7038}, {"Retiro", 40.4113, -3.6764},
	{"Tetuán", 40.4600, -3.6980}, {"Carabanchel", 40.3833, -3.7275}, {"Vallecas", 40.3917, -3.6608},
	{"Moncloa", 40.4351, -3.7190}, {"Salamanca", 40.4300, -3.6794},
}

var names = []string{"Lucía", "Hugo", "Martina", "Pablo", "Sofía", "Álvaro", "Carmen", "Javier", "Paula", "Diego",
	"Elena", "Sergio", "Irene", "Adrián", "Nerea", "Marcos", "Alba", "Raúl", "Noa", "Iván"}

func main() {
	if os.Getenv("APP_ENV") == "prod" {
		log.Fatal("refusing to seed prod")
	}
	ctx := context.Background()
	pool, err := platform.OpenDB(ctx, os.Getenv("DATABASE_URL"))
	if err != nil {
		log.Fatal(err)
	}
	q := db.New(pool)
	hash, _ := auth.HashPassword(password)
	genders := []db.Gender{db.GenderFemale, db.GenderMale, db.GenderNonBinary, db.GenderPreferNotToSay}
	now := time.Now()

	create := func(email, name string, i int) {
		a := areas[i%len(areas)]
		lat, lng := auth.RoundLocation(a.lat, a.lng)
		_, err := q.CreateUser(ctx, db.CreateUserParams{
			ID: uuid.Must(uuid.NewV7()), Email: email, EmailVerifiedAt: &now, PasswordHash: &hash, DisplayName: name,
			BirthDate: pgtype.Date{Time: now.AddDate(-(19 + i*2%30), -i%12, 0), Valid: true},
			Gender:    genders[i%len(genders)], Lat: lat, Lng: lng, Locale: "es-ES",
			Status: db.UserStatusActive, TermsVersionAccepted: "2026-10",
		})
		var pgErr *pgconn.PgError
		if err != nil && (!errors.As(err, &pgErr) || pgErr.Code != "23505") {
			log.Fatal(err)
		}
	}
	create(adminEmail, "Admin", 0)
	for i, n := range names {
		create(fmt.Sprintf("player%02d@pachanga.local", i+1), n, i)
	}
	u, err := q.GetUserByEmail(ctx, adminEmail)
	if err != nil {
		log.Fatal(err)
	}
	secret := adminTOTP
	if err := q.SetTOTPSecret(ctx, db.SetTOTPSecretParams{ID: u.ID, TotpSecret: &secret}); err != nil {
		log.Fatal(err)
	}
	fmt.Printf("Seeded %d players (player01..%02d@pachanga.local) and admin.\nPassword for all: %s\nAdmin TOTP: otpauth://totp/Pachanga:%s?secret=%s&issuer=Pachanga\n",
		len(names), len(names), password, adminEmail, adminTOTP)
}
