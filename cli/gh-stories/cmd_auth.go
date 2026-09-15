package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"time"

	"github.com/alliecatowo/gh-stories/internal/credstore"
	"github.com/alliecatowo/gh-stories/internal/media"
	"github.com/alliecatowo/gh-stories/internal/terminal"
	"github.com/alliecatowo/gh-stories/internal/version"
)

// cmdLogin performs the service-mediated authorization flow.
//
// The CLI never reads, extracts or uploads the user's existing `gh` token.
// By default it asks the service for a pending authorization, shows a code,
// and polls until a human approves it in a browser. That is what makes this
// work over SSH, where the remote host has no browser at all.
//
// With --device it uses GitHub's Device Authorization Grant instead: the
// service shows GitHub's own verification URI and user code, and the user
// authorizes the GitHub App directly on github.com.
func cmdLogin(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("login", flag.ContinueOnError)
	service, _, _ := globalFlags(fs)
	noBrowser := fs.Bool("no-browser", false, "print the URL instead of opening a browser")
	device := fs.Bool("device", false, "sign in with GitHub's device authorization flow")
	if err := fs.Parse(args); err != nil {
		return usagef("gh stories login [--device] [--no-browser] [--service URL]")
	}

	sess, err := anonymousSession(*service)
	if err != nil {
		return err
	}
	label := clientLabel()

	if *device {
		return cmdLoginDevice(ctx, sess, *noBrowser, label)
	}

	pending, err := sess.Client.CreatePendingLogin(ctx, "cli", label)
	if err != nil {
		return err
	}

	status("")
	status("  Open:  %s", pending.VerificationURL)
	status("  Code:  %s", pending.UserCode)
	status("")
	status("Check that the code above matches the one on the page before you approve.")

	openedBrowser := false
	if !*noBrowser && !detectSSH() {
		if err := openBrowser(pending.VerificationURL); err == nil {
			openedBrowser = true
			status("Opened your browser.")
		}
	}
	if !openedBrowser {
		status("Open that URL on any device where you are signed in to GitHub.")
	}
	status("Waiting for approval…")

	interval := time.Duration(pending.IntervalSeconds) * time.Second
	if interval < time.Second {
		interval = 2 * time.Second
	}
	deadline := time.Now().Add(10 * time.Minute)

	for time.Now().Before(deadline) {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(interval):
		}
		poll, err := sess.Client.PollPendingLogin(ctx, pending.PendingLoginID, pending.PollingSecret)
		if err != nil {
			return err
		}
		switch poll.Status {
		case "approved":
			store, err := credstore.Open()
			if err != nil {
				return fmt.Errorf("could not open a credential store: %w", err)
			}
			cred := credstore.Credential{
				ServiceURL: sess.ServiceURL, Token: poll.Token, StoredAt: time.Now(),
			}
			if poll.User != nil {
				cred.Login = poll.User.Login
				cred.UserID = poll.User.GitHubID
			}
			if err := store.Save(cred); err != nil {
				return fmt.Errorf("could not save your session: %w", err)
			}
			status("")
			status("Signed in as %s.", cred.Login)
			if store.Backend() != "keyring" {
				status("No OS keyring was available, so the session was saved to a")
				status("permission-restricted file instead. Run `gh stories doctor` for details.")
			}
			return nil
		case "denied":
			return fmt.Errorf("the authorization was denied")
		case "expired":
			return fmt.Errorf("the authorization expired. Run `gh stories login` again")
		}
	}
	return fmt.Errorf("timed out waiting for approval")
}

// cmdLoginDevice performs the GitHub Device Authorization Grant login. The
// service starts the grant and polls GitHub on the CLI's behalf; this
// command only relays the user code and keeps polling the service until the
// user authorizes (or the grant expires) on github.com.
func cmdLoginDevice(ctx context.Context, sess *session, noBrowser bool, label string) error {
	device, err := sess.Client.StartDeviceLogin(ctx, "cli", label)
	if err != nil {
		return err
	}

	status("")
	status("  Open:  %s", device.VerificationURI)
	status("  Code:  %s", device.UserCode)
	status("")
	status("Enter the code above on the page to authorize this device.")

	openedBrowser := false
	if !noBrowser && !detectSSH() {
		if err := openBrowser(device.VerificationURI); err == nil {
			openedBrowser = true
			status("Opened your browser.")
		}
	}
	if !openedBrowser {
		status("Open that URL on any device where you are signed in to GitHub.")
	}
	status("Waiting for approval…")

	interval := time.Duration(device.IntervalSeconds) * time.Second
	if interval < time.Second {
		interval = 5 * time.Second
	}
	deadline := device.ExpiresAt
	if deadline.IsZero() || !deadline.After(time.Now()) {
		// The contract requires expires_at, but retain a bounded fallback for
		// an older or misbehaving service rather than polling indefinitely.
		deadline = time.Now().Add(10 * time.Minute)
	}

	for time.Now().Before(deadline) {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(interval):
		}
		poll, err := sess.Client.PollDeviceLogin(ctx, device.DeviceLoginID)
		if err != nil {
			return err
		}
		switch poll.Status {
		case "slow_down":
			interval += 5 * time.Second
		case "approved":
			store, err := credstore.Open()
			if err != nil {
				return fmt.Errorf("could not open a credential store: %w", err)
			}
			cred := credstore.Credential{
				ServiceURL: sess.ServiceURL, Token: poll.Token, StoredAt: time.Now(),
			}
			if poll.User != nil {
				cred.Login = poll.User.Login
				cred.UserID = poll.User.GitHubID
			}
			if err := store.Save(cred); err != nil {
				return fmt.Errorf("could not save your session: %w", err)
			}
			status("")
			status("Signed in as %s.", cred.Login)
			if store.Backend() != "keyring" {
				status("No OS keyring was available, so the session was saved to a")
				status("permission-restricted file instead. Run `gh stories doctor` for details.")
			}
			return nil
		case "denied":
			return fmt.Errorf("the authorization was denied")
		case "expired":
			return fmt.Errorf("the authorization expired. Run `gh stories login --device` again")
		}
	}
	return fmt.Errorf("timed out waiting for approval")
}

