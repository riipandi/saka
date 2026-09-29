package jobs

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"github.com/riipandi/tango/internal/fetcher"
	"github.com/riipandi/tango/internal/queue"
)

// BackchannelLogoutName is the queue the OIDC back-channel logout
// deliveries run on.
const BackchannelLogoutName = "backchannel_logout"

// BackchannelLogoutTask is one delivery: a signed logout token, and the
// relying party's registered destination. The token is compact and
// short-lived — the payload the queue carries is the delivery's whole
// state, and a redelivery of an expired token is the client's refusal,
// which is a safe outcome.
type BackchannelLogoutTask struct {
	ClientID string `json:"client_id"`
	URL      string `json:"url"`
	Token    string `json:"token"`
}

// Config returns the queue the deliveries run on. The attempts are few:
// an RP that cannot accept a logout token in five tries is down, and the
// hourly account deletes — the other path the token covers — are rarer
// still than the outage.
func (t BackchannelLogoutTask) Config() queue.QueueConfig {
	return queue.QueueConfig{
		Name:        BackchannelLogoutName,
		MaxAttempts: 5,
		Timeout:     30 * time.Second,
		Backoff:     time.Minute,
	}
}

// backchannelLogoutProcessor POSTs the logout token the way the
// specification names: form-encoded, one member, expecting an empty 200.
// Anything else — a 4xx the client refused the token with, a 5xx, a
// transport failure — is the attempt's failure, and the queue's retry
// owns the next one.
func backchannelLogoutProcessor(ctx context.Context, task BackchannelLogoutTask, client *fetcher.Client) error {
	if client == nil {
		return errors.New("backchannel_logout: no fetch client is wired")
	}
	if task.URL == "" || task.Token == "" {
		return errors.New("backchannel_logout: the delivery names no destination or token")
	}

	res, err := client.Do(ctx, fetcher.Request{
		Method: http.MethodPost,
		URL:    task.URL,
		Headers: http.Header{
			"Content-Type": []string{"application/x-www-form-urlencoded"},
		},
		Body: "logout_token=" + task.Token,
	})
	if err != nil {
		return fmt.Errorf("backchannel_logout: deliver to %s: %w", task.URL, err)
	}
	if res.StatusCode != http.StatusOK {
		return fmt.Errorf("backchannel_logout: %s answered %d", task.URL, res.StatusCode)
	}
	slog.InfoContext(ctx, "federation: back-channel logout delivered",
		"client_id", task.ClientID, "status", res.StatusCode)
	return nil
}
