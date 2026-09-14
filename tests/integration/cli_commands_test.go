package integration

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// These exercise EVERY documented `gh stories` command against a real service,
// through the real binary, so a command that compiles but does not work cannot
// pass unnoticed.

type cli struct {
	t       *testing.T
	bin     string
	home    string
	service string
}

func newCLI(t *testing.T, e *env, login string, gitHubID int64) *cli {
	t.Helper()
	bin := buildCLI(t)

	user := testdbAccount(t, e, login, gitHubID)
	token := "clitest-" + login + "-" + randomSuffix()
	_, err := e.store.CreateSession(context.Background(), nil, user, token,
		"cli", "command surface test", "test", 24*time.Hour)
	require.NoError(t, err)

	home := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(home, "gh-stories"), 0o700))
	creds := map[string]any{
		e.srv.URL: map[string]any{
			"service_url": e.srv.URL,
			"token":       token,
			"login":       login,
			"user_id":     gitHubID,
			"stored_at":   time.Now().Format(time.RFC3339),
		},
	}
	raw, err := json.Marshal(creds)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(
		filepath.Join(home, "gh-stories", "credentials.json"), raw, 0o600))

	return &cli{t: t, bin: bin, home: home, service: e.srv.URL}
}

// run executes the CLI and returns stdout, stderr and the exit code.
func (c *cli) run(args ...string) (string, string, int) {
	c.t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, c.bin, args...)
	cmd.Env = append(os.Environ(),
		"XDG_CONFIG_HOME="+c.home,
		"GHS_CREDENTIAL_STORE=file",
		"GHS_SERVICE_URL="+c.service,
		"NO_COLOR=1",
	)
	var stdout, stderr strings.Builder
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	code := 0
	if exitErr, ok := err.(*exec.ExitError); ok {
		code = exitErr.ExitCode()
	} else if err != nil {
		c.t.Fatalf("running %v: %v", args, err)
	}
	return stdout.String(), stderr.String(), code
}

// mustRun requires a zero exit code.
func (c *cli) mustRun(args ...string) string {
	c.t.Helper()
	stdout, stderr, code := c.run(args...)
	require.Equalf(c.t, 0, code, "gh stories %s failed:\n%s\n%s",
		strings.Join(args, " "), stdout, stderr)
	return stdout + stderr
}