func cmdLogout(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("logout", flag.ContinueOnError)
	service, _, _ := globalFlags(fs)
	if err := fs.Parse(args); err != nil {
		return usagef("gh stories logout")
	}
	sess, err := openSession(*service, false, "auto")
	if err != nil {
		if err == errNotSignedIn {
			status("Not signed in.")
			return nil
		}
		return err
	}
	// Revoke server side first, so the token is dead even if the local delete
	// fails; then remove it locally.
	if err := sess.Client.Logout(ctx); err != nil {
		status("Could not revoke the session on the service: %s", humanError(err))
	}
	if err := sess.Store.Delete(sess.ServiceURL); err != nil {
		return fmt.Errorf("could not remove the stored session: %w", err)
	}
	status("Signed out.")
	return nil
}

// cmdSetup offers the singular alias. It never clobbers an existing alias or
// a real gh command.
func cmdSetup(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("setup", flag.ContinueOnError)
	yes := fs.Bool("yes", false, "do not prompt")
	if err := fs.Parse(args); err != nil {
		return usagef("gh stories setup [--yes]")
	}

	out("Everything works under `gh stories` with no setup.")
	out("")
	out("This adds a convenience alias so the singular form works too:")
	out("")
	out("    gh alias set story stories")
	out("")
	out("Then `gh story post cat.jpg` does the same as `gh stories post cat.jpg`.")
	out("")

	if _, err := exec.LookPath("gh"); err != nil {
		return fmt.Errorf("the GitHub CLI (gh) is not on your PATH, so the alias cannot be set")
	}

	// Refuse to clobber anything that already answers to `story`.
	if existing, ok := existingAlias(ctx, "story"); ok {
		out("You already have an alias for `story`:")
		out("    story: %s", existing)
		out("")
		out("Leaving it alone. Nothing was changed.")
		return nil
	}
	if isRealGhCommand(ctx, "story") {
		out("`gh story` is already a real gh command. Leaving it alone.")
		return nil
	}

	if !*yes && interactive() {
		fmt.Fprint(os.Stderr, "Set the alias now? [y/N] ")
		var answer string
		_, _ = fmt.Fscanln(os.Stdin, &answer)
		if !strings.EqualFold(strings.TrimSpace(answer), "y") {
			out("Nothing was changed.")
			return nil
		}
	} else if !*yes {
		out("Run with --yes to set it noninteractively.")
		return nil
	}

	cmd := exec.CommandContext(ctx, "gh", "alias", "set", "story", "stories")
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("could not set the alias: %s", strings.TrimSpace(string(output)))
	}
	out("Done. `gh story post cat.jpg` now works.")
	return nil
}

// existingAlias reports an alias already bound to name.
//
// `gh alias list` has used both "name<TAB>expansion" and "name: expansion"
// across versions, so both are accepted. Getting this wrong is not cosmetic:
// a missed alias falls through to the "is this a real gh command" check, which
// answers yes precisely BECAUSE the alias resolves — and the user is then told
// something false about their own configuration.
func existingAlias(ctx context.Context, name string) (string, bool) {
	cmd := exec.CommandContext(ctx, "gh", "alias", "list")
	output, err := cmd.Output()
	if err != nil {
		return "", false
	}
	for _, line := range strings.Split(string(output), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		var key, expansion string
		if k, rest, ok := strings.Cut(line, "\t"); ok {
			key, expansion = k, rest
		} else if k, rest, ok := strings.Cut(line, ":"); ok {
			key, expansion = k, rest
		} else {
			continue
		}
		if strings.TrimSpace(strings.TrimSuffix(key, ":")) == name {
			return strings.TrimSpace(expansion), true
		}
	}
	return "", false
}

