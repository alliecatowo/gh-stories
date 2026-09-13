// Package tui is the full-screen Stories viewer for the terminal.
//
// Bubble Tea owns state and input; Lip Gloss styles the chrome. The image
// plane is drawn with explicit cursor positioning around the renderer's escape
// sequence, because a line-diffing renderer must not be allowed to erase a
// graphics placement it does not understand. Every previous image is explicitly
// cleared before the next is drawn, and again on exit.
package tui

import (
	"context"
	"fmt"
	"image"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/alliecatowo/gh-stories/internal/cliapi"
	"github.com/alliecatowo/gh-stories/internal/terminal"
)

// Fetcher supplies the data the viewer displays.
type Fetcher interface {
	Media(ctx context.Context, storyID, variant string) (image.Image, MediaMeta, error)
	Reply(ctx context.Context, storyID, body string) error
	React(ctx context.Context, storyID, emoji string) error
	ClearReaction(ctx context.Context, storyID string) error
	Viewers(ctx context.Context, storyID string) (*cliapi.ViewerList, error)
	AckView(ctx context.Context, storyID string) error
}

// MediaMeta describes what was fetched, for the honest external placeholder.
type MediaMeta struct {
	Kind        string
	Width       int
	Height      int
	ByteSize    int64
	DurationMS  int
	Description string
	IsVideo     bool
}

type mode int

const (
	modeViewing mode = iota
	modeReplying
	modeReacting
	modeViewers
	modeHelp
)

// Model is the viewer.
type Model struct {
	groups   []cliapi.AuthorGroup
	group    int
	item     int
	renderer terminal.Renderer
	caps     terminal.Capabilities
	fetch    Fetcher
	// externalURL builds the authenticated service viewer link for Enter.
	externalURL func(storyID string) string
	openURL     func(string) error
	serverNow   func() time.Time

	width, height int
	mode          mode
	replyInput    string
	statusMsg     string
	errMsg        string
	loading       bool
	paused        bool

	current    image.Image
	meta       MediaMeta
	imageID    uint32
	lastDrawn  uint32
	elapsed    time.Duration
	viewers    *cliapi.ViewerList
	pendingRaw string
	quitting   bool
}

// Options configures a viewer.
type Options struct {
	Groups      []cliapi.AuthorGroup
	StartGroup  int
	Renderer    terminal.Renderer
	Caps        terminal.Capabilities
	Fetch       Fetcher
	ExternalURL func(storyID string) string
	OpenURL     func(string) error
	ServerNow   func() time.Time
}

func New(o Options) *Model {
	if o.ServerNow == nil {
		o.ServerNow = time.Now
	}
	m := &Model{
		groups: o.Groups, group: o.StartGroup, renderer: o.Renderer, caps: o.Caps,
		fetch: o.Fetch, externalURL: o.ExternalURL, openURL: o.OpenURL,
		serverNow: o.ServerNow, imageID: 1000,
	}
	if m.group < 0 || m.group >= len(m.groups) {
		m.group = 0
	}
	return m
}

func (m *Model) Init() tea.Cmd {
	return tea.Batch(m.loadCurrent(), tick())
}

type tickMsg time.Time

// tick drives image advance and the progress bar. One second is deliberately
// coarse: this is a Story viewer, not an animation loop.
func tick() tea.Cmd {
	return tea.Tick(250*time.Millisecond, func(t time.Time) tea.Msg { return tickMsg(t) })
}

type mediaMsg struct {
	img  image.Image
	meta MediaMeta
	err  error
	id   string
}

type actionMsg struct {
	status string
	err    error
}

type viewersMsg struct {
	list *cliapi.ViewerList
	err  error
}

func (m *Model) currentItem() *cliapi.StoryItem {
	if m.group < 0 || m.group >= len(m.groups) {
		return nil
	}
	g := m.groups[m.group]
	if m.item < 0 || m.item >= len(g.Items) {
		return nil
	}
	return &g.Items[m.item]
}

func (m *Model) loadCurrent() tea.Cmd {
	it := m.currentItem()
	if it == nil {
		return nil
	}
	m.loading = true
	m.current = nil
	m.elapsed = 0
	id := it.ID
	variant := "image"
	if it.MediaKind == "video" {
		// V1 shows a real poster frame. Inline video playback is not claimed.
		variant = "poster"
	}
	if m.caps.Protocol == terminal.ProtocolExternal {
		// No graphics: do not spend bandwidth fetching pixels we cannot draw.
		return func() tea.Msg {
			return mediaMsg{id: id, meta: metaFor(it), err: errNoGraphics}
		}
	}
	fetch := m.fetch
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		img, meta, err := fetch.Media(ctx, id, variant)
		if err == nil {
			_ = fetch.AckView(ctx, id)
		}
		return mediaMsg{img: img, meta: meta, err: err, id: id}
	}
}

var errNoGraphics = fmt.Errorf("this terminal cannot display images inline")

func metaFor(it *cliapi.StoryItem) MediaMeta {
	meta := MediaMeta{Kind: it.MediaKind, Description: it.AltText,
		IsVideo: it.MediaKind == "video"}
	for _, v := range it.Variants {
		if v.Kind == "image" || v.Kind == "video" || v.Kind == "poster" {
			meta.Width, meta.Height = v.Width, v.Height
			meta.ByteSize, meta.DurationMS = v.ByteSize, v.DurationMS
		}
	}
	return meta
}

// expired reports whether the displayed item has passed its own expiry,
// corrected for client clock skew using the server's time.
func (m *Model) expired(it *cliapi.StoryItem) bool {
	if it == nil || it.ExpiresAt == nil {
		return false
	}
	return !m.serverNow().Before(*it.ExpiresAt)
}

// itemDuration is how long an item shows before advancing.
func (m *Model) itemDuration(it *cliapi.StoryItem) time.Duration {
	if it != nil && it.MediaKind == "video" {
		for _, v := range it.Variants {
			if v.Kind == "video" && v.DurationMS > 0 {
				return time.Duration(v.DurationMS) * time.Millisecond
			}
		}
	}
	return 5 * time.Second
}

func (m *Model) advance() tea.Cmd {
	g := m.groups[m.group]
	if m.item+1 < len(g.Items) {
		m.item++
		return m.loadCurrent()
	}
	if m.group+1 < len(m.groups) {
		m.group++
		m.item = 0
		return m.loadCurrent()
	}
	m.quitting = true
	return tea.Quit
}

func (m *Model) back() tea.Cmd {
	if m.item > 0 {
		m.item--
		return m.loadCurrent()
	}
	if m.group > 0 {
		m.group--
		m.item = 0
		if n := len(m.groups[m.group].Items); n > 0 {
			m.item = n - 1
		}
		return m.loadCurrent()
	}
	return nil
}

func truncate(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	if n <= 1 {
		return "…"
	}
	return string(r[:n-1]) + "…"
}

func relative(t *time.Time, now time.Time) string {
	if t == nil {
		return ""
	}
	d := now.Sub(*t)
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

func pad(s string, n int) string {
	w := len([]rune(s))
	if w >= n {
		return s
	}
	return s + strings.Repeat(" ", n-w)
}
