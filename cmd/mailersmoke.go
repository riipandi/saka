//go:build debug

package main

import (
	"context"
	"fmt"
	"net"
	"strconv"
	"time"

	"github.com/urfave/cli/v3"

	"github.com/riipandi/tango/internal/config"
	"github.com/riipandi/tango/internal/mailer"
	"github.com/riipandi/tango/pkg/printext"
)

// mailerSmokeTemplate is the template a smoke run sends by default. It is the
// one template whose whole purpose is to say the mailer works.
const mailerSmokeTemplate = "test-email"

var mailerSmokeCmd = &cli.Command{
	Name:     "mailer:smoke",
	Category: "Development commands",
	Usage:    "Send one templated email through the configured SMTP server",
	Description: `Renders one embedded email template and submits it, so the mail path can be
proved end to end against a real server before anything depends on it.

It prints the server, the sender, the template, and where the message went:

  docker compose -f docker/compose.yaml up -d mailpit
  task mailer:smoke -- --to=you@example.com

Open the Mailpit UI at http://localhost:8025 to read the message back. A run
without a configured smtp_host reports that the mailer is off and sends
nothing; that is not a failure.`,
	Flags: []cli.Flag{
		&cli.StringFlag{
			Name:     "to",
			Usage:    "Recipient address",
			Required: true,
		},
		&cli.StringFlag{
			Name:  "template",
			Usage: "Template to send",
			Value: mailerSmokeTemplate,
		},
		&cli.StringFlag{
			Name:  "subject",
			Usage: "Subject line",
			Value: "SMTP Test Successful",
		},
	},
	Action: runMailerSmoke,
}

// runMailerSmoke builds the mailer, renders one template, and submits it.
//
// It builds the mailer itself rather than reaching for the registry, because the
// point is to report a mail server that cannot be used: a command that exists to
// diagnose mail should not fail before it can say which part broke.
func runMailerSmoke(ctx context.Context, cmd *cli.Command) error {
	p := printext.NewPalette(cmd.Root().Writer)

	cfg, err := fullConfigFrom(ctx)
	if err != nil {
		return err
	}

	client, err := mailer.New(cfg, nil)
	if err != nil {
		return err
	}
	templates, err := mailer.NewTemplates(mailer.SenderFrom(cfg))
	if err != nil {
		return err
	}

	recipient := cmd.String("to")
	if planErr := printMailerPlan(p, cfg, recipient, cmd.String("template")); planErr != nil {
		return planErr
	}

	if !client.Configured() {
		return printStatusLine(p, "mailer is not configured; set %s to send",
			p.Dim(config.EnvName("mailer.smtp_host")))
	}

	started := time.Now()
	service := mailer.NewService(client, templates)
	err = service.Send(ctx, mailer.Request{
		To:       []string{recipient},
		Subject:  cmd.String("subject"),
		Template: cmd.String("template"),
		View:     mailer.View{Email: recipient, Data: map[string]string{"email": recipient}},
	})
	if err != nil {
		return err
	}

	if err := printFields(p, []field{{"elapsed", p.Dim(printext.Duration(time.Since(started)))}}); err != nil {
		return err
	}
	return printStatusLine(p, "sent %s to %s", cmd.String("template"), recipient)
}

// printMailerPlan reports what the run is about to do, so the command's output
// is the instruction for reading the message back. The password is never part of
// it: this prints the address and the username, and the username is not a secret.
func printMailerPlan(p printext.Palette, cfg config.Config, recipient, template string) error {
	server := "(not set)"
	if cfg.Mailer.SMTPHost != "" {
		server = net.JoinHostPort(cfg.Mailer.SMTPHost, strconv.Itoa(cfg.Mailer.SMTPPort))
	}
	auth := "no"
	if cfg.Mailer.SMTPUsername != "" {
		auth = "yes"
	}
	tls := "STARTTLS"
	if cfg.Mailer.SMTPSecure {
		tls = "implicit TLS"
	}
	return printFields(p, []field{
		{"server", server},
		{"sender", fmt.Sprintf("%s <%s>", cfg.Mailer.FromName, cfg.Mailer.FromEmail)},
		{"auth", auth},
		{"tls", tls},
		{"template", template},
		{"to", recipient},
	})
}
