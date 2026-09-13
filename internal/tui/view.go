package tui

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/alliecatowo/gh-stories/internal/cliapi"
	"github.com/alliecatowo/gh-stories/internal/domain"
	"github.com/alliecatowo/gh-stories/internal/terminal"
)

var (
	styleDim     = lipgloss.NewStyle().Foreground(lipgloss.Color("244"))
	styleAuthor  = lipgloss.NewStyle().Bold(true)
	styleAccent  = lipgloss.NewStyle().Foreground(lipgloss.Color("205"))
	styleErr     = lipgloss.NewStyle().Foreground(lipgloss.Color("203"))
	styleStatus  = lipgloss.NewStyle().Foreground(lipgloss.Color("114"))
	styleSegOn   = lipgloss.NewStyle().Foreground(lipgloss.Color("205"))
	styleSegOff  = lipgloss.NewStyle().Foreground(lipgloss.Color("238"))
	styleReply   = lipgloss.NewStyle().Foreground(lipgloss.Color("252"))
	styleKeyHint = lipgloss.NewStyle().Foreground(lipgloss.Color("240"))
)

// imageTop is the first terminal row the image plane may occupy (1-indexed).
const imageTop = 3

// clearImage removes the previously placed graphic. Doing this explicitly on
// every transition is what stops a stale picture from sitting underneath the
// next one in terminals that composite images above the text plane.
func (m *Model) clearImage() tea.Cmd {
	if m.renderer == nil || m.lastDrawn == 0 {
		return nil
	}
	id := m.lastDrawn
	m.lastDrawn = 0
	renderer := m.renderer
	return func() tea.Msg {
		var b strings.Builder
		_ = renderer.Clear(&b, id)
		if b.Len() > 0 {
			return rawWriteMsg(b.String())
		}
		return nil
	}
}

type rawWriteMsg string

func (m *Model) View() string {
	if m.quitting {
		return ""
	}
	if m.width == 0 || m.height == 0 {
		return "Loading…"
	}
	if len(m.groups) == 0 {
		return m.emptyView()
	}
	it := m.currentItem()
	if it == nil {
		return m.emptyView()
	}
	g := m.groups[m.group]

	var b strings.Builder
	b.WriteString(m.progressBar(len(g.Items), m.item, it))
	b.WriteString("\n")
	b.WriteString(m.headerLine(g, it))
	b.WriteString("\n")

	imageRows := m.height - 6
	if imageRows < 1 {
		imageRows = 1
	}

	switch {
	case m.mode == modeViewers:
		b.WriteString(m.viewersPane(imageRows))
	case m.mode == modeHelp:
		b.WriteString(m.helpPane(imageRows))
	case m.current != nil:
		// Reserve the plane with blank lines; the graphic is placed after the
		// text frame using absolute positioning (see imagePlacement).
		b.WriteString(strings.Repeat("\n", imageRows))
	default:
		b.WriteString(m.placeholderPane(imageRows, it))
	}

	b.WriteString("\n")
	b.WriteString(m.captionLine(it))
	b.WriteString("\n")
	b.WriteString(m.statusLine())
	b.WriteString("\n")
	b.WriteString(m.hintLine(it))

	frame := b.String()
	if m.pendingRaw != "" {
		frame = m.pendingRaw + frame
		m.pendingRaw = ""
	}
	if m.current != nil && m.mode == modeViewing {
		frame += m.imagePlacement(imageRows)
	}
	return frame
}

// imagePlacement emits the graphics escape after the text frame.
//
// The cursor is saved, moved to an absolute position, the image is placed, and
// the cursor is restored, so the escape is zero-width from the text renderer's
// point of view and cannot disturb the layout it just drew.
//
// The escape sequences here are constructed by the renderer from image bytes.
// No untrusted text ever reaches this path — captions and logins go through
// terminal.Sanitize before they are printed as ordinary text.
func (m *Model) imagePlacement(rows int) string {
	if m.renderer == nil || m.current == nil {
		return ""
	}
	box := terminal.Placement{Col: 1, Row: imageTop, WidthCells: m.width, HeightCells: rows}
	cols, fitRows, _, _ := terminal.FitCells(
		m.current.Bounds().Dx(), m.current.Bounds().Dy(), box, m.caps)
	// Centre horizontally, and vertically within the plane.
	col := 1 + max(0, (m.width-cols)/2)
	row := imageTop + max(0, (rows-fitRows)/2)

	var out strings.Builder
	out.WriteString("\x1b7")                   // save cursor
	fmt.Fprintf(&out, "\x1b[%d;%dH", row, col) // absolute position
	m.imageID++
	if err := m.renderer.Render(&out, m.imageID, m.current,
		terminal.Placement{Col: col, Row: row, WidthCells: cols, HeightCells: fitRows},
		m.caps); err != nil {
		return ""
	}
	m.lastDrawn = m.imageID
	out.WriteString("\x1b8") // restore cursor
	return out.String()
}

