package bootstrap

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/rs/zerolog/log"

	"github.com/obcode/tallox.go/internal/domain"
	"github.com/obcode/tallox.go/internal/mail"
)

// mailSender is the SMTP client the configuration describes, or nil — and nil is how the
// dispatcher knows to queue without sending.
func mailSender(cfg MailConfig) domain.MailSender {
	if !cfg.Configured() {
		return nil
	}
	return mail.NewSMTP(mail.SMTPConfig{
		Host: cfg.Host, Port: cfg.Port, Username: cfg.Username, Password: cfg.Password,
		From: cfg.From, TestMail: cfg.TestMail, BaseURL: cfg.BaseURL,
	})
}

// logMailConfiguration says at startup whether mail leaves this installation. Never the password.
func logMailConfiguration(cfg MailConfig) {
	switch {
	case !cfg.Configured():
		log.Info().Msg("mail not configured — notifications are queued, not sent")
	case cfg.TestMail != "":
		log.Warn().Str("host", cfg.Host).Msg("mail dry run — every mail goes to mail.testmail")
	default:
		log.Info().Str("host", cfg.Host).Str("from", cfg.From).Msg("mail configured")
	}
}

// runMailPreview renders one of every mail with invented data into dir, as .html and .txt, and
// exits. For looking at a mail in a browser and in a mail client before anybody receives one —
// the counterpart of MAIL_OUT for the cron scripts. Needs no database.
func runMailPreview(dir, baseURL string) error {
	if baseURL == "" {
		baseURL = "https://tallox.example.org"
	}
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return fmt.Errorf("cannot create %s: %w", dir, err)
	}
	r := mail.NewRenderer(baseURL)
	for name, m := range mail.PreviewMails(r) {
		for ext, body := range map[string]string{".html": m.HTML, ".txt": "Betreff: " + m.Subject + "\n\n" + m.Text} {
			path := filepath.Join(dir, name+ext)
			if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
				return fmt.Errorf("cannot write %s: %w", path, err)
			}
			fmt.Println(path)
		}
	}
	return nil
}
