package terminal

// FitCells computes how large an image should be drawn — both in terminal
// cells and in pixels — to fit inside box while preserving its aspect
// ratio. cols/rows are clamped to box.WidthCells/box.HeightCells; pxW/pxH
// are the corresponding pixel dimensions the image should be resized to
// before transmission.
//
// It never returns a zero or negative dimension. Degenerate inputs (a
// zero-sized image, an empty or negative box) are clamped to at least 1x1,
// so callers never need to special-case "there is nothing to draw" — a 3x2
// box always yields a valid, if tiny, placement.
//
// When caps has no known cell pixel geometry (CellWidthPx/CellHeightPx are
// 0 — no TTY, a failed probe, or a forced --renderer override that skipped
// probing), FitCells assumes a 1:2 cell width:height ratio, matching most
// monospace terminal fonts, so aspect-ratio math still produces a sane
// result instead of treating cells as square. In that case pxW/pxH are a
// synthetic target derived from that assumption, not a measurement of real
// hardware pixels; that's fine, because both kitty (c=/r=) and iTerm2
// (width=/height= in cells) ask the terminal to do the final on-screen
// scaling into the cell box regardless of the transmitted image's exact
// pixel size.
func FitCells(imgW, imgH int, box Placement, caps Capabilities) (cols, rows, pxW, pxH int) {
	if imgW <= 0 {
		imgW = 1
	}
	if imgH <= 0 {
		imgH = 1
	}

	boxCols := clampPositive(box.WidthCells)
	boxRows := clampPositive(box.HeightCells)

	cellW, cellH := caps.CellWidthPx, caps.CellHeightPx
	if cellW <= 0 || cellH <= 0 {
		cellW, cellH = 1, 2
	}

	boxPxW := boxCols * cellW
	boxPxH := boxRows * cellH

	// Largest scale factor that keeps the image within both box dimensions.
	scaleW := float64(boxPxW) / float64(imgW)
	scaleH := float64(boxPxH) / float64(imgH)
	scale := scaleW
	if scaleH < scale {
		scale = scaleH
	}

	pxW = clampPositive(int(float64(imgW)*scale + 0.5))
	pxH = clampPositive(int(float64(imgH)*scale + 0.5))
	if pxW > boxPxW {
		pxW = boxPxW
	}
	if pxH > boxPxH {
		pxH = boxPxH
	}
	pxW = clampPositive(pxW)
	pxH = clampPositive(pxH)

	cols = clampPositive(ceilDiv(pxW, cellW))
	rows = clampPositive(ceilDiv(pxH, cellH))
	if cols > boxCols {
		cols = boxCols
	}
	if rows > boxRows {
		rows = boxRows
	}

	return cols, rows, pxW, pxH
}

func clampPositive(v int) int {
	if v < 1 {
		return 1
	}
	return v
}

func ceilDiv(a, b int) int {
	if b <= 0 {
		return a
	}
	return (a + b - 1) / b
}