func (m *Model) emptyView() string {
	return lipgloss.Place(m.width, m.height, lipgloss.Center, lipgloss.Center,
		styleDim.Render("No Stories right now.\n\nPeople you follow will show up here.\n\nq to quit"))
}

// progressBar draws one segment per item in the current author's sequence.
func (m *Model) progressBar(n, active int, it *cliapi.StoryItem) string {
	if n <= 0 {
		return ""
	}
	gap := 1
	total := m.width - 2
	seg := (total - gap*(n-1)) / n
	if seg < 1 {
		seg = 1
	}
	var b strings.Builder
	b.WriteString(" ")
	for i := 0; i < n; i++ {
		switch {
		case i < active:
			b.WriteString(styleSegOn.Render(strings.Repeat("━", seg)))
		case i == active:
			filled := 0
			if d := m.itemDuration(it); d > 0 {
				filled = int(float64(seg) * float64(m.elapsed) / float64(d))
			}
			if filled > seg {
				filled = seg
			}
			b.WriteString(styleSegOn.Render(strings.Repeat("━", filled)))
			b.WriteString(styleSegOff.Render(strings.Repeat("━", seg-filled)))
		default:
			b.WriteString(styleSegOff.Render(strings.Repeat("━", seg)))
		}
		if i < n-1 {
			b.WriteString(strings.Repeat(" ", gap))
		}
	}
	return b.String()
}

// headerLine shows the author, relative time and audience.
//
// Every value interpolated here originates from the server and is therefore
// untrusted: logins and captions are sanitized before they are printed.
func (m *Model) headerLine(g cliapi.AuthorGroup, it *cliapi.StoryItem) string {
	login := terminal.SanitizeTruncate(g.Author.Login, 32)
	when := relative(it.PublishedAt, m.serverNow())

	left := " " + styleAccent.Render("●") + " " + styleAuthor.Render(login)
	if when != "" {
		left += styleDim.Render(" · " + when)
	}
	if it.IsOwner && it.AudienceLabel != "" {
		left += styleDim.Render(" · " + terminal.SanitizeTruncate(it.AudienceLabel, 28))
	}
	if m.paused {
		left += styleDim.Render(" · paused")
	}
	right := styleDim.Render(fmt.Sprintf("%d/%d ", m.group+1, len(m.groups)))

	spacing := m.width - lipgloss.Width(left) - lipgloss.Width(right)
	if spacing < 1 {
		spacing = 1
	}
	return left + strings.Repeat(" ", spacing) + right
}

func (m *Model) captionLine(it *cliapi.StoryItem) string {
	if it.Caption == "" {
		if it.AltText != "" {
			return " " + styleDim.Render(terminal.SanitizeTruncate(it.AltText, m.width-3))
		}
		return ""
	}
	return " " + terminal.SanitizeTruncate(it.Caption, m.width-3)
}

func (m *Model) statusLine() string {
	switch {
	case m.mode == modeReplying:
		prompt := " reply › "
		body := terminal.SanitizeTruncate(m.replyInput, max(1, m.width-len(prompt)-3))
		return styleAccent.Render(prompt) + styleReply.Render(body) + styleAccent.Render("▌")
	case m.mode == modeReacting:
		var parts []string
		for i, e := range domain.Reactions {
			parts = append(parts, fmt.Sprintf("%d %s", i+1, e))
		}
		return " " + styleDim.Render("react: ") + strings.Join(parts, "  ") +
			styleDim.Render("   esc to cancel")
	case m.errMsg != "":
		return " " + styleErr.Render(terminal.SanitizeTruncate(m.errMsg, m.width-3))
	case m.statusMsg != "":
		return " " + styleStatus.Render(terminal.SanitizeTruncate(m.statusMsg, m.width-3))
	case m.loading:
		return " " + styleDim.Render("loading…")
	}
	return ""
}

