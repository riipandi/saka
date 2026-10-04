package datastore

import (
	"context"
	"fmt"
	"time"

	"github.com/valkey-io/valkey-go"
)

// ValkeyOptions configures the optional key-value backend connection. Zero
// fields fall back to what the URL carries; only URL is required.
type ValkeyOptions struct {
	// URL is the connection string, such as
	// redis://default:password@localhost:6379/0. The schemes redis, rediss,
	// and unix are the ones the backend accepts.
	URL string
	// DB selects the logical database. A negative value leaves the index
	// the URL names alone, which is how a URL that already carries /2 is
	// honoured.
	DB int
	// ApplicationName is the client name the connection registers itself
	// under, so an operator can tell the application's connections apart
	// in the server's client list.
	ApplicationName string
}

// Valkey owns the single key-value backend client of the process. Nothing
// else may open another one: a second client would be a second cache of the
// same server that cannot be invalidated together.
type Valkey struct {
	client valkey.Client
}

// NewValkey opens the backend client and verifies connectivity, so an
// unreachable server fails at startup instead of on the first command.
func NewValkey(ctx context.Context, opts ValkeyOptions) (*Valkey, error) {
	if opts.URL == "" {
		return nil, fmt.Errorf("datastore: valkey: %w", ErrNoValkeyURL)
	}

	opt, err := valkey.ParseURL(opts.URL)
	if err != nil {
		return nil, fmt.Errorf("datastore: valkey: parse url: %w", err)
	}
	if opts.DB >= 0 {
		opt.SelectDB = opts.DB
	}
	if opts.ApplicationName != "" {
		opt.ClientName = opts.ApplicationName
	}

	client, err := valkey.NewClient(opt)
	if err != nil {
		return nil, fmt.Errorf("datastore: valkey: connect: %w", err)
	}

	v := &Valkey{client: client}
	if err := v.Ping(ctx); err != nil {
		// The error carries no target: a URL is a composite value where
		// revealing part of it says nothing, so the caller renders the
		// target itself (config.RedactKVURL is the one rendering).
		client.Close()
		return nil, fmt.Errorf("datastore: valkey: ping: %w", err)
	}
	return v, nil
}

// Ping reports whether the backend answers. It is the probe the health check
// runs; a pool that is connected but unresponsive fails here.
func (v *Valkey) Ping(ctx context.Context) error {
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()

	cmd := v.client.B().Ping().Build()
	if err := v.client.Do(ctx, cmd).Error(); err != nil {
		return fmt.Errorf("valkey ping: %w", err)
	}
	return nil
}

// Client hands out the underlying client for a feature that owns commands of
// its own, such as a session store or a rate limiter. The client is shared:
// a feature builds commands on it, it never opens a second one.
func (v *Valkey) Client() valkey.Client {
	return v.client
}

// Shutdown releases the client, and is what the container calls when the run
// ends. It is named Shutdown rather than Close because that is the interface
// samber/do looks for: the container calls `Shutdown` on every service that
// implements it and ignores `Close` entirely, so a client exposing only Close
// was never released by a run that shut down through the container.
func (v *Valkey) Shutdown(context.Context) {
	// A handle that was never opened is not a failure to report: the
	// container calls this on whatever it holds, and a test that registers a
	// stub registers a zero value.
	if v == nil || v.client == nil {
		return
	}
	v.client.Close()
}

// ErrNoValkeyURL is returned when the backend is opened without a URL. The
// driver is opt-in, so a missing URL is a configuration decision reported
// plainly, not a fallback to something that would silently disagree.
var ErrNoValkeyURL = errNoValkeyURL{}

type errNoValkeyURL struct{}

func (errNoValkeyURL) Error() string { return "no valkey url configured" }
