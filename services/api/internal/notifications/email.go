// Package notifications sends transactional email (and push from M1).
package notifications

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/riverqueue/river"

	"github.com/pachanga/pachanga/services/api/internal/db"
)

type Email struct{ To, Subject, Text string }

// EmailSender is one transactional provider reached over HTTPS (spec §2: DO blocks SMTP).
type EmailSender interface {
	Name() string
	Send(ctx context.Context, e Email) error
}

type Console struct{}

func (Console) Name() string { return "console" }
func (Console) Send(_ context.Context, e Email) error {
	slog.Info("email", "to", e.To, "subject", e.Subject, "text", e.Text)
	return nil
}

type Brevo struct{ Key, From string }

func (Brevo) Name() string { return "brevo" }
func (b Brevo) Send(ctx context.Context, e Email) error {
	return postJSON(ctx, "https://api.brevo.com/v3/smtp/email", map[string]string{"api-key": b.Key}, "", map[string]any{
		"sender": map[string]string{"email": b.From}, "to": []map[string]string{{"email": e.To}},
		"subject": e.Subject, "textContent": e.Text,
	})
}

type Mailjet struct{ Key, From string } // Key = "public:private"

func (Mailjet) Name() string { return "mailjet" }
func (m Mailjet) Send(ctx context.Context, e Email) error {
	return postJSON(ctx, "https://api.mailjet.com/v3.1/send", nil, m.Key, map[string]any{
		"Messages": []map[string]any{{"From": map[string]string{"Email": m.From}, "To": []map[string]string{{"Email": e.To}},
			"Subject": e.Subject, "TextPart": e.Text}},
	})
}

func postJSON(ctx context.Context, url string, headers map[string]string, basic string, body any) error {
	b, _ := json.Marshal(body)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(b))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	if user, pass, ok := strings.Cut(basic, ":"); ok {
		req.SetBasicAuth(user, pass)
	}
	res, err := (&http.Client{Timeout: 10 * time.Second}).Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = res.Body.Close() }()
	if res.StatusCode >= 300 {
		return fmt.Errorf("%s: status %d", url, res.StatusCode)
	}
	return nil
}

type Capped struct {
	Sender   EmailSender
	DailyCap int // 0 = unlimited
}

var ErrAllCapped = errors.New("all email providers at daily cap")

// Failover tries providers in order, skipping those at their daily cap.
// ponytail: check-then-send cap is racy across workers (may overshoot by worker count); lock the usage row if caps get tight.
type Failover struct {
	Providers []Capped
	Q         *db.Queries
}

func (f Failover) Send(ctx context.Context, e Email) error {
	var errs []error
	for _, p := range f.Providers {
		if p.DailyCap > 0 {
			sent, err := f.Q.EmailUsageToday(ctx, p.Sender.Name())
			if err != nil {
				return err
			}
			if int(sent) >= p.DailyCap {
				continue
			}
		}
		if err := p.Sender.Send(ctx, e); err != nil {
			errs = append(errs, err)
			continue
		}
		return f.Q.BumpEmailUsage(ctx, p.Sender.Name())
	}
	if len(errs) == 0 {
		return ErrAllCapped
	}
	return errors.Join(errs...)
}

// SendCodeArgs is the River job for verification / reset codes.
type SendCodeArgs struct {
	To      string `json:"to"`
	Locale  string `json:"locale"`
	Purpose string `json:"purpose"` // verify_email | password_reset
	Code    string `json:"code"`
}

func (SendCodeArgs) Kind() string { return "send_code_email" }

type SendCodeWorker struct {
	river.WorkerDefaults[SendCodeArgs]
	Mail Failover
}

func (w *SendCodeWorker) Work(ctx context.Context, job *river.Job[SendCodeArgs]) error {
	a := job.Args
	err := w.Mail.Send(ctx, renderCode(a))
	if errors.Is(err, ErrAllCapped) {
		return river.JobSnooze(time.Hour) // queued; app already says "code on its way"
	}
	return err
}

func renderCode(a SendCodeArgs) Email {
	t := codeTemplates["es-ES"][a.Purpose]
	if strings.HasPrefix(a.Locale, "en") {
		t = codeTemplates["en"][a.Purpose]
	}
	return Email{To: a.To, Subject: t[0], Text: fmt.Sprintf(t[1], a.Code)}
}

var codeTemplates = map[string]map[string][2]string{
	"es-ES": {
		"verify_email":   {"Tu código de verificación de Pachanga", "Tu código es %s. Caduca en 15 minutos."},
		"password_reset": {"Restablece tu contraseña de Pachanga", "Tu código para restablecer la contraseña es %s. Caduca en 15 minutos. Si no lo pediste, ignora este mensaje."},
	},
	"en": {
		"verify_email":   {"Your Pachanga verification code", "Your code is %s. It expires in 15 minutes."},
		"password_reset": {"Reset your Pachanga password", "Your password reset code is %s. It expires in 15 minutes. If you didn't ask for it, ignore this email."},
	},
}
