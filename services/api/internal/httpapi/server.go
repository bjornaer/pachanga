// Package httpapi maps the generated OpenAPI interface onto domain services. Handlers stay thin.
package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/getkin/kin-openapi/openapi3filter"
	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/go-chi/httprate"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	oapimw "github.com/oapi-codegen/nethttp-middleware"
	openapi_types "github.com/oapi-codegen/runtime/types"

	"github.com/pachanga/pachanga/services/api/internal/api"
	"github.com/pachanga/pachanga/services/api/internal/auth"
	"github.com/pachanga/pachanga/services/api/internal/db"
	"github.com/pachanga/pachanga/services/api/internal/platform"
)

type Server struct {
	Auth *auth.Service
	Q    *db.Queries
	Pool *pgxpool.Pool
	// TrustedProxies in front of the API (0 = clients connect directly). Decides which X-Forwarded-For hop is the client.
	TrustedProxies int
}

var _ api.StrictServerInterface = (*Server)(nil)

type ctxKey struct{}

func userID(ctx context.Context) uuid.UUID { return ctx.Value(ctxKey{}).(uuid.UUID) }

func NewRouter(s *Server) (http.Handler, error) {
	spec, err := api.GetSwagger()
	if err != nil {
		return nil, err
	}
	spec.Servers = nil
	r := chi.NewRouter()
	clientIP := middleware.ClientIPFromRemoteAddr
	if s.TrustedProxies > 0 {
		clientIP = middleware.ClientIPFromXFFTrustedProxies(s.TrustedProxies)
	}
	r.Use(clientIP, middleware.Recoverer, logRequests)
	r.Use(oapimw.OapiRequestValidatorWithOptions(spec, &oapimw.Options{
		Options:               openapi3filter.Options{AuthenticationFunc: openapi3filter.NoopAuthenticationFunc},
		SilenceServersWarning: true,
		ErrorHandlerWithOpts: func(_ context.Context, err error, w http.ResponseWriter, _ *http.Request, o oapimw.ErrorHandlerOpts) {
			writeProblem(w, &platform.Problem{Status: o.StatusCode, Code: "invalid_request", Detail: err.Error()})
		},
	}))
	// Rate limits on auth endpoints (spec §8). ponytail: in-memory per instance; move to Postgres/Redis when running >1 replica.
	r.Group(func(r chi.Router) {
		r.Use(func(next http.Handler) http.Handler {
			limited := httprate.LimitBy(20, time.Minute, func(r *http.Request) (string, error) {
				return middleware.GetClientIP(r.Context()), nil
			})(next)
			return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
				if strings.Contains(req.URL.Path, "/auth/") {
					limited.ServeHTTP(w, req)
					return
				}
				next.ServeHTTP(w, req)
			})
		})
		strict := api.NewStrictHandlerWithOptions(s, nil, api.StrictHTTPServerOptions{
			RequestErrorHandlerFunc: func(w http.ResponseWriter, _ *http.Request, err error) {
				writeProblem(w, &platform.Problem{Status: http.StatusBadRequest, Code: "invalid_request", Detail: err.Error()})
			},
			ResponseErrorHandlerFunc: func(w http.ResponseWriter, r *http.Request, err error) { writeErr(w, r, err) },
		})
		api.HandlerWithOptions(strict, api.ChiServerOptions{BaseRouter: r, Middlewares: []api.MiddlewareFunc{s.authenticate}})
	})
	return r, nil
}

