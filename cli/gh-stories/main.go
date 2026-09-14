// Command gh-stories is the GitHub CLI extension for GitHub Stories.
//
// Installed with `gh extension install alliecatowo/gh-stories`, it becomes
// `gh stories`. `gh stories setup` additionally offers the singular alias so
// `gh story post cat.jpg` works; everything documented is available under
// `gh stories` with no setup at all.
package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"github.com/alliecatowo/gh-stories/internal/cliapi"
	"github.com/alliecatowo/gh-stories/internal/version"
)

// Exit codes are part of the CLI's contract; scripts depend on them.
const (
	exitOK           = 0
	exitError        = 1 // generic failure
	exitUsage        = 2 // bad arguments
	exitNotAuthed    = 3 // not signed in
	exitNoAccess     = 4 // forbidden, not found, or no access
	exitNetwork      = 5 // network or service unavailable
	exitMediaFailure = 6 // media processing failed
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(),
		os.Interrupt, syscall.SIGTERM)
	defer stop()
	os.Exit(dispatch(ctx, os.Args[1:]))
}

func dispatch(ctx context.Context, args []string) int {
	if len(args) == 0 {
		return run(ctx, cmdView, nil)
	}
	switch args[0] {
	case "-h", "--help", "help":
		usage(os.Stdout)
		return exitOK
	case "version", "--version", "-v":
		fmt.Printf("gh-stories %s\nbuilt %s\n", version.Short(), version.BuildDate)
		return exitOK
	case "login":
		return run(ctx, cmdLogin, args[1:])
	case "logout":
		return run(ctx, cmdLogout, args[1:])
	case "setup":
		return run(ctx, cmdSetup, args[1:])
	case "doctor":
		return run(ctx, cmdDoctor, args[1:])
	case "import":
		return run(ctx, cmdImport, args[1:])
	case "post":
		return run(ctx, cmdPost, args[1:])
	case "reply":
		return run(ctx, cmdReply, args[1:])
	case "react":
		return run(ctx, cmdReact, args[1:])
	case "delete":
		return run(ctx, cmdDelete, args[1:])
	case "viewers":
		return run(ctx, cmdViewers, args[1:])
	case "inbox":
		return run(ctx, cmdInbox, args[1:])
	case "report":
		return run(ctx, cmdReport, args[1:])
	case "settings":
		return run(ctx, cmdSettings, args[1:])
	case "follow", "unfollow", "mute", "unmute", "block", "unblock":
		return run(ctx, relationCommand(args[0]), args[1:])
	}
	if strings.HasPrefix(args[0], "@") {
		return run(ctx, cmdView, args)
	}
	if strings.HasPrefix(args[0], "-") {
		return run(ctx, cmdView, args)
	}
	fmt.Fprintf(os.Stderr, "gh stories: unknown command %q\n\n", args[0])
	usage(os.Stderr)
	return exitUsage
}

type command func(context.Context, []string) error

func run(ctx context.Context, fn command, args []string) int {
	err := fn(ctx, args)
	if err == nil {
		return exitOK
	}
	if errors.Is(err, context.Canceled) {
		fmt.Fprintln(os.Stderr, "\nCancelled.")
		return exitError
	}
	var usageErr *usageError
	if errors.As(err, &usageErr) {
		fmt.Fprintln(os.Stderr, "gh stories: "+usageErr.Error())
		return exitUsage
	}
	fmt.Fprintln(os.Stderr, "gh stories: "+humanError(err))
	return codeFor(err)
}

type usageError struct{ msg string }

func (e *usageError) Error() string { return e.msg }
func usagef(format string, a ...any) error {
	return &usageError{msg: fmt.Sprintf(format, a...)}
}

// codeFor maps an error onto the documented exit codes.
func codeFor(err error) int {
	var apiErr *cliapi.APIError
	if errors.As(err, &apiErr) {
		switch apiErr.Code {
		case "unauthorized":
			return exitNotAuthed
		case "forbidden", "not_found":
			return exitNoAccess
		case "rate_limited", "unavailable":
			return exitNetwork
		case "unsupported_media", "payload_too_large":
			return exitMediaFailure
		}
		if apiErr.Status >= 500 {
			return exitNetwork
		}
		return exitError
	}
	var netErr *cliapi.NetworkError
	if errors.As(err, &netErr) {
		return exitNetwork
	}
	if errors.Is(err, errNotSignedIn) {
		return exitNotAuthed
	}
	if errors.Is(err, errProcessingFailed) {
		return exitMediaFailure
	}
	return exitError
}

// humanError turns an error into an actionable sentence rather than a Go dump.
func humanError(err error) string {
	var apiErr *cliapi.APIError
	if errors.As(err, &apiErr) {
		switch apiErr.Code {
		case "unauthorized":
			return "you are not signed in. Run: gh stories login"
		case "not_found":
			return "not found, or you do not have access to it."
		case "forbidden":
			if apiErr.Message != "" {
				return apiErr.Message
			}
			return "you do not have access to that."
		case "rate_limited":
			if apiErr.RetryAfter > 0 {
				return fmt.Sprintf("too many requests. Try again in %ds.", apiErr.RetryAfter)
			}
			return "too many requests. Try again shortly."
		}
		if apiErr.Message != "" {
			return apiErr.Message
		}
		return fmt.Sprintf("the service returned %d.", apiErr.Status)
	}
	var netErr *cliapi.NetworkError
	if errors.As(err, &netErr) {
		return "could not reach the service. Check your connection, or run: gh stories doctor"
	}
	return err.Error()
}

func usage(w *os.File) {
	fmt.Fprint(w, `gh stories — Stories for GitHub. Yes, those Stories.

USAGE
  gh stories                       open the Stories viewer
  gh stories @alice                open one person's Stories

  gh stories login [--no-browser]  sign in (works over SSH)
  gh stories logout                sign out and revoke this session
  gh stories setup                 offer the singular "gh story" alias
  gh stories doctor                check the service, auth and terminal
  gh stories import               follow the people you follow on GitHub

  gh stories post photo.jpg        post an image
  gh stories post video.mp4        post a video
  cat shot.png | gh stories post - --filename shot.png

  gh stories reply @alice "lmao"   reply privately
  gh stories reply --story ID "…"
  gh stories react @alice '❤️'
  gh stories react --story ID '🔥'
  gh stories delete STORY_ID
  gh stories viewers STORY_ID      who viewed your Story
  gh stories inbox                 replies, reactions, new followers
  gh stories report --story ID

  gh stories follow @alice         follow on Stories (never touches GitHub)
  gh stories unfollow|mute|unmute|block|unblock @alice
  gh stories settings              audiences, privacy, sessions
  gh stories settings --create-list "close friends" --members @maya,@sam
  gh stories settings --list "close friends" --members @maya
  gh stories settings --delete-list "close friends"
  gh stories version

GLOBAL FLAGS
  --service URL   use a different GitHub Stories service
  --renderer M    auto | kitty | iterm | external
  --json          machine-readable output where supported

EXIT CODES
  0 ok   1 error   2 usage   3 not signed in
  4 no access   5 network   6 media processing failed

Stories expire 24 hours after they are published.
`)
}
