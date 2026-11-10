package mail

import (
	"context"
	"fmt"
	"net/url"
	"strings"

	gomail "github.com/wneessen/go-mail"

	"github.com/obcode/tallox.go/internal/domain"
)

// SMTPConfig is the mail block of tallox.yaml, as the sender needs it.
type SMTPConfig struct {
	Host     string
	Port     int
	Username string
	Password string
	// From is header and envelope sender alike. They must be equal: a gateway that checks
	// alignment refuses the mail otherwise (554 5.7.1), and the error says nothing about why.
	From string
	// TestMail, when set, receives every mail instead of its recipient — the dry run before an
	// installation writes to the faculty.
	TestMail string
	// BaseURL names the installation in List-Id.
	BaseURL string
}

// SMTP sends through one server, STARTTLS mandatory.
type SMTP struct {
	cfg    SMTPConfig
	listID string
}

// NewSMTP wires one up.
func NewSMTP(cfg SMTPConfig) *SMTP {
	host := "tallox"
	if u, err := url.Parse(cfg.BaseURL); err == nil && u.Host != "" {
		host = u.Host
	}
	if cfg.Port == 0 {
		cfg.Port = 587
	}
	return &SMTP{cfg: cfg, listID: "Tallox <" + host + ">"}
}

var _ domain.MailSender = (*SMTP)(nil)

// Send sends one mail.
func (s *SMTP) Send(ctx context.Context, m domain.OutgoingMail) error {
	msg, err := s.Message(m)
	if err != nil {
		return err
	}

	opts := []gomail.Option{
		gomail.WithPort(s.cfg.Port),
		gomail.WithTLSPortPolicy(gomail.TLSMandatory),
	}
	if s.cfg.Username != "" {
		opts = append(opts, gomail.WithSMTPAuth(gomail.SMTPAuthAutoDiscover),
			gomail.WithUsername(s.cfg.Username), gomail.WithPassword(s.cfg.Password))
	}
	client, err := gomail.NewClient(s.cfg.Host, opts...)
	if err != nil {
		return fmt.Errorf("cannot configure the SMTP client: %w", err)
	}
	if err := client.DialAndSendWithContext(ctx, msg); err != nil {
		return fmt.Errorf("cannot send: %w", err)
	}
	return nil
}

// Message builds the MIME message, without sending it. Exported for -mail-preview and tests.
func (s *SMTP) Message(m domain.OutgoingMail) (*gomail.Msg, error) {
	msg := gomail.NewMsg()
	if err := msg.From(s.cfg.From); err != nil {
		return nil, fmt.Errorf("cannot set the sender: %w", err)
	}
	to, subject := m.To, m.Subject
	if s.cfg.TestMail != "" {
		to = s.cfg.TestMail
		subject = "[Test] " + subject
		msg.SetGenHeader("X-Tallox-Original-Recipient", m.To)
	}
	if err := msg.AddToFormat(m.ToName, to); err != nil {
		return nil, fmt.Errorf("cannot set the recipient: %w", err)
	}
	if m.ReplyTo != "" {
		if err := msg.ReplyTo(m.ReplyTo); err != nil {
			return nil, fmt.Errorf("cannot set the reply address: %w", err)
		}
	}
	msg.Subject(strings.ReplaceAll(subject, "\n", " "))
	msg.SetGenHeader("List-Id", s.listID)
	switch m.Category {
	case domain.MailNotification:
		msg.SetGenHeader("Auto-Submitted", "auto-generated")
	case domain.MailAnnouncement:
		msg.SetGenHeader("Precedence", "bulk")
	}
	msg.SetBodyString(gomail.TypeTextPlain, m.Text)
	msg.AddAlternativeString(gomail.TypeTextHTML, m.HTML)
	return msg, nil
}