// authenticate runs per operation; only operations with bearer security get BearerScopes in context.
func (s *Server) authenticate(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Context().Value(api.BearerScopes) == nil {
			next.ServeHTTP(w, r)
			return
		}
		aud := auth.AudApp
		if strings.HasPrefix(r.URL.Path, "/admin/") {
			aud = auth.AudAdmin
		}
		tok, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
		id, err := s.Auth.Signer.Parse(tok, aud)
		if !ok || err != nil {
			writeProblem(w, platform.Err(http.StatusUnauthorized, "unauthorized"))
			return
		}
		st, err := s.Q.GetUserStatus(r.Context(), id)
		switch {
		case errors.Is(err, pgx.ErrNoRows):
			writeProblem(w, platform.Err(http.StatusUnauthorized, "unauthorized"))
			return
		case err != nil:
			writeErr(w, r, err)
			return
		case st.Status == db.UserStatusBanned:
			writeProblem(w, platform.Err(http.StatusForbidden, "banned"))
			return
		case aud == auth.AudAdmin && st.Role != db.UserRoleSuperAdmin: // role re-checked on every admin request
			writeProblem(w, platform.Err(http.StatusForbidden, "forbidden"))
			return
		}
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), ctxKey{}, id)))
	})
}

func writeErr(w http.ResponseWriter, r *http.Request, err error) {
	var p *platform.Problem
	if errors.As(err, &p) {
		writeProblem(w, p)
		return
	}
	slog.ErrorContext(r.Context(), "internal error", "err", err, "path", r.URL.Path)
	writeProblem(w, platform.Err(http.StatusInternalServerError, "internal"))
}

func writeProblem(w http.ResponseWriter, p *platform.Problem) {
	w.Header().Set("Content-Type", "application/problem+json")
	w.WriteHeader(p.Status)
	body := api.Problem{Type: "https://pachanga.app/problems/" + p.Code, Title: http.StatusText(p.Status), Status: p.Status, Code: p.Code}
	if p.Detail != "" {
		body.Detail = &p.Detail
	}
	_ = json.NewEncoder(w).Encode(body)
}

func logRequests(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ww := middleware.NewWrapResponseWriter(w, r.ProtoMajor)
		start := time.Now()
		next.ServeHTTP(ww, r)
		slog.Info("http", "method", r.Method, "path", r.URL.Path, "status", ww.Status(), "ms", time.Since(start).Milliseconds())
	})
}

func tokens(t auth.Tokens) api.TokensJSONResponse {
	return api.TokensJSONResponse{AccessToken: t.Access, RefreshToken: t.Refresh, ExpiresIn: t.ExpiresIn}
}

func profile(p api.Profile) auth.Profile {
	out := auth.Profile{
		DisplayName: p.DisplayName, BirthDate: p.BirthDate.Time, Gender: db.Gender(p.Gender),
		Lat: p.HomeLocation.Lat, Lng: p.HomeLocation.Lng, Adult: p.AdultAttested, TermsVersion: p.TermsVersion,
	}
	if p.Locale != nil {
		out.Locale = string(*p.Locale)
	}
	return out
}

func device(d *api.DeviceAttestation) *auth.Device {
	if d == nil {
		return nil
	}
	return &auth.Device{Platform: string(d.Platform), Token: d.Token}
}

func (s *Server) Health(ctx context.Context, _ api.HealthRequestObject) (api.HealthResponseObject, error) {
	if err := s.Pool.Ping(ctx); err != nil {
		return nil, err
	}
	return api.Health200JSONResponse{Status: "ok"}, nil
}

func (s *Server) Signup(ctx context.Context, r api.SignupRequestObject) (api.SignupResponseObject, error) {
	b := r.Body
	p := api.Profile{DisplayName: b.DisplayName, BirthDate: b.BirthDate, Gender: b.Gender, HomeLocation: b.HomeLocation,
		AdultAttested: b.AdultAttested, TermsVersion: b.TermsVersion}
	if b.Locale != nil {
		l := api.ProfileLocale(*b.Locale)
		p.Locale = &l
	}
	t, err := s.Auth.Signup(ctx, string(b.Email), b.Password, profile(p), device(b.Device))
	if err != nil {
		return nil, err
	}
	return api.Signup201JSONResponse{TokensJSONResponse: tokens(t)}, nil
}

func (s *Server) Login(ctx context.Context, r api.LoginRequestObject) (api.LoginResponseObject, error) {
	t, err := s.Auth.Login(ctx, string(r.Body.Email), r.Body.Password)
	if err != nil {
		return nil, err
	}
	return api.Login200JSONResponse{TokensJSONResponse: tokens(t)}, nil
}

