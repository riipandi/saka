package appconfig

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"uuid"

	systemv1 "github.com/riipandi/tango/codegen/proto/go/tango/system/v1"
	"github.com/riipandi/tango/internal/audit"
	"github.com/riipandi/tango/internal/config"
	"github.com/riipandi/tango/internal/datastore"
	"github.com/riipandi/tango/internal/mailer"
	"github.com/riipandi/tango/modules/identity/user"
)

// The failures the procedure reports. The handler maps them to connect
// codes, so the wire form of a refusal lives with the transport, not here.
var (
	// ErrMailUnavailable is a request the running process cannot serve: no
	// SMTP host is configured, so there is no configuration to test.
	ErrMailUnavailable = errors.New("appconfig: mailer is not configured")

	// ErrUnknownAccount is a caller the database no longer names. The guard
	// admitted the claims, so reaching here means the account was deleted
	// under a still-valid token.
	ErrUnknownAccount = errors.New("appconfig: unknown account")
)

// testEmailSubject is the subject the smoke message carries. The template's
// header says the same thing, and the smoke command sends the same line.
const testEmailSubject = "SMTP Test Successful"

// Service answers the deployment's configuration and sends its test email.
type Service struct {
	// cfg is the configuration the process was started with. It is resolved
	// once at startup, so every answer describes the running process, not the
	// file on disk.
	cfg  config.Config
	pool *datastore.Postgres
	// users is the identity area's account read. The caller's address on
	// record is the message's default recipient, and the identity area is
	// where accounts live — the audit-log reader leans on the same package.
	users *user.Repository
	// audit writes the record of a message that left, on the pool: the send
	// is not a database change, so there is no transaction to ride.
	audit *audit.Recorder
	mail  *mailer.Service
	log   *slog.Logger
}

// NewService builds the service. The mailer is the infrastructure the
// composition root resolves; its configuration decides whether the
// procedure can serve at all.
func NewService(cfg config.Config, pool *datastore.Postgres, recorder *audit.Recorder, mail *mailer.Service, log *slog.Logger) *Service {
	if log == nil {
		log = slog.New(slog.DiscardHandler)
	}
	return &Service{
		cfg:   cfg,
		pool:  pool,
		users: user.NewRepository(),
		audit: recorder,
		mail:  mail,
		log:   log,
	}
}

// GetPublic answers the configuration an unauthenticated client may read:
// the public subset only. It never touches the database, so the SPA can
// bootstrap before any sign-in exists.
func (s *Service) GetPublic() *systemv1.AppConfigPublic {
	return publicConfig(s.cfg)
}

// GetAll answers the deployment's configuration to an administrator: every
// non-secret setting the process runs on.
func (s *Service) GetAll() *systemv1.AppConfig {
	return fullConfig(s.cfg)
}

// SendTestEmail renders the test template and submits one message. The send
// is synchronous, so the returned error is the SMTP attempt's outcome and
// the caller learns whether the configuration works while the settings
// screen is still open.
//
// The recipient is the caller's address on record unless the request names
// one, so an administrator can prove the path to a mailbox they hold. The
// record is written after the send, naming the address it went to.
func (s *Service) SendTestEmail(ctx context.Context, callerID uuid.UUID, to string) error {
	if !s.mail.Configured() {
		return ErrMailUnavailable
	}

	account, err := s.users.GetUser(ctx, s.pool, callerID)
	if err != nil {
		if errors.Is(err, datastore.ErrNoRows) {
			return ErrUnknownAccount
		}
		return fmt.Errorf("appconfig: read account: %w", err)
	}

	recipient := to
	if recipient == "" {
		recipient = account.Email
	}

	if err := s.mail.Send(ctx, mailer.Request{
		To:       []string{recipient},
		Subject:  testEmailSubject,
		Template: mailer.TemplateTestEmail,
		View: mailer.View{
			Email: recipient,
			Data:  mailer.TestEmailData{Email: recipient},
		},
	}); err != nil {
		return fmt.Errorf("appconfig: send test email: %w", err)
	}

	payload := map[string]string{"to": recipient}
	if to != "" && to != account.Email {
		payload["redirected"] = "true"
	}
	s.audit.Record(ctx, s.pool, audit.Entry{
		Event:   audit.EventTestEmailSent,
		Status:  audit.StatusSuccess,
		UserID:  callerID.String(),
		Payload: payload,
	})
	return nil
}
