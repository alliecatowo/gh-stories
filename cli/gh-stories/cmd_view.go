package main

import (
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"image"
	_ "image/gif"
	_ "image/jpeg"
	_ "image/png"
	"io"
	"os"
	"strings"
	"time"

	_ "golang.org/x/image/webp"

	"github.com/alliecatowo/gh-stories/internal/cliapi"
	"github.com/alliecatowo/gh-stories/internal/terminal"
	"github.com/alliecatowo/gh-stories/internal/tui"
)

// cmdView opens the viewer, or prints the feed when output is not a terminal.
func cmdView(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("view", flag.ContinueOnError)
	service, renderer, asJSON := globalFlags(fs)
	var who string
	rest := []string{}
	for _, a := range args {
		if strings.HasPrefix(a, "@") {
			who = strings.TrimPrefix(a, "@")
			continue
		}
		rest = append(rest, a)
	}
	if err := fs.Parse(rest); err != nil {
		return usagef("gh stories [@login] [--renderer M] [--json]")
	}
	sess, err := openSession(*service, *asJSON, *renderer)
	if err != nil {
		return err
	}

	var groups []cliapi.AuthorGroup
	if who != "" {
		group, err := sess.Client.UserStories(ctx, who)
		if err != nil {
			return err
		}
		groups = []cliapi.AuthorGroup{*group}
	} else {
		feed, err := sess.Client.Feed(ctx, "", 25, false)
		if err != nil {
			return err
		}
		if feed.Me != nil && len(feed.Me.Items) > 0 {
			groups = append(groups, *feed.Me)
		}
		groups = append(groups, feed.Groups...)
	}

	// Noninteractive: never launch a full-screen app, never emit graphics,
	// never hang. Print something useful and exit.
	if *asJSON || !interactive() {
		return printFeed(groups, *asJSON)
	}
	if len(groups) == 0 {
		status("No Stories right now. People you follow will show up here.")
		return nil
	}

	caps := detectTerminal(ctx, terminal.Protocol(*renderer))
	model := tui.New(tui.Options{
		Groups:   groups,
		Renderer: terminal.New(caps),
		Caps:     caps,
		Fetch:    &fetcher{client: sess.Client},
		ExternalURL: func(storyID string) string {
			return sess.ServiceURL + "/s/" + storyID
		},
		OpenURL: openBrowser,
	})
	return tui.Run(ctx, model)
}

// printFeed is the noninteractive path.
func printFeed(groups []cliapi.AuthorGroup, asJSON bool) error {
	if asJSON {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(map[string]any{"groups": groups})
	}
	if len(groups) == 0 {
		out("No Stories right now.")
		return nil
	}
	for _, g := range groups {
		marker := " "
		if g.HasUnseen {
			marker = "●"
		}
		suffix := ""
		if g.Muted {
			suffix = " (muted)"
		}
		out("%s %-20s %d item(s)%s", marker,
			terminal.SanitizeTruncate(g.Author.Login, 20), len(g.Items), suffix)
		for _, it := range g.Items {
			when := ""
			if it.PublishedAt != nil {
				when = relativeAge(*it.PublishedAt)
			}
			caption := terminal.SanitizeTruncate(it.Caption, 48)
			out("    %s  %-6s %-8s %s", it.ID, when, it.MediaKind, caption)
		}
	}
	return nil
}

func relativeAge(t time.Time) string {
	d := time.Since(t)
	switch {
	case d < time.Minute:
		return "now"
	case d < time.Hour:
		return fmt.Sprintf("%dm", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh", int(d.Hours()))
	default:
		return fmt.Sprintf("%dd", int(d.Hours()/24))
	}
}

// fetcher adapts the API client to what the viewer needs.
type fetcher struct{ client *cliapi.Client }

func (f *fetcher) Media(ctx context.Context, storyID, variant string) (image.Image, tui.MediaMeta, error) {
	body, _, err := f.client.GetMedia(ctx, storyID, variant, "")
	if err != nil {
		return nil, tui.MediaMeta{}, err
	}
	defer body.Close()
	// Bound the read: a hostile or broken service must not be able to make the
	// CLI allocate without limit.
	const maxMedia = 64 << 20
	raw, err := io.ReadAll(io.LimitReader(body, maxMedia))
	if err != nil {
		return nil, tui.MediaMeta{}, err
	}
	img, _, err := image.Decode(bytes.NewReader(raw))
	if err != nil {
		return nil, tui.MediaMeta{}, fmt.Errorf("could not decode the media")
	}
	return img, tui.MediaMeta{
		Kind: variant, ByteSize: int64(len(raw)),
		Width: img.Bounds().Dx(), Height: img.Bounds().Dy(),
		IsVideo: variant == "poster",
	}, nil
}

func (f *fetcher) Reply(ctx context.Context, storyID, body string) error {
	_, err := f.client.Reply(ctx, storyID, body, "")
	return err
}
func (f *fetcher) React(ctx context.Context, storyID, emoji string) error {
	_, err := f.client.SetReaction(ctx, storyID, emoji, "")
	return err
}
func (f *fetcher) ClearReaction(ctx context.Context, storyID string) error {
	return f.client.ClearReaction(ctx, storyID)
}
func (f *fetcher) Viewers(ctx context.Context, storyID string) (*cliapi.ViewerList, error) {
	return f.client.Viewers(ctx, storyID)
}
func (f *fetcher) AckView(ctx context.Context, storyID string) error {
	return f.client.AckView(ctx, storyID)
}

var _ tui.Fetcher = (*fetcher)(nil)
