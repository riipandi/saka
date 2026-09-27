package main

import (
	"fmt"
	"time"

	"github.com/riipandi/tango/database/seeders"
	"github.com/riipandi/tango/pkg/printext"
)

// printSeedResults writes one line per record the seed touched, then the
// outcome line at column zero. It lives outside the debug-tagged file
// because `initialize` — a release command — answers the same way.
func printSeedResults(p printext.Palette, results []seeders.Result, dryRun bool, elapsed time.Duration) error {
	var created, skipped int
	for _, result := range results {
		for _, key := range result.Created {
			created++
			if err := printSeedLine(p, result.Name, key, "created", "would create", dryRun); err != nil {
				return err
			}
		}
		for _, key := range result.Skipped {
			skipped++
			if err := printSeedLine(p, result.Name, key, "skipped", "would skip", dryRun); err != nil {
				return err
			}
		}
	}

	if created+skipped > 0 {
		if err := p.Printf("\n"); err != nil {
			return err
		}
	}
	if dryRun {
		return printStatusLine(p, "%s, %s %s",
			p.Yellow(fmt.Sprintf("%d to create", created)),
			p.Yellow(fmt.Sprintf("%d to skip", skipped)),
			p.Dim("in "+printext.Duration(elapsed)))
	}
	return printStatusLine(p, "%s, %s %s",
		p.Green(fmt.Sprintf("%d created", created)),
		p.Green(fmt.Sprintf("%d skipped", skipped)),
		p.Dim("in "+printext.Duration(elapsed)))
}

// printSeedLine writes one record. The seeder name comes first so the output
// sorts and greps by what was seeded. Both wordings are passed in rather than
// derived: "create" and "skip" do not share a past-tense rule.
//
// A record that was created is a success and one that was skipped already
// existed, so the two are told apart by colour.
func printSeedLine(p printext.Palette, seeder, key, verb, dryRunVerb string, dryRun bool) error {
	if dryRun {
		return p.Printf("%s%s %s %s\n", progressIndent, seeder, key, p.Yellow(dryRunVerb))
	}
	style := printext.Yellow
	if verb == "created" {
		style = printext.Green
	}
	return p.Printf("%s%s %s %s\n", progressIndent, seeder, key, p.Paint(style, verb))
}
