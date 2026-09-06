package api

// Administration of the outgoing mail server.
//
// These live apart from SaveSettings because that handler takes only the
// integers in settingsSchema, while this is text plus a password that must not
// be stored or returned in the clear.

import (
	"database/sql"
	"net/http"
	"strconv"
	"strings"

	"meshdepot/internal/coerce"
	"meshdepot/internal/dbutil"
	"meshdepot/internal/httpx"
	"meshdepot/internal/mail"
	"meshdepot/internal/maildigest"
)

// mailSettingKeys are the app_settings rows the mail configuration lives in.
const (
	mailEnabledKey    = "mail_enabled"
	mailHostKey       = "mail_host"
	mailPortKey       = "mail_port"
	mailUsernameKey   = "mail_username"
	mailPasswordKey   = "mail_password" // stored encrypted
	mailFromKey       = "mail_from"
	mailFromNameKey   = "mail_from_name"
	mailEncryptionKey = "mail_encryption"
)

// passwordPlaceholder is what the form gets instead of the stored password, and
// what it sends back when the administrator did not retype it. The real value
// never leaves the server.
const passwordPlaceholder = "***"

func (server *Server) mailSetting(key string) string {
	var value string
	server.DB.QueryRow("SELECT value FROM app_settings WHERE key = ?", key).Scan(&value)
	return value
}

func (server *Server) writeMailSetting(key, value string) {
	dbutil.ExecLogged(server.DB,
		"INSERT INTO app_settings (key, value) VALUES (?, ?) ON CONFLICT(key) DO UPDATE SET value = excluded.value",
		key, value)
}

// mailConfig assembles the stored settings. The loader lives in the mail
// package so the notification path uses exactly the same one.
func (server *Server) mailConfig() (mail.Config, bool) {
	return mail.Load(server.DB, server.Crypto)
}

// mailPassword decrypts the stored password for the masked-field resolution.
func (server *Server) mailPassword(stored string) string {
	if stored == "" {
		return ""
	}
	owner, _ := strconv.Atoi(server.mailSetting(mailPasswordKey + "_owner"))
	if owner == 0 {
		return ""
	}
	if decrypted, ok := server.Crypto.Decrypt(stored, owner); ok {
		return decrypted
	}
	return ""
}

// GetMailSettings returns the mail configuration. Admin only; the password is
// replaced by a placeholder.
func (server *Server) GetMailSettings(responseWriter http.ResponseWriter, request *http.Request) {
	password := ""
	if server.mailSetting(mailPasswordKey) != "" {
		password = passwordPlaceholder
	}
	port, _ := strconv.Atoi(server.mailSetting(mailPortKey))
	if port == 0 {
		port = 587
	}
	encryption := server.mailSetting(mailEncryptionKey)
	if encryption == "" {
		encryption = "starttls"
	}
	// How the queue is doing. Sent rows are kept, so the figure is a running
	// total since the feature was switched on rather than a snapshot.
	var queued, sent int
	var lastSent sql.NullString
	server.DB.QueryRow("SELECT COUNT(*) FROM notification_mail_queue WHERE sent_at IS NULL").Scan(&queued)
	server.DB.QueryRow("SELECT COUNT(*) FROM notification_mail_queue WHERE sent_at IS NOT NULL").Scan(&sent)
	server.DB.QueryRow("SELECT MAX(sent_at) FROM notification_mail_queue WHERE sent_at IS NOT NULL").Scan(&lastSent)

	httpx.Success(responseWriter, map[string]any{
		"enabled":    server.mailSetting(mailEnabledKey) == "1",
		"host":       server.mailSetting(mailHostKey),
		"port":       port,
		"username":   server.mailSetting(mailUsernameKey),
		"password":   password,
		"from":       server.mailSetting(mailFromKey),
		"from_name":  server.mailSetting(mailFromNameKey),
		"encryption": encryption,
		// Counted per notification, not per message: a digest bundles several
		// into one e-mail, so these say how much was reported, not how many
		// messages left the building.
		"queued_count":   queued,
		"sent_count":     sent,
		"last_sent_at":   lastSent.String,
		"digest_minutes": int(maildigest.Interval.Minutes()),
	})
}

// mailBody is the shape both handlers accept.
type mailBody struct {
	Enabled    bool   `json:"enabled"`
	Host       string `json:"host"`
	Port       any    `json:"port"`
	Username   string `json:"username"`
	Password   string `json:"password"`
	From       string `json:"from"`
	FromName   string `json:"from_name"`
	Encryption string `json:"encryption"`
}