func (s *Server) OauthLogin(ctx context.Context, r api.OauthLoginRequestObject) (api.OauthLoginResponseObject, error) {
	var p *auth.Profile
	if r.Body.Profile != nil {
		pp := profile(*r.Body.Profile)
		p = &pp
	}
	t, err := s.Auth.OAuth(ctx, string(r.Provider), r.Body.IdToken, p, device(r.Body.Device))
	if err != nil {
		return nil, err
	}
	return api.OauthLogin200JSONResponse{TokensJSONResponse: tokens(t)}, nil
}

func (s *Server) Refresh(ctx context.Context, r api.RefreshRequestObject) (api.RefreshResponseObject, error) {
	t, err := s.Auth.Refresh(ctx, r.Body.RefreshToken)
	if err != nil {
		return nil, err
	}
	return api.Refresh200JSONResponse{TokensJSONResponse: tokens(t)}, nil
}

func (s *Server) Logout(ctx context.Context, r api.LogoutRequestObject) (api.LogoutResponseObject, error) {
	return api.Logout204Response{}, s.Auth.Logout(ctx, r.Body.RefreshToken)
}

func (s *Server) VerifyEmail(ctx context.Context, r api.VerifyEmailRequestObject) (api.VerifyEmailResponseObject, error) {
	return api.VerifyEmail204Response{}, s.Auth.VerifyEmail(ctx, userID(ctx), r.Body.Code)
}

func (s *Server) ResendVerification(ctx context.Context, _ api.ResendVerificationRequestObject) (api.ResendVerificationResponseObject, error) {
	return api.ResendVerification204Response{}, s.Auth.ResendVerification(ctx, userID(ctx))
}

func (s *Server) RequestPasswordReset(ctx context.Context, r api.RequestPasswordResetRequestObject) (api.RequestPasswordResetResponseObject, error) {
	return api.RequestPasswordReset204Response{}, s.Auth.RequestPasswordReset(ctx, string(r.Body.Email))
}

func (s *Server) ConfirmPasswordReset(ctx context.Context, r api.ConfirmPasswordResetRequestObject) (api.ConfirmPasswordResetResponseObject, error) {
	return api.ConfirmPasswordReset204Response{}, s.Auth.ConfirmPasswordReset(ctx, string(r.Body.Email), r.Body.Code, r.Body.NewPassword)
}

func (s *Server) GetMe(ctx context.Context, _ api.GetMeRequestObject) (api.GetMeResponseObject, error) {
	me, err := s.me(ctx)
	return api.GetMe200JSONResponse(me), err
}

func (s *Server) AdminLogin(ctx context.Context, r api.AdminLoginRequestObject) (api.AdminLoginResponseObject, error) {
	t, err := s.Auth.AdminLogin(ctx, string(r.Body.Email), r.Body.Password, r.Body.Totp)
	if err != nil {
		return nil, err
	}
	return api.AdminLogin200JSONResponse{TokensJSONResponse: tokens(t)}, nil
}

func (s *Server) AdminGetMe(ctx context.Context, _ api.AdminGetMeRequestObject) (api.AdminGetMeResponseObject, error) {
	me, err := s.me(ctx)
	return api.AdminGetMe200JSONResponse(me), err
}

func (s *Server) me(ctx context.Context) (api.Me, error) {
	m, err := s.Q.GetMe(ctx, userID(ctx))
	if err != nil {
		return api.Me{}, err
	}
	return api.Me{
		Id: m.ID, Email: m.Email, EmailVerified: m.EmailVerifiedAt != nil, DisplayName: m.DisplayName,
		BirthDate: &openapi_types.Date{Time: m.BirthDate.Time}, Gender: api.Gender(m.Gender),
		HomeLocation: api.GeoPoint{Lat: m.Lat, Lng: m.Lng}, Locale: m.Locale,
		Status: api.MeStatus(m.Status), Role: api.MeRole(m.Role),
	}, nil
}
