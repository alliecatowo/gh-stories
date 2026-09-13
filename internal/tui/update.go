package tui

import (
	"context"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/alliecatowo/gh-stories/internal/domain"
)

func (m *Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		// A resize invalidates the placed image: redraw it at the new geometry.
		return m, tea.Batch(m.clearImage(), m.loadCurrent())

	case tea.KeyMsg:
		return m.handleKey(msg)

	case mediaMsg:
		m.loading = false
		if it := m.currentItem(); it == nil || it.ID != msg.id {
			return m, nil // a stale fetch for an item we already left
		}
		m.meta = msg.meta
		if msg.err != nil {
			m.current = nil
			if msg.err != errNoGraphics {
				m.errMsg = msg.err.Error()
			}
			return m, nil
		}
		m.current = msg.img
		m.errMsg = ""
		return m, nil

	case actionMsg:
		if msg.err != nil {
			m.errMsg = msg.err.Error()
		} else {
			m.statusMsg = msg.status
		}
		return m, nil

	case viewersMsg:
		if msg.err != nil {
			m.errMsg = msg.err.Error()
			m.mode = modeViewing
			return m, nil
		}
		m.viewers = msg.list
		m.mode = modeViewers
		return m, nil

	case rawWriteMsg:
		// A clear-image escape produced outside the text frame. It is buffered
		// and emitted once at the top of the next View, so it is never
		// interleaved with a half-drawn frame.
		m.pendingRaw += string(msg)
		return m, nil

	case tickMsg:
		return m.handleTick()
	}
	return m, nil
}

func (m *Model) handleTick() (tea.Model, tea.Cmd) {
	it := m.currentItem()

	// Expiry is enforced client side too, including while paused. The viewer
	// never becomes an offline archive of media whose time is up.
	if m.expired(it) {
		m.current = nil
		m.statusMsg = "This Story expired."
		return m, tea.Batch(m.clearImage(), m.advance(), tick())
	}

	// Pause while replying, picking a reaction, or reading the viewer list.
	if m.paused || m.mode != modeViewing || m.loading {
		return m, tick()
	}
	m.elapsed += 250 * time.Millisecond
	if m.elapsed >= m.itemDuration(it) {
		return m, tea.Batch(m.clearImage(), m.advance(), tick())
	}
	return m, tick()
}

func (m *Model) handleKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	// The reply editor consumes most keys while it is open.
	if m.mode == modeReplying {
		switch msg.Type {
		case tea.KeyEsc:
			m.mode = modeViewing
			m.replyInput = ""
			return m, nil
		case tea.KeyEnter:
			body := strings.TrimSpace(m.replyInput)
			m.replyInput = ""
			m.mode = modeViewing
			if body == "" {
				return m, nil
			}
			it := m.currentItem()
			if it == nil {
				return m, nil
			}
			// Replying from the viewer always targets the item on screen.
			id := body
			storyID := it.ID
			fetch := m.fetch
			return m, func() tea.Msg {
				ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
				defer cancel()
				if err := fetch.Reply(ctx, storyID, id); err != nil {
					return actionMsg{err: err}
				}
				return actionMsg{status: "Reply sent."}
			}
		case tea.KeyBackspace:
			if r := []rune(m.replyInput); len(r) > 0 {
				m.replyInput = string(r[:len(r)-1])
			}
			return m, nil
		case tea.KeyRunes, tea.KeySpace:
			if len([]rune(m.replyInput)) < 500 {
				m.replyInput += string(msg.Runes)
				if msg.Type == tea.KeySpace {
					m.replyInput += " "
				}
			}
			return m, nil
		}
		return m, nil
	}

	if m.mode == modeReacting {
		if msg.Type == tea.KeyEsc {
			m.mode = modeViewing
			return m, nil
		}
		if msg.Type == tea.KeyRunes && len(msg.Runes) == 1 {
			idx := int(msg.Runes[0] - '1')
			if idx >= 0 && idx < len(domain.Reactions) {
				emoji := domain.Reactions[idx]
				it := m.currentItem()
				m.mode = modeViewing
				if it == nil {
					return m, nil
				}
				storyID, fetch := it.ID, m.fetch
				return m, func() tea.Msg {
					ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
					defer cancel()
					if err := fetch.React(ctx, storyID, emoji); err != nil {
						return actionMsg{err: err}
					}
					return actionMsg{status: "Reacted " + emoji}
				}
			}
		}
		return m, nil
	}

	if m.mode == modeViewers || m.mode == modeHelp {
		m.mode = modeViewing
		return m, nil
	}

	switch msg.String() {
	case "q", "esc", "ctrl+c":
		m.quitting = true
		return m, tea.Sequence(m.clearImage(), tea.Quit)
	case "right", "n", "l":
		return m, tea.Batch(m.clearImage(), m.advance())
	case "left", "p", "h":
		return m, tea.Batch(m.clearImage(), m.back())
	case "down", "j":
		// Skip to the next author rather than the next item.
		if m.group+1 < len(m.groups) {
			m.group++
			m.item = 0
			return m, tea.Batch(m.clearImage(), m.loadCurrent())
		}
		return m, nil
	case "up", "k":
		if m.group > 0 {
			m.group--
			m.item = 0
			return m, tea.Batch(m.clearImage(), m.loadCurrent())
		}
		return m, nil
	case " ":
		m.paused = !m.paused
		return m, nil
	case "r":
		if it := m.currentItem(); it != nil && it.AllowReplies && !it.IsOwner {
			m.mode = modeReplying
			m.statusMsg = ""
		} else if it != nil && it.IsOwner {
			m.statusMsg = "You cannot reply to your own Story."
		} else {
			m.statusMsg = "Replies are turned off for this Story."
		}
		return m, nil
	case "e":
		if it := m.currentItem(); it != nil && it.AllowReactions && !it.IsOwner {
			m.mode = modeReacting
		} else {
			m.statusMsg = "Reactions are turned off for this Story."
		}
		return m, nil
	case "v":
		it := m.currentItem()
		if it == nil || !it.IsOwner {
			m.statusMsg = "Only the author can see who viewed a Story."
			return m, nil
		}
		storyID, fetch := it.ID, m.fetch
		return m, func() tea.Msg {
			ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
			defer cancel()
			list, err := fetch.Viewers(ctx, storyID)
			return viewersMsg{list: list, err: err}
		}
	case "enter":
		it := m.currentItem()
		if it == nil || m.externalURL == nil {
			return m, nil
		}
		url := m.externalURL(it.ID)
		if m.caps.OverSSH || m.openURL == nil {
			// Over SSH the remote host has no browser to open; offer the URL.
			m.statusMsg = "Open on your machine: " + url
			return m, nil
		}
		if err := m.openURL(url); err != nil {
			m.statusMsg = "Open this URL: " + url
		} else {
			m.statusMsg = "Opened in your browser."
		}
		return m, nil
	case "?":
		m.mode = modeHelp
		return m, nil
	}
	return m, nil
}
