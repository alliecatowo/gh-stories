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
	"os"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/alliecatowo/gh-stories/internal/cliapi"
	"github.com/alliecatowo/gh-stories/internal/terminal"
	"github.com/alliecatowo/gh-stories/internal/vidframes"
)

// VideoMode selects how video items are shown.
type VideoMode string

const (
	// VideoAuto plays video inline when the terminal can actually animate and
	// ffmpeg is available, and shows the poster frame otherwise.
	VideoAuto VideoMode = "auto"
	// VideoInline asks for inline playback and reports why if it is refused.
	VideoInline VideoMode = "inline"
	// VideoPoster always shows the poster frame.
	VideoPoster VideoMode = "poster"
)

// Fetcher supplies the data the viewer displays.
type Fetcher interface {
	Media(ctx context.Context, storyID, variant string) (image.Image, MediaMeta, error)
	// MediaFile downloads a variant to a local file, for formats that must be
	// decoded from a container (inline video). The returned cleanup removes it.
	MediaFile(ctx context.Context, storyID, variant string) (path string, cleanup func(), err error)
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

// imageSignature identifies a placement: the same picture at the same spot
// does not need to be sent again.
type imageSignature struct {
	img        image.Image
	col, row   int
	cols, rows int
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
	animator terminal.Animator
	video    VideoMode
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

	current image.Image
	meta    MediaMeta
	imageID uint32
	// placedID is the graphics id currently on screen, and placedSig says what
	// it depicts and where, so an unchanged frame never re-transmits it.
	placedID     uint32
	placedSig    imageSignature
	pendingClear []uint32

	// Inline video state.
	frames        []terminal.Frame
	framesCleanup func()
	framesFor     string
	animation     *terminal.Animation
	animatedFor   string
	videoNote     string
	elapsed       time.Duration
	viewers       *cliapi.ViewerList
	pendingRaw    string
	quitting      bool
}

// Options configures a viewer.
type Options struct {
	Groups      []cliapi.AuthorGroup
	StartGroup  int
	Renderer    terminal.Renderer
	Caps        terminal.Capabilities
	Fetch       Fetcher
	Video       VideoMode
	ExternalURL func(storyID string) string
	OpenURL     func(string) error
	ServerNow   func() time.Time
}

func New(o Options) *Model {
	if o.ServerNow == nil {
		o.ServerNow = time.Now
	}
	if o.Video == "" {
		o.Video = VideoAuto
	}
	m := &Model{
		groups: o.Groups, group: o.StartGroup, renderer: o.Renderer, caps: o.Caps,
		fetch: o.Fetch, externalURL: o.ExternalURL, openURL: o.OpenURL,
		serverNow: o.ServerNow, imageID: 1000, video: o.Video,
	}
	// Only attach an animator if this terminal can genuinely animate. A nil
	// animator is what makes every video fall back to its poster frame.
	if o.Video != VideoPoster {
		m.animator = terminal.AnimatorFor(o.Caps)
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
		variant = "poster"
		// Inline playback when this terminal can really animate; otherwise the
		// poster frame, which is always fetched as well so there is something
		// to show immediately and something to fall back to.
		if m.animator != nil {
			return tea.Batch(m.loadPoster(id), m.loadFrames(id))
		}
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

// framesMsg carries a decoded video ready for inline playback.
type framesMsg struct {
	storyID string
	frames  []terminal.Frame
	cleanup func()
	err     error
}

// loadPoster fetches just the still, so something appears immediately.
func (m *Model) loadPoster(storyID string) tea.Cmd {
	fetch := m.fetch
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		img, meta, err := fetch.Media(ctx, storyID, "poster")
		if err == nil {
			_ = fetch.AckView(ctx, storyID)
		}
		return mediaMsg{img: img, meta: meta, err: err, id: storyID}
	}
}

// loadFrames downloads the canonical video and decodes it into frames.
//
// Best effort by design: any failure leaves the poster frame on screen, which
// is what the terminal would have shown anyway.
func (m *Model) loadFrames(storyID string) tea.Cmd {
	fetch := m.fetch
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
		defer cancel()
		path, cleanupFile, err := fetch.MediaFile(ctx, storyID, "video")
		if err != nil {
			return framesMsg{storyID: storyID, err: err}
		}
		defer cleanupFile()
		frames, cleanupFrames, err := vidframes.Decode(ctx, path, vidframes.Options{})
		if err != nil {
			return framesMsg{storyID: storyID, err: err}
		}
		return framesMsg{storyID: storyID, frames: frames, cleanup: cleanupFrames}
	}
}

// stopAnimation halts inline playback and releases its frame files.
func (m *Model) stopAnimation() {
	if m.animation == nil {
		return
	}
	var b strings.Builder
	_ = m.animation.Stop(&b, m.caps)
	m.pendingRaw += b.String()
	m.animation = nil
	m.animatedFor = ""
}

// firstLine keeps a diagnostic to one line so it fits the status bar.
func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i]
	}
	return s
}

// animationGeometry computes where the video should sit, in cells.
func (m *Model) animationGeometry() (terminal.Placement, bool) {
	if len(m.frames) == 0 || m.frames[0].Image == nil || m.width == 0 || m.height == 0 {
		return terminal.Placement{}, false
	}
	rows := m.height - 6
	if rows < 1 {
		rows = 1
	}
	b := m.frames[0].Image.Bounds()
	box := terminal.Placement{Col: 1, Row: imageTop, WidthCells: m.width, HeightCells: rows}
	cols, fitRows, _, _ := terminal.FitCells(b.Dx(), b.Dy(), box, m.caps)
	col := 1 + max(0, (m.width-cols)/2)
	row := imageTop + max(0, (rows-fitRows)/2)
	return terminal.Placement{Col: col, Row: row, WidthCells: cols, HeightCells: fitRows}, true
}

// startAnimation begins inline playback.
//
// It writes to the terminal directly rather than through the view string: the
// animation is a run of control escapes that must arrive in order, and a
// line-diffing text renderer is not a reliable transport for them. The still
// image placed while the video was decoding is removed first, or it would sit
// on top of the animation and make a moving picture look static.
func (m *Model) startAnimation() tea.Cmd {
	it := m.currentItem()
	if it == nil || m.animator == nil {
		return nil
	}
	place, ok := m.animationGeometry()
	if !ok {
		return nil
	}
	frames := m.frames
	animator := m.animator
	caps := m.caps
	renderer := m.renderer
	stale := m.placedID
	m.placedID = 0
	m.placedSig = imageSignature{}
	storyID := it.ID

	return func() tea.Msg {
		var buf strings.Builder
		if stale != 0 && renderer != nil {
			_ = renderer.Clear(&buf, stale)
		}
		buf.WriteString("\x1b7")
		fmt.Fprintf(&buf, "\x1b[%d;%dH", place.Row, place.Col)
		anim, err := animator.Animate(&buf, frames, place, caps)
		buf.WriteString("\x1b8")
		if err != nil {
			return animationMsg{storyID: storyID, err: err}
		}
		if _, werr := os.Stdout.WriteString(buf.String()); werr != nil {
			return animationMsg{storyID: storyID, err: werr}
		}
		return animationMsg{storyID: storyID, anim: &anim}
	}
}

// animationMsg reports the outcome of starting inline playback.
type animationMsg struct {
	storyID string
	anim    *terminal.Animation
	err     error
}
