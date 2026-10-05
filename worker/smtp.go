package worker

import (
	"context"
	"crypto/tls"
	"fmt"
	"net"
	"net/mail"
	"net/smtp"
	"time"

	"github.com/mizuchilabs/inkan/license"
)

// SMTP mails keys through a mail server. The server has to offer STARTTLS,
// which is the usual setup on port 587.
type SMTP struct {
	Addr string // host:port
	User string
	Pass string
	From string // an address, or "Name <address>"

	// Brand shapes the built in mail.
	Brand Brand
	// Templates replace the built in mail, see [LoadTemplates]. May be nil.
	Templates *Templates
}

// Send mails the key to the email in its claims. It fits [Config.Send].
func (s SMTP) Send(ctx context.Context, key string, claims license.Claims) error {
	// Parsing also keeps line breaks out of the headers.
	from, err := mail.ParseAddress(s.From)
	if err != nil {
		return fmt.Errorf("smtp: sender: %w", err)
	}
	to, err := mail.ParseAddress(claims.Email)
	if err != nil {
		return fmt.Errorf("smtp: recipient: %w", err)
	}
	host, _, err := net.SplitHostPort(s.Addr)
	if err != nil {
		return fmt.Errorf("smtp: %w", err)
	}
	msg, err := s.Templates.message(from, to, key, claims, s.Brand)
	if err != nil {
		return fmt.Errorf("smtp: %w", err)
	}

	var dialer net.Dialer
	conn, err := dialer.DialContext(ctx, "tcp", s.Addr)
	if err != nil {
		return fmt.Errorf("smtp: %w", err)
	}
	// smtp.SendMail has no timeout, a stuck server would hold the webhook forever.
	_ = conn.SetDeadline(time.Now().Add(30 * time.Second))
	client, err := smtp.NewClient(conn, host)
	if err != nil {
		_ = conn.Close()
		return fmt.Errorf("smtp: %w", err)
	}
	defer func() { _ = client.Close() }()

	if ok, _ := client.Extension("STARTTLS"); ok {
		if err := client.StartTLS(&tls.Config{ServerName: host, MinVersion: tls.VersionTLS12}); err != nil {
			return fmt.Errorf("smtp: %w", err)
		}
	}
	if s.User != "" {
		if err := client.Auth(smtp.PlainAuth("", s.User, s.Pass, host)); err != nil {
			return fmt.Errorf("smtp: %w", err)
		}
	}
	if err := client.Mail(from.Address); err != nil {
		return fmt.Errorf("smtp: %w", err)
	}
	if err := client.Rcpt(to.Address); err != nil {
		return fmt.Errorf("smtp: %w", err)
	}
	data, err := client.Data()
	if err != nil {
		return fmt.Errorf("smtp: %w", err)
	}
	if _, err := data.Write(msg); err != nil {
		return fmt.Errorf("smtp: %w", err)
	}
	if err := data.Close(); err != nil {
		return fmt.Errorf("smtp: %w", err)
	}
	return client.Quit()
}
