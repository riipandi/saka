package mailer

import "time"

// Options is the SMTP configuration the mailer runs on. It is the caller's
// resolved configuration — this package reads nothing itself.
type Options struct {
	// FromEmail and FromName are the sender every message is sent as.
	FromEmail string
	FromName  string
	// SMTPHost is the mail server. An empty host means the mailer is not
	// configured, and it is not an error.
	SMTPHost string
	// SMTPPort is the submission port: 587 for STARTTLS, 465 for implicit TLS.
	SMTPPort int
	// SMTPUsername and SMTPPassword authenticate the session.
	SMTPUsername string
	SMTPPassword string
	// SMTPSecure selects implicit TLS on connect instead of STARTTLS.
	SMTPSecure bool
	// SMTPAllowPlaintextAuth permits a credential to be sent over an
	// unencrypted connection to a host that is not this machine. It defaults to
	// false: a password in the clear is a leak, and a server that offers
	// STARTTLS is never affected. A loopback host needs no setting, because a
	// connection to it never leaves the machine.
	SMTPAllowPlaintextAuth bool
	// Timeout bounds one send: the dial, the handshake, the commands, and the
	// message body. A submission that hangs must fail rather than hold the
	// caller for as long as the kernel's own connect timeout allows. Zero means
	// no explicit bound: the SMTP library's own defaults apply.
	Timeout time.Duration
}
