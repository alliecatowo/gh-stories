package main

import (
	"context"
	"flag"
	"strings"
)

// cmdImport accepts or declines importing the people you follow on GitHub.
//
// Accepting cannot finish here. The service never keeps a GitHub token, so
// reading who you follow on GitHub needs a fresh authorization in a browser —
// this prints the URL, which is also the only thing that can work over SSH.
// Declining finishes immediately and reads nothing from GitHub.
func cmdImport(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("import", flag.ContinueOnError)
	service, _, asJSON := globalFlags(fs)
	decline := fs.Bool("no", false, "decline the import and stop being asked")
	if err := fs.Parse(args); err != nil {
		return usagef("gh stories import [--no]")
	}
	if fs.NArg() > 0 {
		return usagef("gh stories import [--no]")
	}
	sess, err := openSession(*service, *asJSON, "auto")
	if err != nil {
		return err
	}

	result, err := sess.Client.ImportFollows(ctx, !*decline)
	if err != nil {
		return err
	}
	if *asJSON {
		return jsonOut(result)
	}

	if *decline {
		out("Not importing. You can still follow people one at a time:")
		out("  gh stories follow maya")
		return nil
	}

	if result.NeedsGitHubAuthorization {
		url := result.AuthorizationURL
		if url == "" {
			url = sess.ServiceURL + "/v1/auth/github/start?purpose=import"
		}
		out("Importing reads who you follow on GitHub, so GitHub has to authorize it.")
		out("")
		out("  Open: %s", url)
		out("")
		out("You will see the summary there. This never changes who you follow on GitHub.")
		if !detectSSH() {
			if err := openBrowser(url); err == nil {
				status("Opened your browser.")
			}
		}
		return nil
	}

	// A service that could import without a browser would land here.
	out("Now following %d new account(s).", result.Added)
	if result.AlreadyFollowing > 0 {
		out("%d you already followed here.", result.AlreadyFollowing)
	}
	if result.SkippedUnfollowed > 0 {
		out("%d skipped because you unfollowed them here before.", result.SkippedUnfollowed)
	}
	if result.SkippedBlocked > 0 {
		out("%d skipped because of a block.", result.SkippedBlocked)
	}
	if len(result.Sample) > 0 {
		names := make([]string, 0, len(result.Sample))
		for _, u := range result.Sample {
			names = append(names, u.Login)
		}
		out("For example: %s.", strings.Join(names, ", "))
	}
	return nil
}