// readMailBody turns an already-decoded body into a config, resolving a masked
// password from what is stored. Returns the config and the password to persist
// ("" = keep the stored one).
//
// It takes the decoded struct rather than the request: a request body can only
// be read once, and decoding it twice in one handler leaves the second read
// with nothing.
func (server *Server) readMailBody(body mailBody) (mail.Config, string) {
	encryption := strings.ToLower(strings.TrimSpace(body.Encryption))
	switch encryption {
	case "none", "starttls", "tls":
	default:
		encryption = "starttls"
	}
	config := mail.Config{
		Host:       strings.TrimSpace(body.Host),
		Port:       coerce.Int(body.Port),
		Username:   strings.TrimSpace(body.Username),
		Password:   body.Password,
		From:       strings.TrimSpace(body.From),
		FromName:   strings.TrimSpace(body.FromName),
		Encryption: encryption,
	}
	// An untouched form sends the placeholder back; that must not overwrite the
	// stored password with three asterisks.
	if config.Password == passwordPlaceholder || config.Password == "" {
		config.Password = server.mailPassword(server.mailSetting(mailPasswordKey))
		return config, ""
	}
	return config, config.Password
}

// SaveMailSettings stores the configuration. Admin only.
func (server *Server) SaveMailSettings(responseWriter http.ResponseWriter, request *http.Request) {
	currentUserID := userID(request)
	var body mailBody
	if httpx.DecodeJSON(request, &body) != nil {
		httpx.Error(responseWriter, http.StatusUnprocessableEntity, "error.invalid_request")
		return
	}
	config, newPassword := server.readMailBody(body)
	// Switching the service on with an unusable configuration would leave every
	// send failing quietly in the background, so it is refused here where the
	// administrator can see it.
	if body.Enabled && !config.Usable() {
		httpx.Error(responseWriter, http.StatusUnprocessableEntity, "error.mail_incomplete")
		return
	}

	server.writeMailSetting(mailEnabledKey, boolSetting(body.Enabled))
	server.writeMailSetting(mailHostKey, config.Host)
	server.writeMailSetting(mailPortKey, strconv.Itoa(config.Port))
	server.writeMailSetting(mailUsernameKey, config.Username)
	server.writeMailSetting(mailFromKey, config.From)
	server.writeMailSetting(mailFromNameKey, config.FromName)
	server.writeMailSetting(mailEncryptionKey, config.Encryption)
	if newPassword != "" {
		encrypted, failure := server.Crypto.Encrypt(newPassword, currentUserID)
		if failure != nil {
			httpx.Error(responseWriter, http.StatusInternalServerError, "error.server")
			return
		}
		server.writeMailSetting(mailPasswordKey, encrypted)
		// Recorded so the value can be decrypted later: the key is derived per
		// user, so reading it back needs to know whose it was.
		server.writeMailSetting(mailPasswordKey+"_owner", strconv.Itoa(currentUserID))
	}
	server.GetMailSettings(responseWriter, request)
}

// TestMailSettings sends a test message to the administrator's own address,
// using the settings in the request rather than the stored ones - so a
// configuration can be tried before it is saved. Admin only.
func (server *Server) TestMailSettings(responseWriter http.ResponseWriter, request *http.Request) {
	currentUserID := userID(request)
	var body mailBody
	if httpx.DecodeJSON(request, &body) != nil {
		httpx.Error(responseWriter, http.StatusUnprocessableEntity, "error.invalid_request")
		return
	}
	config, _ := server.readMailBody(body)
	if !config.Usable() {
		httpx.Success(responseWriter, map[string]any{"ok": false, "error": "error.mail_incomplete"})
		return
	}

	var recipient string
	server.DB.QueryRow("SELECT COALESCE(email, '') FROM users WHERE id = ?", currentUserID).Scan(&recipient)
	if strings.TrimSpace(recipient) == "" {
		// Testing against a mailbox nobody reads proves nothing, so the check
		// stops here rather than reporting a success no one can confirm.
		httpx.Success(responseWriter, map[string]any{"ok": false, "error": "error.mail_no_own_address"})
		return
	}

	if failure := mail.Send(config, recipient,
		"MeshDepot test message",
		"This is a test message from MeshDepot.\n\nIf it arrived, notification e-mails will work with these settings."); failure != nil {
		// The server's own words are passed through: "login rejected" or
		// "STARTTLS refused" tells an administrator what to change, which a
		// generic failure does not.
		httpx.Success(responseWriter, map[string]any{"ok": false, "error": "error.mail_send_failed", "detail": failure.Error()})
		return
	}
	httpx.Success(responseWriter, map[string]any{"ok": true, "sent_to": recipient})
}

func boolSetting(value bool) string {
	if value {
		return "1"
	}
	return "0"
}
