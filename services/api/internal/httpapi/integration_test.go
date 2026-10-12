package httpapi_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/pquerna/otp/totp"
	"github.com/riverqueue/river"
	"github.com/riverqueue/river/riverdriver/riverpgxv5"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"

	"github.com/pachanga/pachanga/services/api/internal/auth"
	"github.com/pachanga/pachanga/services/api/internal/db"
	"github.com/pachanga/pachanga/services/api/internal/httpapi"
	"github.com/pachanga/pachanga/services/api/internal/platform"
)

func setup(t *testing.T) (*httptest.Server, *db.Queries) {
	srv, q, p := setupPool(t)
	pools[q] = p
	return srv, q
}

var pools = map[*db.Queries]*pgxpool.Pool{}

func setupPool(t *testing.T) (*httptest.Server, *db.Queries, *pgxpool.Pool) {
	t.Helper()
	if testing.Short() {
		t.Skip("integration")
	}
	ctx := context.Background()
	pg, err := postgres.Run(ctx, "postgis/postgis:16-3.4",
		postgres.WithDatabase("pachanga"), postgres.WithUsername("pachanga"), postgres.WithPassword("pachanga"),
		testcontainers.WithWaitStrategy(wait.ForLog("database system is ready to accept connections").WithOccurrence(2).WithStartupTimeout(90*time.Second)))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = pg.Terminate(ctx) })
	url, _ := pg.ConnectionString(ctx, "sslmode=disable")
	_, file, _, _ := runtime.Caller(0)
	if err := platform.Migrate(ctx, url, filepath.Join(filepath.Dir(file), "../../../../db/migrations")); err != nil {
		t.Fatal(err)
	}
	pool, err := platform.OpenDB(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	rc, _ := river.NewClient[pgx.Tx](riverpgxv5.New(pool), &river.Config{})
	q := db.New(pool)
	h, err := httpapi.NewRouter(&httpapi.Server{Q: q, Pool: pool, Auth: &auth.Service{
		Pool: pool, Q: q, River: rc, Signer: auth.Signer{Secret: []byte("test-secret-test-secret-test-secret!")},
		Terms: "2026-10", Devices: auth.NoDeviceCheck{}, Now: time.Now,
	}})
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	return srv, q, pool
}

type client struct {
	t   *testing.T
	url string
}

func (c client) do(method, path, token string, body any, want int) map[string]any {
	c.t.Helper()
	b, _ := json.Marshal(body)
	req, _ := http.NewRequest(method, c.url+path, bytes.NewReader(b))
	req.Header.Set("Content-Type", "application/json")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		c.t.Fatal(err)
	}
	defer func() { _ = res.Body.Close() }()
	var out map[string]any
	_ = json.NewDecoder(res.Body).Decode(&out)
	if res.StatusCode != want {
		c.t.Fatalf("%s %s: status %d, want %d, body %v", method, path, res.StatusCode, want, out)
	}
	return out
}

// lastCode reads the code from the most recent queued email job.
func lastCode(t *testing.T, q *db.Queries, purpose string) string {
	t.Helper()
	var args []byte
	if err := pools[q].QueryRow(context.Background(),
		`SELECT args FROM river_job WHERE kind = 'send_code_email' AND args->>'purpose' = $1 ORDER BY id DESC LIMIT 1`, purpose).Scan(&args); err != nil {
		t.Fatal(err)
	}
	var a struct{ Code string }
	_ = json.Unmarshal(args, &a)
	return a.Code
}

func signupBody(email, birth string) map[string]any {
	return map[string]any{
		"email": email, "password": "supersecret1", "display_name": "Ana", "birth_date": birth, "gender": "female",
		"home_location": map[string]float64{"lat": 40.4168, "lng": -3.7038}, "adult_attested": true, "terms_version": "2026-10",
	}
}