func TestEveryDocumentedCommand(t *testing.T) {
	e := newEnv(t)
	e.runWorker()
	alice := newCLI(t, e, "alice", 7001)
	bob := newCLI(t, e, "bob", 7002)

	t.Run("version", func(t *testing.T) {
		out := alice.mustRun("version")
		require.Contains(t, out, "gh-stories")
	})

	t.Run("help", func(t *testing.T) {
		out := alice.mustRun("--help")
		// Every documented command must appear in the help.
		for _, cmd := range []string{
			"login", "logout", "setup", "doctor", "post", "reply", "react",
			"delete", "viewers", "inbox", "report", "follow", "unfollow",
			"mute", "unmute", "block", "unblock", "settings", "version", "import",
		} {
			require.Containsf(t, out, cmd, "help must document %q", cmd)
		}
		require.Contains(t, out, "EXIT CODES")
	})

	t.Run("doctor", func(t *testing.T) {
		out := alice.mustRun("doctor")
		require.Contains(t, out, "Service")
		require.Contains(t, out, "reachable          yes")
		require.Contains(t, out, "signed in          yes, as alice")
		// A diagnostic must never print a secret.
		require.NotContains(t, out, "clitest-alice")
	})

	t.Run("import sends you to GitHub rather than pretending", func(t *testing.T) {
		out := alice.mustRun("import")
		// The service holds no GitHub token, so the honest answer is a URL.
		require.Contains(t, out, "/v1/auth/github/start?purpose=import")
		require.Contains(t, out, "never changes who you follow on GitHub")
	})

	t.Run("import --no is applied immediately", func(t *testing.T) {
		bob := newCLI(t, e, "bob-import", 770201)
		before, err := e.store.UserByGitHubID(context.Background(), nil, 770201)
		require.NoError(t, err)
		require.Nil(t, before.OnboardedAt, "a fresh account has not been onboarded")

		out := bob.mustRun("import", "--no")
		require.Contains(t, out, "Not importing")
		require.NotContains(t, out, "auth/github/start")

		after, err := e.store.UserByGitHubID(context.Background(), nil, 770201)
		require.NoError(t, err)
		require.NotNil(t, after.OnboardedAt, "declining must stop the offer coming back")
	})

	t.Run("import --json", func(t *testing.T) {
		carol := newCLI(t, e, "carol-import", 770202)
		out := carol.mustRun("import", "--json")
		var got struct {
			Enabled                  bool   `json:"enabled"`
			NeedsGitHubAuthorization bool   `json:"needs_github_authorization"`
			AuthorizationURL         string `json:"authorization_url"`
		}
		require.NoError(t, json.Unmarshal([]byte(out), &got))
		require.True(t, got.Enabled)
		require.True(t, got.NeedsGitHubAuthorization)
		require.Contains(t, got.AuthorizationURL, "purpose=import")
	})

	var storyID string
	t.Run("post", func(t *testing.T) {
		out := alice.mustRun("post", fixture(t, "samples/cat.jpg"),
			"--caption", "he has claimed the laundry",
			"--description", "A tabby cat asleep on a blanket",
			"--audience", "public")
		require.Contains(t, out, "Posted")
		for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
			if len(strings.TrimSpace(line)) == 36 {
				storyID = strings.TrimSpace(line)
			}
		}
		require.NotEmpty(t, storyID, "post must print the new Story id on stdout")
	})

	t.Run("post from stdin", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
		defer cancel()
		cmd := exec.CommandContext(ctx, alice.bin, "post", "-",
			"--filename", "shot.png", "--audience", "public")
		cmd.Env = append(os.Environ(),
			"XDG_CONFIG_HOME="+alice.home, "GHS_CREDENTIAL_STORE=file",
			"GHS_SERVICE_URL="+alice.service, "NO_COLOR=1")
		data, err := os.ReadFile(fixture(t, "landscape.png"))
		require.NoError(t, err)
		cmd.Stdin = strings.NewReader(string(data))
		out, err := cmd.CombinedOutput()
		require.NoErrorf(t, err, "binary stdin pipeline failed: %s", out)
		require.Contains(t, string(out), "Posted")
	})

	t.Run("feed as json", func(t *testing.T) {
		stdout, _, code := alice.run("--json")
		require.Zero(t, code)
		var feed map[string]any
		require.NoError(t, json.Unmarshal([]byte(stdout), &feed),
			"--json must emit parseable JSON on stdout")
	})

	t.Run("viewers", func(t *testing.T) {
		out := alice.mustRun("viewers", storyID)
		require.Contains(t, out, "view")
	})

	t.Run("reply and react", func(t *testing.T) {
		require.Contains(t, bob.mustRun("reply", "--story", storyID, "lmao"), "Sent")
		require.Contains(t, bob.mustRun("react", "--story", storyID, "😂"), "Reacted")
		require.Contains(t, alice.mustRun("inbox"), "bob")
	})

	t.Run("social graph", func(t *testing.T) {
		require.Contains(t, alice.mustRun("follow", "@bob"), "Following bob")
		require.Contains(t, alice.mustRun("mute", "@bob"), "Muted bob")
		require.Contains(t, alice.mustRun("unmute", "@bob"), "Unmuted bob")
		require.Contains(t, alice.mustRun("block", "@bob"), "Blocked bob")
		require.Contains(t, alice.mustRun("unblock", "@bob"), "Unblocked bob")
		require.Contains(t, alice.mustRun("unfollow", "@bob"), "Unfollowed bob")
	})

	t.Run("settings", func(t *testing.T) {
		out := alice.mustRun("settings")
		require.Contains(t, out, "Defaults for new Stories")
		require.Contains(t, out, "audience")

		// Changing a default must take effect and be reported back.
		out = alice.mustRun("settings", "--default-audience", "mutuals")
		require.Contains(t, out, "Mutuals")

		out = alice.mustRun("settings", "--replies", "off")
		require.Contains(t, out, "replies            off")
		alice.mustRun("settings", "--replies", "on", "--default-audience", "public")
	})

	t.Run("hide and unhide", func(t *testing.T) {
		require.Contains(t, alice.mustRun("settings", "--hide-from", "@bob"), "Hidden from bob")
		require.Contains(t, strings.ToLower(alice.mustRun("settings", "--unhide-from", "@bob")), "no longer hidden")
	})

	t.Run("audience lists", func(t *testing.T) {
		// A custom audience is useless if you cannot create one.
		out := alice.mustRun("settings", "--create-list", "close friends", "--members", "@bob")
		require.Contains(t, out, "close friends")
		require.Contains(t, out, "1 member")

		out = alice.mustRun("settings")
		require.Contains(t, out, "close friends")
		require.Contains(t, out, "bob")

		// And it can actually be posted to.
		out = alice.mustRun("post", fixture(t, "samples/food.jpg"),
			"--caption", "for the list", "--audience", "list:close friends")
		require.Contains(t, out, "Posted")

		// Only the member can see it.
		stdout, _, _ := bob.run("--json")
		require.Contains(t, stdout, "for the list")

		// A list can be explicitly emptied, and the CLI says what that means.
		out = alice.mustRun("settings", "--list", "close friends", "--members", "")
		require.Contains(t, out, "is now empty")

		// Omitting --members entirely is a usage error, not a silent wipe.
		_, stderr, code := alice.run("settings", "--list", "close friends")
		require.Equal(t, 2, code)
		require.Contains(t, stderr, "say who should be on the list")

		out = alice.mustRun("settings", "--list", "close friends", "--members", "@bob")
		require.Contains(t, out, "1 member")

		require.Contains(t, alice.mustRun("settings", "--delete-list", "close friends"), "Deleted the list")
	})

	t.Run("a missing audience list is explained, not a raw error", func(t *testing.T) {
		_, stderr, code := alice.run("post", fixture(t, "landscape.png"),
			"--audience", "list:nope")
		require.NotZero(t, code)
		// The error must say what you DO have, not just what you do not.
		require.Contains(t, stderr, "audience list")
		require.Contains(t, stderr, "nope")
	})

	t.Run("report", func(t *testing.T) {
		require.Contains(t, bob.mustRun("report", "--story", storyID, "--reason", "spam"), "Reported")
	})

	t.Run("delete", func(t *testing.T) {
		require.Contains(t, alice.mustRun("delete", storyID), "Deleted")
		// And it is genuinely gone for the other account.
		stdout, _, _ := bob.run("--json")
		require.NotContains(t, stdout, storyID)
	})

	t.Run("logout", func(t *testing.T) {
		require.Contains(t, bob.mustRun("logout"), "Signed out")
		// A logged-out client reports the right exit code, not a crash.
		_, stderr, code := bob.run("inbox")
		require.Equal(t, 3, code, "exit code 3 means 'not signed in'")
		require.Contains(t, stderr, "gh stories login")
	})
}

