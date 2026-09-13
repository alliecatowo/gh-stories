package media_test

import (
	"bytes"
	"encoding/binary"
	"image"
	"image/color"
	"image/jpeg"
	"testing"

	"github.com/stretchr/testify/require"
)

// buildEXIFJPEG encodes a w x h JPEG and splices in a hand-built EXIF APP1
// segment carrying an Orientation tag and a minimal GPS IFD — the same two
// pieces of metadata a real phone photo carries that this package's tests
// need to prove are gone from every output variant.
func buildEXIFJPEG(t *testing.T, w, h int, orientation uint16) []byte {
	t.Helper()

	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			img.Set(x, y, color.RGBA{R: uint8(x % 256), G: uint8(y % 256), B: 200, A: 255})
		}
	}
	var base bytes.Buffer
	require.NoError(t, jpeg.Encode(&base, img, &jpeg.Options{Quality: 90}))
	raw := base.Bytes()
	require.True(t, len(raw) > 4 && raw[0] == 0xFF && raw[1] == 0xD8, "not a JPEG")

	app1 := buildEXIFAPP1(orientation)

	out := make([]byte, 0, len(raw)+len(app1))
	out = append(out, raw[:2]...) // SOI
	out = append(out, app1...)
	out = append(out, raw[2:]...)
	return out
}

// buildEXIFAPP1 returns a complete "FF E1 <len> Exif\0\0 <TIFF+IFD0+GPS IFD>"
// segment: IFD0 has an Orientation entry and a GPSInfoIFDPointer entry
// pointing at a one-entry GPS IFD (GPSLatitudeRef="N"), all little-endian.
func buildEXIFAPP1(orientation uint16) []byte {
	const (
		tiffHeaderLen = 8
		ifd0Entries   = 2
		ifd0Len       = 2 + 12*ifd0Entries + 4
		gpsIFDOffset  = tiffHeaderLen + ifd0Len
		gpsEntries    = 1
		gpsIFDLen     = 2 + 12*gpsEntries + 4
	)

	var tiff bytes.Buffer
	// TIFF header: byte order "II" (little-endian), magic 42, IFD0 offset 8.
	tiff.WriteString("II")
	binary.Write(&tiff, binary.LittleEndian, uint16(42))
	binary.Write(&tiff, binary.LittleEndian, uint32(tiffHeaderLen))

	// IFD0
	binary.Write(&tiff, binary.LittleEndian, uint16(ifd0Entries))

	writeEntry := func(tag, typ uint16, count uint32, value uint32) {
		binary.Write(&tiff, binary.LittleEndian, tag)
		binary.Write(&tiff, binary.LittleEndian, typ)
		binary.Write(&tiff, binary.LittleEndian, count)
		binary.Write(&tiff, binary.LittleEndian, value)
	}
	// Orientation: tag 0x0112, type SHORT(3), count 1, value in low 16 bits.
	writeEntry(0x0112, 3, 1, uint32(orientation))
	// GPSInfoIFDPointer: tag 0x8825, type LONG(4), count 1, offset to GPS IFD.
	writeEntry(0x8825, 4, 1, uint32(gpsIFDOffset))
	binary.Write(&tiff, binary.LittleEndian, uint32(0)) // no next IFD

	// GPS IFD: one entry, GPSLatitudeRef (tag 1, ASCII, "N\0" inline).
	binary.Write(&tiff, binary.LittleEndian, uint16(gpsEntries))
	binary.Write(&tiff, binary.LittleEndian, uint16(0x0001)) // GPSLatitudeRef
	binary.Write(&tiff, binary.LittleEndian, uint16(2))      // ASCII
	binary.Write(&tiff, binary.LittleEndian, uint32(2))      // "N\0"
	tiff.Write([]byte{'N', 0, 0, 0})                         // inline value, padded to 4 bytes
	binary.Write(&tiff, binary.LittleEndian, uint32(0))      // no next IFD

	payload := append([]byte("Exif\x00\x00"), tiff.Bytes()...)

	var seg bytes.Buffer
	seg.Write([]byte{0xFF, 0xE1})
	binary.Write(&seg, binary.BigEndian, uint16(len(payload)+2))
	seg.Write(payload)
	return seg.Bytes()
}