func (m *Model) hintLine(it *cliapi.StoryItem) string {
	hints := []string{"←/→ move", "space pause"}
	if it != nil && it.IsOwner {
		hints = append(hints, "v viewers")
	} else {
		if it != nil && it.AllowReplies {
			hints = append(hints, "r reply")
		}
		if it != nil && it.AllowReactions {
			hints = append(hints, "e react")
		}
	}
	hints = append(hints, "enter open", "q quit")
	return " " + styleKeyHint.Render(truncate(strings.Join(hints, "  ·  "), max(1, m.width-2)))
}

// placeholderPane is the honest fallback. It never pretends a picture is on
// screen: it says what the media is, offers the description, and explains how
// to see it.
func (m *Model) placeholderPane(rows int, it *cliapi.StoryItem) string {
	meta := m.meta
	if meta.Width == 0 {
		meta = metaFor(it)
	}
	inner := []string{}
	kind := "image"
	if meta.IsVideo {
		kind = "video"
	}
	dims := ""
	if meta.Width > 0 && meta.Height > 0 {
		dims = fmt.Sprintf("%d×%d", meta.Width, meta.Height)
	}
	size := ""
	if meta.ByteSize > 0 {
		size = fmt.Sprintf("%.1f MB", float64(meta.ByteSize)/(1<<20))
	}
	head := "[" + kind + "]"
	for _, extra := range []string{dims, size} {
		if extra != "" {
			head += " · " + extra
		}
	}
	if meta.IsVideo && meta.DurationMS > 0 {
		head += fmt.Sprintf(" · %.1fs", float64(meta.DurationMS)/1000)
	}
	inner = append(inner, head)

	if it.AltText != "" {
		inner = append(inner, "", terminal.SanitizeTruncate(it.AltText, min(60, m.width-8)))
	}
	inner = append(inner, "")
	switch {
	case m.errMsg != "":
		inner = append(inner, "Could not load the media.")
	case m.caps.Protocol == terminal.ProtocolExternal:
		inner = append(inner, "This terminal cannot display images inline.",
			terminal.SanitizeTruncate(m.caps.Reason, min(60, m.width-8)),
			"", "Press Enter to open it in your browser.")
	case meta.IsVideo:
		inner = append(inner, "Press Enter to play it in your browser.")
	default:
		inner = append(inner, "Loading…")
	}

	box := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).BorderForeground(lipgloss.Color("240")).
		Padding(1, 3).Align(lipgloss.Center).Render(strings.Join(inner, "\n"))
	return lipgloss.Place(m.width, rows, lipgloss.Center, lipgloss.Center, box)
}

// viewersPane is shown to the author only.
func (m *Model) viewersPane(rows int) string {
	if m.viewers == nil {
		return lipgloss.Place(m.width, rows, lipgloss.Center, lipgloss.Center,
			styleDim.Render("loading viewers…"))
	}
	lines := []string{styleAuthor.Render(fmt.Sprintf("%d viewer(s)", m.viewers.Total)), ""}
	limit := rows - 4
	for i, v := range m.viewers.Viewers {
		if i >= limit {
			lines = append(lines, styleDim.Render(fmt.Sprintf("…and %d more",
				len(m.viewers.Viewers)-i)))
			break
		}
		line := terminal.SanitizeTruncate(v.User.Login, 24)
		if v.Reaction != "" {
			line += "  " + v.Reaction
		}
		lines = append(lines, line)
	}
	lines = append(lines, "", styleDim.Render("any key to go back"))
	box := lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).
		BorderForeground(lipgloss.Color("240")).Padding(1, 4).Render(strings.Join(lines, "\n"))
	return lipgloss.Place(m.width, rows, lipgloss.Center, lipgloss.Center, box)
}

func (m *Model) helpPane(rows int) string {
	lines := []string{
		styleAuthor.Render("gh stories"), "",
		"←  →      previous / next item",
		"↑  ↓      previous / next person",
		"space     pause and resume",
		"r         reply privately",
		"e         react",
		"v         who viewed (your own Stories)",
		"enter     open in your browser",
		"q         quit", "",
		styleDim.Render("renderer: " + string(m.caps.Protocol) + " — " +
			terminal.SanitizeTruncate(m.caps.Reason, 40)),
		"", styleDim.Render("any key to go back"),
	}
	box := lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).
		BorderForeground(lipgloss.Color("240")).Padding(1, 4).Render(strings.Join(lines, "\n"))
	return lipgloss.Place(m.width, rows, lipgloss.Center, lipgloss.Center, box)
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
