package terminal

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestFitCells_PreservesAspectRatio(t *testing.T) {
	caps := Capabilities{CellWidthPx: 8, CellHeightPx: 16}
	box := Placement{WidthCells: 80, HeightCells: 24}

	cols, rows, pxW, pxH := FitCells(1920, 1080, box, caps)

	require := assert.New(t)
	require.GreaterOrEqual(cols, 1)
	require.GreaterOrEqual(rows, 1)
	require.LessOrEqual(cols, box.WidthCells)
	require.LessOrEqual(rows, box.HeightCells)
	require.Greater(pxW, 0)
	require.Greater(pxH, 0)

	wantRatio := 1920.0 / 1080.0
	gotRatio := float64(pxW) / float64(pxH)
	require.InDelta(wantRatio, gotRatio, 0.05, "resized pixel dimensions should preserve source aspect ratio")
}

func TestFitCells_TinyWindow(t *testing.T) {
	caps := Capabilities{CellWidthPx: 8, CellHeightPx: 16}
	box := Placement{WidthCells: 3, HeightCells: 2}

	cols, rows, pxW, pxH := FitCells(1920, 1080, box, caps)

	assert.GreaterOrEqual(t, cols, 1)
	assert.GreaterOrEqual(t, rows, 1)
	assert.GreaterOrEqual(t, pxW, 1)
	assert.GreaterOrEqual(t, pxH, 1)
	assert.LessOrEqual(t, cols, 3)
	assert.LessOrEqual(t, rows, 2)
}

func TestFitCells_UnknownCellGeometryAssumesOneToTwoAspect(t *testing.T) {
	caps := Capabilities{} // CellWidthPx/CellHeightPx unset
	box := Placement{WidthCells: 40, HeightCells: 40}

	cols, rows, _, _ := FitCells(100, 100, box, caps) // square image

	// With an assumed 1:2 (width:height) cell aspect, a square image should
	// end up roughly twice as many columns as rows.
	assert.InDelta(t, float64(cols), float64(rows)*2, 1)
}

func TestFitCells_ExtremeAspectRatio(t *testing.T) {
	caps := Capabilities{CellWidthPx: 9, CellHeightPx: 18}
	box := Placement{WidthCells: 80, HeightCells: 24}

	cols, rows, pxW, pxH := FitCells(100, 4000, box, caps)

	assert.GreaterOrEqual(t, cols, 1)
	assert.GreaterOrEqual(t, rows, 1)
	assert.LessOrEqual(t, cols, 80)
	assert.LessOrEqual(t, rows, 24)
	assert.Greater(t, pxW, 0)
	assert.Greater(t, pxH, 0)
	// A 100x4000 source is far taller than wide, so it should end up
	// hugging the height of the box (few columns, many rows) rather than
	// the width.
	assert.Less(t, cols, rows)
}

func TestFitCells_NeverZeroOrNegative(t *testing.T) {
	cases := []struct {
		imgW, imgH int
		box        Placement
		caps       Capabilities
	}{
		{0, 0, Placement{}, Capabilities{}},
		{-5, -5, Placement{WidthCells: -1, HeightCells: -1}, Capabilities{}},
		{1, 1, Placement{WidthCells: 1, HeightCells: 1}, Capabilities{CellWidthPx: -1, CellHeightPx: -1}},
	}
	for _, tc := range cases {
		cols, rows, pxW, pxH := FitCells(tc.imgW, tc.imgH, tc.box, tc.caps)
		assert.GreaterOrEqual(t, cols, 1)
		assert.GreaterOrEqual(t, rows, 1)
		assert.GreaterOrEqual(t, pxW, 1)
		assert.GreaterOrEqual(t, pxH, 1)
	}
}
