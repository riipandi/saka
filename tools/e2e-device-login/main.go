// e2e-device-login exercises the device login protocol end to end against
// a running Saka server: it opens a pairing request, holds the pairing
// cookie the way the creating browser would, prints the user code and
// the verification URL for a human to approve at /device, and polls the
// exchange until the decision lands. The approval mints the session the
// wire answers with — the tool prints the account view as its proof.
//
// Usage:
//
//	go run ./tools/e2e-device-login --server http://localhost:3000 --timeout 5m
package main

import (
	"context"
	"encoding/json/v2"
	"errors"
	"flag"
	"fmt"
	"net/http"
	"net/http/cookiejar"
	"os"
	"os/signal"
	"strings"
	"time"
)

func main() {
	server := flag.String("server", "http://localhost:3000", "base URL of the Saka server")
	timeout := flag.Duration("timeout", 5*time.Minute, "how long to wait for the approval")
	flag.Parse()

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	ctx, cancel := context.WithTimeout(ctx, *timeout)
	defer cancel()

	if err := run(ctx, strings.TrimRight(*server, "/")); err != nil {
		if errors.Is(err, context.DeadlineExceeded) {
			fmt.Fprintln(os.Stderr, "\nthe pairing request expired before an approval arrived")
			os.Exit(2)
		}
		if errors.Is(err, context.Canceled) {
			fmt.Fprintln(os.Stderr, "\nstopped")
			os.Exit(130)
		}
		fmt.Fprintln(os.Stderr, "\nerror:", err)
		os.Exit(1)
	}
}

func run(ctx context.Context, server string) error {
	jar, err := cookiejar.New(nil)
	if err != nil {
		return fmt.Errorf("cookie jar: %w", err)
	}
	client := &http.Client{Jar: jar}

	view, err := createRequest(ctx, client, server)
	if err != nil {
		return err
	}

	fmt.Println()
	fmt.Printf("  Pairing Code: %s\n", view.UserCode)
	fmt.Printf("  Verification URL: %s\n", view.VerificationURIComplete)
	fmt.Println()
	fmt.Println("Waiting for approval in browser...")

	account, err := pollExchange(ctx, client, server, view)
	if err != nil {
		return err
	}

	fmt.Println()
	fmt.Println("Device approved. The session mints against:")
	fmt.Printf("  Account ID:   %s\n", account.ID)
	fmt.Printf("  Username:     %s\n", account.Username)
	return nil
}

// createdView mirrors the create answer's wire shape — the code and URLs
// the device page renders, the poll rhythm the interval names.
type createdView struct {
	ID                      string    `json:"id"`
	UserCode                string    `json:"user_code"`
	VerificationURI         string    `json:"verification_uri"`
	VerificationURIComplete string    `json:"verification_uri_complete"`
	ExpiresAt               time.Time `json:"expires_at"`
	Interval                int       `json:"interval"`
}

// accountView mirrors the approved exchange's wire shape — the account
// the approval names, which the mint turns into the session.
type accountView struct {
	ID       string `json:"ID"`
	Username string `json:"Username"`
}

func createRequest(ctx context.Context, client *http.Client, server string) (createdView, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, server+"/api/device-login/requests", nil)
	if err != nil {
		return createdView{}, fmt.Errorf("build create request: %w", err)
	}

	resp, err := client.Do(req)
	if err != nil {
		return createdView{}, fmt.Errorf("create pairing request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusCreated {
		return createdView{}, fmt.Errorf("create pairing request: unexpected status %s (%s)", resp.Status, describeError(resp))
	}

	var view createdView
	if err := json.UnmarshalRead(resp.Body, &view); err != nil {
		return createdView{}, fmt.Errorf("decode create answer: %w", err)
	}
	if view.UserCode == "" || view.VerificationURIComplete == "" {
		return createdView{}, errors.New("create pairing request: the answer carries no code or verification URL")
	}
	return view, nil
}

// pollExchange rides the exchange's own rhythm: each turn long-polls up
// to the server's window, 202 means the decision has not landed yet and
// the device spins again, 200 answers the account view, and a refusal
// (unknown, expired, or already consumed) ends the flow.
func pollExchange(ctx context.Context, client *http.Client, server string, view createdView) (accountView, error) {
	pause := time.Duration(view.Interval) * time.Second
	if pause <= 0 {
		pause = 3 * time.Second
	}

	for {
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, server+"/api/device-login/requests/"+view.ID+"/exchange", nil)
		if err != nil {
			return accountView{}, fmt.Errorf("build exchange request: %w", err)
		}

		resp, err := client.Do(req)
		if err != nil {
			return accountView{}, fmt.Errorf("exchange: %w", err)
		}

		switch resp.StatusCode {
		case http.StatusOK:
			var account accountView
			if err := json.UnmarshalRead(resp.Body, &account); err != nil {
				resp.Body.Close()
				return accountView{}, fmt.Errorf("decode exchange answer: %w", err)
			}
			resp.Body.Close()
			return account, nil

		case http.StatusAccepted:
			resp.Body.Close()

		default:
			defer resp.Body.Close()
			return accountView{}, fmt.Errorf("exchange: unexpected status %s (%s)", resp.Status, describeError(resp))
		}

		fmt.Printf("  still waiting (next poll in %s)...\n", pause)
		select {
		case <-time.After(pause):
		case <-ctx.Done():
			return accountView{}, ctx.Err()
		}
	}
}

// describeError reads the server's error envelope for its human message,
// so a refusal names itself instead of a bare status code.
func describeError(resp *http.Response) string {
	var body struct {
		Message string `json:"message"`
	}
	if err := json.UnmarshalRead(resp.Body, &body); err != nil || body.Message == "" {
		return "no detail"
	}
	return body.Message
}