func TestAuthFlow(t *testing.T) {
	srv, q := setup(t)
	c := client{t, srv.URL}

	c.do("GET", "/healthz", "", nil, 200)

	under := time.Now().AddDate(-17, 0, 0).Format(time.DateOnly)
	if p := c.do("POST", "/v1/auth/signup", "", signupBody("kid@example.com", under), 422); p["code"] != "underage" {
		t.Fatalf("got %v", p)
	}
	var n int
	_ = pools[q].QueryRow(context.Background(), `SELECT count(*) FROM users`).Scan(&n)
	if n != 0 {
		t.Fatal("underage signup stored a row")
	}
	noAttest := signupBody("x@example.com", "1990-01-01")
	noAttest["adult_attested"] = false
	c.do("POST", "/v1/auth/signup", "", noAttest, 422)

	tok := c.do("POST", "/v1/auth/signup", "", signupBody("ana@example.com", "1990-05-01"), 201)
	access := tok["access_token"].(string)
	c.do("POST", "/v1/auth/signup", "", signupBody("ANA@example.com", "1990-05-01"), 409)

	me := c.do("GET", "/v1/me", access, nil, 200)
	if me["email_verified"] != false {
		t.Fatal("should be unverified")
	}
	loc := me["home_location"].(map[string]any)
	if loc["lat"].(float64) != 40.415 {
		t.Fatalf("location not rounded: %v", loc)
	}
	c.do("GET", "/v1/me", "", nil, 401)

	c.do("POST", "/v1/auth/verify-email", access, map[string]string{"code": "000000"}, 400)
	c.do("POST", "/v1/auth/verify-email", access, map[string]string{"code": lastCode(t, q, "verify_email")}, 204)
	if me := c.do("GET", "/v1/me", access, nil, 200); me["email_verified"] != true {
		t.Fatal("should be verified")
	}

	c.do("POST", "/v1/auth/login", "", map[string]string{"email": "ana@example.com", "password": "wrongwrong"}, 401)
	login := c.do("POST", "/v1/auth/login", "", map[string]string{"email": "ana@example.com", "password": "supersecret1"}, 200)

	r1 := login["refresh_token"].(string)
	r2 := c.do("POST", "/v1/auth/refresh", "", map[string]string{"refresh_token": r1}, 200)["refresh_token"].(string)
	// Reusing r1 revokes the family, so r2 dies too.
	if p := c.do("POST", "/v1/auth/refresh", "", map[string]string{"refresh_token": r1}, 401); p["code"] != "refresh_token_reused" {
		t.Fatalf("got %v", p)
	}
	c.do("POST", "/v1/auth/refresh", "", map[string]string{"refresh_token": r2}, 401)

	l2 := c.do("POST", "/v1/auth/login", "", map[string]string{"email": "ana@example.com", "password": "supersecret1"}, 200)
	c.do("POST", "/v1/auth/logout", "", map[string]string{"refresh_token": l2["refresh_token"].(string)}, 204)
	c.do("POST", "/v1/auth/refresh", "", map[string]string{"refresh_token": l2["refresh_token"].(string)}, 401)

	// Password reset
	c.do("POST", "/v1/auth/password-reset", "", map[string]string{"email": "nobody@example.com"}, 204)
	c.do("POST", "/v1/auth/password-reset", "", map[string]string{"email": "ana@example.com"}, 204)
	c.do("POST", "/v1/auth/password-reset/confirm", "", map[string]string{"email": "ana@example.com", "code": lastCode(t, q, "password_reset"), "new_password": "newsecret12"}, 204)
	c.do("POST", "/v1/auth/login", "", map[string]string{"email": "ana@example.com", "password": "newsecret12"}, 200)

	// App tokens can't reach admin API.
	c.do("GET", "/admin/v1/me", access, nil, 401)
	c.do("POST", "/v1/auth/oauth/google", "", map[string]string{"id_token": "x"}, 503)
}

func TestVerifyAttemptsLimit(t *testing.T) {
	srv, q := setup(t)
	c := client{t, srv.URL}
	access := c.do("POST", "/v1/auth/signup", "", signupBody("b@example.com", "1990-05-01"), 201)["access_token"].(string)
	code := lastCode(t, q, "verify_email")
	for range 5 {
		c.do("POST", "/v1/auth/verify-email", access, map[string]string{"code": "000000"}, 400)
	}
	c.do("POST", "/v1/auth/verify-email", access, map[string]string{"code": code}, 429)
}

func TestAdminLogin(t *testing.T) {
	srv, q := setup(t)
	c := client{t, srv.URL}
	c.do("POST", "/v1/auth/signup", "", signupBody("admin@example.com", "1985-01-01"), 201)
	key, _ := totp.Generate(totp.GenerateOpts{Issuer: "Pachanga", AccountName: "admin@example.com"})
	u, _ := q.GetUserByEmail(context.Background(), "admin@example.com")
	secret := key.Secret()
	if err := q.SetTOTPSecret(context.Background(), db.SetTOTPSecretParams{ID: u.ID, TotpSecret: &secret}); err != nil {
		t.Fatal(err)
	}
	code, _ := totp.GenerateCode(secret, time.Now())
	c.do("POST", "/admin/v1/auth/login", "", map[string]string{"email": "admin@example.com", "password": "supersecret1", "totp": "000000"}, 401)
	tok := c.do("POST", "/admin/v1/auth/login", "", map[string]string{"email": "admin@example.com", "password": "supersecret1", "totp": code}, 200)
	me := c.do("GET", "/admin/v1/me", tok["access_token"].(string), nil, 200)
	if me["role"] != "super_admin" {
		t.Fatal(me)
	}
	c.do("GET", "/v1/me", tok["access_token"].(string), nil, 401) // admin token isn't an app token
}
