// Drives the REAL internal/terminal animation code against a real kitty and
// reports whatever the terminal says back, so protocol failures are visible
// rather than suppressed.
package main

import (
	"fmt"
	"image"
	"image/color"
	"os"
	"time"

	"golang.org/x/term"

	"github.com/alliecatowo/gh-stories/internal/terminal"
)

func main() {
	log, _ := os.Create("/tmp/animreal.log")
	defer log.Close()

	old, err := term.MakeRaw(int(os.Stdin.Fd()))
	if err == nil {
		defer term.Restore(int(os.Stdin.Fd()), old)
	}
	replies := make(chan string, 64)
	go func() {
		buf := make([]byte, 4096)
		for {
			n, err := os.Stdin.Read(buf)
			if n > 0 {
				replies <- string(buf[:n])
			}
			if err != nil {
				return
			}
		}
	}()

	caps := terminal.Capabilities{
		Protocol: terminal.ProtocolKitty, ColsCells: 92, RowsCells: 32,
		CellWidthPx: 10, CellHeightPx: 20,
	}
	a := terminal.AnimatorFor(caps)
	if a == nil {
		fmt.Fprintln(log, "no animator for these capabilities")
		return
	}

	frames := make([]terminal.Frame, 0, 12)
	for i := 0; i < 12; i++ {
		img := image.NewRGBA(image.Rect(0, 0, 300, 300))
		c := color.RGBA{uint8(20 + i*18), uint8(200 - i*12), uint8(120 + i*8), 255}
		for y := 0; y < 300; y++ {
			for x := 0; x < 300; x++ {
				img.SetRGBA(x, y, c)
			}
		}
		frames = append(frames, terminal.Frame{Image: img, GapMS: 300})
	}

	os.Stdout.WriteString("\x1b[2J\x1b[H")
	anim, err := a.Animate(os.Stdout, frames,
		terminal.Placement{Col: 2, Row: 3, WidthCells: 40, HeightCells: 20}, caps)
	if err != nil {
		fmt.Fprintln(log, "Animate returned error:", err)
		return
	}
	fmt.Fprintln(log, "Animate returned OK, image number", anim.Number)

	deadline := time.After(3 * time.Second)
	for {
		select {
		case r := <-replies:
			fmt.Fprintf(log, "TERMINAL SAID %q\n", r)
		case <-deadline:
			fmt.Fprintln(log, "done collecting replies")
			time.Sleep(40 * time.Second)
			return
		}
	}
}