// isRealGhCommand reports whether gh already has a built-in by this name.
func isRealGhCommand(ctx context.Context, name string) bool {
	cmd := exec.CommandContext(ctx, "gh", name, "--help")
	if err := cmd.Run(); err != nil {
		return false
	}
	return true
}

// cmdDoctor reports what is configured and what is missing. It never prints a
// token or any other secret.
func cmdDoctor(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("doctor", flag.ContinueOnError)
	service, renderer, _ := globalFlags(fs)
	if err := fs.Parse(args); err != nil {
		return usagef("gh stories doctor")
	}
	url := serviceURL(*service)

	out("gh stories %s", version.Short())
	out("")
	out("Service")
	if url == "" {
		out("  url                (none configured)")
		out("  next step          set GHS_SERVICE_URL, or pass --service URL")
	} else {
		out("  url                %s", url)
	}

	if url != "" {
		probeCtx, cancel := context.WithTimeout(ctx, 8*time.Second)
		defer cancel()
		if err := pingService(probeCtx, url); err != nil {
			out("  reachable          no — %s", err)
		} else {
			out("  reachable          yes")
		}
	}

	out("")
	out("Account")
	sess, err := openSession(*service, false, "auto")
	switch {
	case errors.Is(err, errNoService):
		// The multi-line explanation belongs to the failing command, not to
		// this one-line-per-fact report.
		out("  signed in          n/a — no service configured")
	case errors.Is(err, errNotSignedIn):
		out("  signed in          no — run: gh stories login")
	case err != nil:
		out("  signed in          unknown — %s", firstLine(err.Error()))
	default:
		out("  credential store   %s", sess.Store.Backend())
		if sess.Store.Backend() != "keyring" {
			out("                     (no OS keyring available; using a 0600 file)")
		}
		meCtx, cancel := context.WithTimeout(ctx, 8*time.Second)
		defer cancel()
		me, err := sess.Client.Me(meCtx)
		if err != nil {
			out("  signed in          token present but rejected — run: gh stories login")
		} else {
			out("  signed in          yes, as %s", me.User.Login)
			out("  unread inbox       %d", me.UnreadInbox)
		}
	}

	out("")
	out("Terminal")
	caps := detectTerminal(ctx, terminal.Protocol(*renderer))
	out("  renderer           %s", caps.Protocol)
	out("  reason             %s", caps.Reason)
	if caps.ColsCells > 0 {
		out("  size               %d×%d cells", caps.ColsCells, caps.RowsCells)
	}
	if caps.CellWidthPx > 0 {
		out("  cell               %d×%d px", caps.CellWidthPx, caps.CellHeightPx)
	}
	out("  tmux               %v%s", caps.InTmux, tmuxNote(caps))
	out("  ssh                %v", caps.OverSSH)
	out("  stdout is a tty    %v", stdoutIsTTY())
	out("  colour             %v", colorEnabled())

	out("")
	out("Tools")
	if path, err := exec.LookPath("gh"); err == nil {
		out("  gh                 %s", path)
		if alias, ok := existingAlias(ctx, "story"); ok {
			out("  gh story alias     set → %s", alias)
		} else {
			out("  gh story alias     not set — run: gh stories setup")
		}
	} else {
		out("  gh                 not found on PATH")
	}
	if ff, ok := media.FFmpegAvailable(); ok {
		out("  ffmpeg             %s", ff)
	} else {
		out("  ffmpeg             not found (only needed by the service, not the CLI)")
	}
	return nil
}

func tmuxNote(caps terminal.Capabilities) string {
	if !caps.InTmux {
		return ""
	}
	if caps.TmuxPassthroughOK {
		return " (passthrough verified)"
	}
	return " (passthrough NOT enabled — images will fall back)"
}

func clientLabel() string {
	host, err := os.Hostname()
	if err != nil || host == "" {
		host = "unknown host"
	}
	return fmt.Sprintf("gh stories on %s (%s/%s)", host, runtime.GOOS, runtime.GOARCH)
}

func detectSSH() bool {
	return os.Getenv("SSH_CONNECTION") != "" || os.Getenv("SSH_TTY") != ""
}

// openBrowser opens a URL with the platform's opener.
func openBrowser(url string) error {
	if !strings.HasPrefix(url, "http://") && !strings.HasPrefix(url, "https://") {
		return fmt.Errorf("refusing to open a non-http URL")
	}
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		cmd = exec.Command("open", url)
	case "windows":
		cmd = exec.Command("rundll32", "url.dll,FileProtocolHandler", url)
	default:
		cmd = exec.Command("xdg-open", url)
	}
	return cmd.Start()
}

// pingService checks liveness without needing a session.
func pingService(ctx context.Context, baseURL string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, baseURL+"/v1/health/live", nil)
	if err != nil {
		return err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return fmt.Errorf("could not reach %s", baseURL)
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("the service answered %d", resp.StatusCode)
	}
	return nil
}

// firstLine keeps a diagnostic report to one line per fact.
func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i]
	}
	return s
}
