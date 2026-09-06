// Package mail sends the notification e-mails.
//
// Deliberately built on net/smtp from the standard library rather than a mail
// library: what is needed here is one plaintext message to one recipient, and
// the dependency would buy nothing but attachments and MIME trees this never
// produces.
package mail

import (
	"crypto/tls"
	"database/sql"
	"errors"
	"fmt"
	"mime"
	"net"
	"net/smtp"
	"strconv"
	"strings"
	"time"
)

// dialTimeout bounds every step of a send. A mail server that accepts the
// connection and then goes quiet must not hold a worker for minutes.
const dialTimeout = 20 * time.Second

// Config is what an administrator fills in.
type Config struct {
	Host     string
	Port     int
	Username string
	Password string
	// From is the envelope and header sender. Many providers refuse a From that
	// is not the authenticated mailbox, so it defaults to Username when empty.
	From string
	// FromName is the display name shown in a mail client, optional.
	FromName string
	// Encryption is "starttls", "tls" or "none".
	Encryption string
}

// Usable reports whether the settings are complete enough to attempt a send.
func (config Config) Usable() bool {
	return strings.TrimSpace(config.Host) != "" && config.Port > 0 && config.Sender() != ""
}

// Sender is the address mail is sent from.
func (config Config) Sender() string {
	if address := strings.TrimSpace(config.From); address != "" {
		return address
	}
	return strings.TrimSpace(config.Username)
}

// Send delivers one plaintext message. The error is meant to be shown to an
// administrator, so it says which step failed rather than only that something
// did.
func Send(config Config, recipient, subject, body string) error {
	if !config.Usable() {
		return errors.New("mail: host, port and sender must be set")
	}
	recipient = strings.TrimSpace(recipient)
	if recipient == "" {
		return errors.New("mail: no recipient")
	}

	address := net.JoinHostPort(config.Host, fmt.Sprint(config.Port))
	var client *smtp.Client
	var failure error

	if config.Encryption == "tls" {
		// Implicit TLS: the connection is encrypted from the first byte, the way
		// port 465 expects it.
		connection, dialFailure := tls.DialWithDialer(&net.Dialer{Timeout: dialTimeout}, "tcp", address,
			&tls.Config{ServerName: config.Host, MinVersion: tls.VersionTLS12})
		if dialFailure != nil {
			return fmt.Errorf("mail: cannot connect over TLS: %w", dialFailure)
		}
		client, failure = smtp.NewClient(connection, config.Host)
		if failure != nil {
			connection.Close()
			return fmt.Errorf("mail: TLS handshake accepted but SMTP did not start: %w", failure)
		}
	} else {
		connection, dialFailure := net.DialTimeout("tcp", address, dialTimeout)
		if dialFailure != nil {
			return fmt.Errorf("mail: cannot connect: %w", dialFailure)
		}
		client, failure = smtp.NewClient(connection, config.Host)
		if failure != nil {
			connection.Close()
			return fmt.Errorf("mail: connected but SMTP did not start: %w", failure)
		}
		if config.Encryption == "starttls" {
			if failure = client.StartTLS(&tls.Config{ServerName: config.Host, MinVersion: tls.VersionTLS12}); failure != nil {
				client.Close()
				return fmt.Errorf("mail: STARTTLS refused: %w", failure)
			}
		}
	}
	defer client.Close()

	// Authentication is skipped without a username: relays on a private network
	// commonly accept mail from known hosts and offer no AUTH at all.
	if strings.TrimSpace(config.Username) != "" {
		if failure = client.Auth(smtp.PlainAuth("", config.Username, config.Password, config.Host)); failure != nil {
			return fmt.Errorf("mail: login rejected: %w", failure)
		}
	}
	if failure = client.Mail(config.Sender()); failure != nil {
		return fmt.Errorf("mail: sender %q refused: %w", config.Sender(), failure)
	}
	if failure = client.Rcpt(recipient); failure != nil {
		return fmt.Errorf("mail: recipient %q refused: %w", recipient, failure)
	}
	writer, failure := client.Data()
	if failure != nil {
		return fmt.Errorf("mail: server refused the message body: %w", failure)
	}
	if _, failure = writer.Write([]byte(message(config, recipient, subject, body))); failure != nil {
		writer.Close()
		return fmt.Errorf("mail: sending the message failed: %w", failure)
	}
	if failure = writer.Close(); failure != nil {
		return fmt.Errorf("mail: the server rejected the message: %w", failure)
	}
	return client.Quit()
}

// message builds the RFC 5322 text.
//
// The subject is encoded as UTF-8 base64 whenever it is not plain ASCII:
// design names carry umlauts and emoji, and an unencoded header arrives as
// mojibake or gets the mail rejected outright.
func message(config Config, recipient, subject, body string) string {
	from := config.Sender()
	if name := strings.TrimSpace(config.FromName); name != "" {
		from = fmt.Sprintf("%s <%s>", encodeHeader(name), from)
	}
	var builder strings.Builder
	builder.WriteString("From: " + from + "\r\n")
	builder.WriteString("To: " + recipient + "\r\n")
	builder.WriteString("Subject: " + encodeHeader(subject) + "\r\n")
	builder.WriteString("MIME-Version: 1.0\r\n")
	builder.WriteString("Content-Type: text/plain; charset=UTF-8\r\n")
	builder.WriteString("Content-Transfer-Encoding: 8bit\r\n")
	builder.WriteString("\r\n")
	// Bare newlines are illegal in SMTP data; normalise whatever the caller sent.
	builder.WriteString(strings.ReplaceAll(strings.ReplaceAll(body, "\r\n", "\n"), "\n", "\r\n"))
	builder.WriteString("\r\n")
	return builder.String()
}

// encodeHeader returns the value as-is when it is plain printable ASCII, and as
// an RFC 2047 encoded word otherwise.
func encodeHeader(value string) string {
	plain := true
	for _, character := range value {
		if character < 32 || character > 126 {
			plain = false
			break
		}
	}
	if plain {
		return value
	}
	return mime.BEncoding.Encode("UTF-8", value)
}

// Decryptor is what the stored password needs to be read: the crypto helper,
// narrowed to the one method, so this package does not depend on it.
type Decryptor interface {
	Decrypt(value string, userID int) (string, bool)
}

// Load assembles the configuration from app_settings.
//
// It lives here rather than in the api package because the notification path
// needs it too, and a second copy of the key names and the password handling is
// exactly the kind of duplication that drifts apart.
//
// ready is false when the service is switched off or the settings are
// incomplete - callers use it to decide whether to offer or attempt a send.
func Load(database *sql.DB, decryptor Decryptor) (config Config, ready bool) {
	setting := func(key string) string {
		var value string
		database.QueryRow("SELECT value FROM app_settings WHERE key = ?", key).Scan(&value)
		return value
	}
	port, _ := strconv.Atoi(setting("mail_port"))
	config = Config{
		Host:       setting("mail_host"),
		Port:       port,
		Username:   setting("mail_username"),
		From:       setting("mail_from"),
		FromName:   setting("mail_from_name"),
		Encryption: setting("mail_encryption"),
	}
	// The password is encrypted under the id of the admin who saved it, so
	// reading it back needs to know whose key derived it.
	if stored := setting("mail_password"); stored != "" && decryptor != nil {
		if owner, _ := strconv.Atoi(setting("mail_password_owner")); owner > 0 {
			if decrypted, ok := decryptor.Decrypt(stored, owner); ok {
				config.Password = decrypted
			}
		}
	}
	return config, setting("mail_enabled") == "1" && config.Usable()
}
