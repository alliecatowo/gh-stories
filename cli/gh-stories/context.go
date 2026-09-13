package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"strings"
	"time"

	"golang.org/x/term"

	"github.com/alliecatowo/gh-stories/internal/cliapi"
	"github.com/alliecatowo/gh-stories/internal/credstore"
	"github.com/alliecatowo/gh-stories/internal/terminal"
	"github.com/alliecatowo/gh-stories/internal/version"
)

var errNotSignedIn = errors.New("not signed in")
var errProcessingFailed = errors.New("media processing failed")

// session carries everything a command needs.
type session struct {
	Client     *cliapi.Client
	ServiceURL string
	Login      string
	Store      credstore.Store
	JSON       bool
	Renderer   terminal.Protocol
}

// globalFlags registers the flags every command accepts.
func globalFlags(fs *flag.FlagSet) (service, renderer *string, asJSON *bool) {
	service = fs.String("service", "", "GitHub Stories service URL")
	renderer = fs.String("renderer", "auto", "auto | kitty | iterm | external")
	asJSON = fs.Bool("json", false, "machine-readable output")
	return
}

// serviceURL resolves which service to talk to.
//
// Release artifacts target the live service by default; an explicit override
// is what makes self-hosting work without rebuilding anything.
func serviceURL(flagValue string) string {
	if flagValue != "" {
		return strings.TrimSuffix(flagValue, "/")
	}
	if v := os.Getenv("GHS_SERVICE_URL"); v != "" {
		return strings.TrimSuffix(v, "/")
	}
	return strings.TrimSuffix(version.DefaultServiceURL, "/")
}

// openSession loads stored credentials and builds an API client.
func openSession(service string, asJSON bool, renderer string) (*session, error) {
	url := serviceURL(service)
	store, err := credstore.Open()
	if err != nil {
		return nil, fmt.Errorf("could not open a credential store: %w", err)
	}
	cred, err := store.Load(url)
	if err != nil || cred == nil || cred.Token == "" {
		return nil, errNotSignedIn
	}
	return &session{
		Client:     cliapi.New(url, cred.Token),
		ServiceURL: url,
		Login:      cred.Login,
		Store:      store,
		JSON:       asJSON,
		Renderer:   terminal.Protocol(renderer),
	}, nil
}

// anonymousSession is for commands that run before sign-in.
func anonymousSession(service string) *session {
	url := serviceURL(service)
	return &session{Client: cliapi.New(url, ""), ServiceURL: url}
}

// interactive reports whether we may draw a full-screen application, prompt,
// or emit graphics.
//
// When stdin or stdout is not a terminal we must not launch a full-screen app,
// must not emit graphics escapes, and must not hang waiting for input.
func interactive() bool {
	return term.IsTerminal(int(os.Stdin.Fd())) && term.IsTerminal(int(os.Stdout.Fd()))
}

func stdoutIsTTY() bool { return term.IsTerminal(int(os.Stdout.Fd())) }

// colorEnabled honours the conventional environment overrides.
func colorEnabled() bool {
	if os.Getenv("NO_COLOR") != "" {
		return false
	}
	if os.Getenv("CLICOLOR_FORCE") != "" && os.Getenv("CLICOLOR_FORCE") != "0" {
		return true
	}
	return stdoutIsTTY()
}

// detectTerminal probes the terminal, bounded, restoring state afterwards.
func detectTerminal(ctx context.Context, override terminal.Protocol) terminal.Capabilities {
	probeCtx, cancel := context.WithTimeout(ctx, 500*time.Millisecond)
	defer cancel()
	caps, err := terminal.Detect(probeCtx, os.Stdin, os.Stdout, override)
	if err != nil {
		return terminal.Capabilities{
			Protocol: terminal.ProtocolExternal,
			Reason:   "capability detection failed: " + err.Error(),
		}
	}
	return caps
}

// out prints to stdout, sanitised, because much of what we print originates
// from other people's accounts.
func out(format string, a ...any) {
	fmt.Fprintln(os.Stdout, terminal.Sanitize(fmt.Sprintf(format, a...)))
}

// status prints progress and human chatter to stderr, keeping stdout clean for
// machine-readable output.
func status(format string, a ...any) {
	fmt.Fprintln(os.Stderr, terminal.Sanitize(fmt.Sprintf(format, a...)))
}

// parseArgs parses flags that may appear before, after, or between positional
// arguments.
//
// Go's flag package stops at the first non-flag argument, which would make
// `gh stories post cat.jpg --caption "…"` silently drop the caption. Users
// reasonably expect the file first, so we parse repeatedly, peeling off one
// positional at a time.
func parseArgs(fs *flag.FlagSet, args []string) ([]string, error) {
	var positional []string
	rest := args
	for {
		if err := fs.Parse(rest); err != nil {
			return nil, err
		}
		if fs.NArg() == 0 {
			return positional, nil
		}
		positional = append(positional, fs.Arg(0))
		rest = fs.Args()[1:]
	}
}
