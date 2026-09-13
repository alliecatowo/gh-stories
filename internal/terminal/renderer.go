package terminal

import (
	"image"
	"io"
)

// Placement describes where to draw an image and how large a box it must
// fit inside, in terminal cell coordinates. Col/Row are 0-based positions
// relative to the current cursor origin used by the caller's layout;
// WidthCells/HeightCells bound the box the image is scaled into.
type Placement struct {
	Col, Row                int
	WidthCells, HeightCells int
}

// Renderer draws images into a terminal using one specific inline-image
// protocol. Implementations never pretend: Supported reports honestly
// whether the detected Capabilities actually allow this renderer to draw
// anything, and Render/Clear only emit protocol bytes appropriate to those
// capabilities (including tmux passthrough wrapping, when verified).
type Renderer interface {
	// Name reports the protocol this renderer implements.
	Name() Protocol
	// Render draws img at placement p, resized to fit p while preserving
	// aspect ratio. id is a caller-chosen, non-zero image identity that a
	// later Clear call can use to remove exactly this image.
	Render(w io.Writer, id uint32, img image.Image, p Placement, caps Capabilities) error
	// Clear removes a previously rendered image identified by id, or every
	// image this renderer knows about when id == 0.
	Clear(w io.Writer, id uint32) error
	// Supported reports whether this renderer can actually draw under caps
	// (e.g. false for kitty inside tmux without verified passthrough).
	Supported(caps Capabilities) bool
}

// New returns the Renderer implementation matching caps.Protocol. Any
// protocol value other than ProtocolKitty/ProtocolITerm2 (including an
// unrecognised one) yields External, which is always safe to use: it never
// emits a byte a terminal could misinterpret.
func New(caps Capabilities) Renderer {
	switch caps.Protocol {
	case ProtocolKitty:
		return &Kitty{caps: caps}
	case ProtocolITerm2:
		return &ITerm2{caps: caps, placements: make(map[uint32]Placement)}
	default:
		return &External{caps: caps}
	}
}