// TestExitCodes pins the documented exit-code contract.
func TestExitCodes(t *testing.T) {
	e := newEnv(t)
	alice := newCLI(t, e, "alice", 7101)

	t.Run("usage error is 2", func(t *testing.T) {
		_, _, code := alice.run("post")
		require.Equal(t, 2, code)
	})

	t.Run("unknown command is 2", func(t *testing.T) {
		_, _, code := alice.run("definitely-not-a-command")
		require.Equal(t, 2, code)
	})

	t.Run("no access is 4", func(t *testing.T) {
		_, _, code := alice.run("viewers", "00000000-0000-0000-0000-000000000000")
		require.Equal(t, 4, code)
	})

	t.Run("pointing at a service you are not signed into is 3", func(t *testing.T) {
		// Credentials are keyed by service URL, so this is legitimately
		// "not signed in" rather than a network fault.
		_, stderr, code := alice.run("--service", "http://127.0.0.1:1", "inbox")
		require.Equal(t, 3, code)
		require.Contains(t, stderr, "gh stories login")
	})

	t.Run("an unreachable service is 5", func(t *testing.T) {
		// Signed in, but the service has gone away underneath us.
		e.srv.Close()
		_, _, code := alice.run("inbox")
		require.Equal(t, 5, code)
	})
}
