package worker

import (
	"context"
	"crypto/rand"
	"crypto/tls"
	"fmt"
	"net"
	"net/mail"
	"net/smtp"
	"strings"
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
	if _, err := data.Write(message(from, to, key, claims)); err != nil {
		return fmt.Errorf("smtp: %w", err)
	}
	if err := data.Close(); err != nil {
		return fmt.Errorf("smtp: %w", err)
	}
	return client.Quit()
}

func message(from, to *mail.Address, key string, claims license.Claims) []byte {
	_, domain, _ := strings.Cut(from.Address, "@")
	var b strings.Builder
	fmt.Fprintf(&b, "From: %s\r\n", from)
	fmt.Fprintf(&b, "To: %s\r\n", to)
	fmt.Fprintf(&b, "Subject: Your %s license key\r\n", claims.App)
	fmt.Fprintf(&b, "Date: %s\r\n", time.Now().Format(time.RFC1123Z))
	fmt.Fprintf(&b, "Message-ID: <%s@%s>\r\n", rand.Text(), domain)
	b.WriteString("MIME-Version: 1.0\r\n")
	b.WriteString("Content-Type: text/plain; charset=utf-8\r\n\r\n")

	fmt.Fprintf(&b, "This is your license key for %s:\r\n\r\n%s\r\n\r\n", claims.App, key)
	if claims.ExpiresAt != 0 {
		until := time.Unix(claims.ExpiresAt, 0).UTC().Format("2 January 2006")
		fmt.Fprintf(&b, "It is valid until %s.\r\n", until)
	}
	return []byte(b.String())
}
