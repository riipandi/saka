package mailer

import (
	"context"
	"errors"
	"fmt"
	"net"
	"strings"

	"github.com/emersion/go-smtp"
)

// The classes a caller matches with errors.Is. A failure carries exactly one:
// the class says what a caller should do about it, and two classes would leave
// that ambiguous.
const (
	// ErrNotConfigured means no SMTP host is set, so the mailer cannot send.
	// It is not a deployment failure: the mailer is optional.
	ErrNotConfigured = sentinel("mailer: not configured")
	// ErrNetwork is a failure before the server answered: DNS, a refused
	// connection, a reset, or a TLS handshake that did not verify.
	ErrNetwork = sentinel("mailer: network")
	// ErrTimeout is a dial or a command that ran out of time.
	ErrTimeout = sentinel("mailer: timeout")
	// ErrCanceled is the caller's context ending.
	ErrCanceled = sentinel("mailer: canceled")
	// ErrAuth is a rejected credential, or a server offering no mechanism the
	// mailer can present one with.
	ErrAuth = sentinel("mailer: authentication")
	// ErrRejected is a permanent (5xx) answer: the server refused the sender,
	// a recipient, or the message. Retrying it changes nothing.
	ErrRejected = sentinel("mailer: rejected")
	// ErrTemporary is a transient (4xx) answer. The message may be sent again.
	ErrTemporary = sentinel("mailer: temporary")
)

// Op names the step that failed, so a log line says where in the session it
// happened.
type Op string

const (
	// OpDial is opening the connection and completing the greeting.
	OpDial Op = "dial"
	// OpAuth is the AUTH exchange.
	OpAuth Op = "auth"
	// OpSend is the envelope and the message body.
	OpSend Op = "send"
)

// sentinel is an error class whose text is the class itself.
type sentinel string

func (s sentinel) Error() string { return string(s) }

// Error is one failed submission. Op and Target locate it; Target is host:port
// and never carries a credential. Code is the SMTP reply code when the server
// answered. Kind is the class.
type Error struct {
	Op     Op
	Target string
	Code   int
	Kind   error
	// cause is the underlying failure when it is one this package defines and a
	// caller may act on it — ErrInsecureAuth is the case: the class is
	// authentication, and the specific reason is what tells a deployment to
	// turn TLS on.
	cause error

	// detail is the server's own reply text. It is kept for the message but
	// never for a log line, where the class is what matters.
	detail string
}

// Error returns a description safe to log: the operation, the server, and the
// class. A credential is never part of it, and neither is the message.
func (e *Error) Error() string {
	if e == nil {
		return ""
	}
	name := "failed"
	if e.Kind != nil {
		name = strings.TrimPrefix(e.Kind.Error(), "mailer: ")
	}
	if e.Code > 0 {
		return fmt.Sprintf("mailer: %s %s: %s (smtp %d)", e.Op, e.Target, name, e.Code)
	}
	return fmt.Sprintf("mailer: %s %s: %s", e.Op, e.Target, name)
}

// Unwrap returns the class, plus the context sentinel a cancellation or a
// deadline belongs to, plus the specific cause when there is one.
func (e *Error) Unwrap() []error {
	if e == nil || e.Kind == nil {
		return nil
	}
	out := []error{e.Kind}
	if e.cause != nil {
		out = append(out, e.cause)
	}
	switch e.Kind {
	case ErrCanceled:
		out = append(out, context.Canceled)
	case ErrTimeout:
		out = append(out, context.DeadlineExceeded)
	}
	return out
}

// classify turns a library failure into an Error, or nil when the step
// succeeded.
func classify(op Op, target string, err error) error {
	if err == nil {
		return nil
	}
	failure := &Error{Op: op, Target: target}
	switch {
	case errors.Is(err, context.Canceled):
		failure.Kind = ErrCanceled
	case timeout(err):
		failure.Kind = ErrTimeout
	case errors.Is(err, errNoAuthMechanism):
		failure.Kind = ErrAuth
	case errors.Is(err, ErrInsecureAuth):
		// The class is authentication, because that is the step that refused;
		// the cause is kept so a caller can tell this from a rejected
		// credential.
		failure.Kind = ErrAuth
		failure.cause = ErrInsecureAuth
	default:
		var smtpErr *smtp.SMTPError
		if errors.As(err, &smtpErr) {
			failure.Code = smtpErr.Code
			failure.detail = smtpErr.Message
			failure.Kind = smtpClass(op, smtpErr)
			break
		}
		failure.Kind = ErrNetwork
	}
	return failure
}

// smtpClass maps a reply code to a class. The first digit is the whole rule —
// 4xx is transient, 5xx is permanent — and authentication answers as a class of
// its own, because a caller responds to it differently from a refused message.
func smtpClass(op Op, err *smtp.SMTPError) error {
	if op == OpAuth {
		return ErrAuth
	}
	if err.Code/100 == 4 {
		return ErrTemporary
	}
	return ErrRejected
}

// timeout reports whether err is a deadline, including one wrapped by the
// transport. A caller's cancellation is not a timeout.
func timeout(err error) bool {
	if errors.Is(err, context.Canceled) {
		return false
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return true
	}
	var netErr net.Error
	return errors.As(err, &netErr) && netErr.Timeout()
}
