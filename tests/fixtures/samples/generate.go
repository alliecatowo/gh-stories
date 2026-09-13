//go:build ignore

// Generates the sample photographs used by the local demo, the website demo
// and the launch assets.
//
// Everything here is drawn procedurally, so provenance is trivially clean:
// no third-party image is used, nobody's private media is involved, and no
// real person is depicted. See README.md.
package main

import (
	"fmt"
	"image"
	"image/color"
	"image/jpeg"
	"math"
	"math/rand"
	"os"
)

const (
	portraitW, portraitH   = 1080, 1920
	landscapeW, landscapeH = 1600, 1067
)

func main() {
	write("concert.jpg", concert(portraitW, portraitH))
	write("cat.jpg", cat(portraitW, portraitH))
	write("food.jpg", food(landscapeW, landscapeH))
	write("sunset.jpg", sunset(landscapeW, landscapeH))
	fmt.Println("wrote 4 sample photographs")
}

func write(name string, img image.Image) {
	f, err := os.Create(name)
	if err != nil {
		panic(err)
	}
	defer f.Close()
	if err := jpeg.Encode(f, img, &jpeg.Options{Quality: 88}); err != nil {
		panic(err)
	}
}

func lerp(a, b float64, t float64) float64 { return a + (b-a)*t }

func clamp8(v float64) uint8 {
	if v < 0 {
		return 0
	}
	if v > 255 {
		return 255
	}
	return uint8(v)
}

// grain adds fine luminance noise so the result reads as a photograph rather
// than a vector illustration.
func grain(img *image.RGBA, amount float64, seed int64) {
	rng := rand.New(rand.NewSource(seed))
	b := img.Bounds()
	for y := b.Min.Y; y < b.Max.Y; y++ {
		for x := b.Min.X; x < b.Max.X; x++ {
			n := (rng.Float64() - 0.5) * amount
			c := img.RGBAAt(x, y)
			img.SetRGBA(x, y, color.RGBA{
				clamp8(float64(c.R) + n), clamp8(float64(c.G) + n),
				clamp8(float64(c.B) + n), 255,
			})
		}
	}
}

// vignette darkens the corners, as a lens does.
func vignette(img *image.RGBA, strength float64) {
	b := img.Bounds()
	cx, cy := float64(b.Dx())/2, float64(b.Dy())/2
	maxD := math.Hypot(cx, cy)
	for y := b.Min.Y; y < b.Max.Y; y++ {
		for x := b.Min.X; x < b.Max.X; x++ {
			d := math.Hypot(float64(x)-cx, float64(y)-cy) / maxD
			f := 1 - strength*d*d
			c := img.RGBAAt(x, y)
			img.SetRGBA(x, y, color.RGBA{
				clamp8(float64(c.R) * f), clamp8(float64(c.G) * f),
				clamp8(float64(c.B) * f), 255,
			})
		}
	}
}

// glow draws a soft radial light.
func glow(img *image.RGBA, cx, cy, radius float64, col color.RGBA, intensity float64) {
	b := img.Bounds()
	for y := b.Min.Y; y < b.Max.Y; y++ {
		for x := b.Min.X; x < b.Max.X; x++ {
			d := math.Hypot(float64(x)-cx, float64(y)-cy)
			if d > radius {
				continue
			}
			t := 1 - d/radius
			t = t * t * intensity
			c := img.RGBAAt(x, y)
			img.SetRGBA(x, y, color.RGBA{
				clamp8(float64(c.R) + float64(col.R)*t),
				clamp8(float64(c.G) + float64(col.G)*t),
				clamp8(float64(c.B) + float64(col.B)*t),
				255,
			})
		}
	}
}

func fillEllipse(img *image.RGBA, cx, cy, rx, ry float64, col color.RGBA) {
	b := img.Bounds()
	for y := b.Min.Y; y < b.Max.Y; y++ {
		for x := b.Min.X; x < b.Max.X; x++ {
			dx := (float64(x) - cx) / rx
			dy := (float64(y) - cy) / ry
			if dx*dx+dy*dy <= 1 {
				img.SetRGBA(x, y, col)
			}
		}
	}
}

func fillTriangle(img *image.RGBA, x1, y1, x2, y2, x3, y3 float64, col color.RGBA) {
	minX := int(math.Min(x1, math.Min(x2, x3)))
	maxX := int(math.Max(x1, math.Max(x2, x3)))
	minY := int(math.Min(y1, math.Min(y2, y3)))
	maxY := int(math.Max(y1, math.Max(y2, y3)))
	sign := func(ax, ay, bx, by, cx, cy float64) float64 {
		return (ax-cx)*(by-cy) - (bx-cx)*(ay-cy)
	}
	for y := minY; y <= maxY; y++ {
		for x := minX; x <= maxX; x++ {
			px, py := float64(x), float64(y)
			d1 := sign(px, py, x1, y1, x2, y2)
			d2 := sign(px, py, x2, y2, x3, y3)
			d3 := sign(px, py, x3, y3, x1, y1)
			neg := d1 < 0 || d2 < 0 || d3 < 0
			pos := d1 > 0 || d2 > 0 || d3 > 0
			if !(neg && pos) && image.Pt(x, y).In(img.Bounds()) {
				img.SetRGBA(x, y, col)
			}
		}
	}
}

// concert: a dark venue, stage haze, coloured beams, a crowd silhouette.
func concert(w, h int) image.Image {
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		t := float64(y) / float64(h)
		for x := 0; x < w; x++ {
			img.SetRGBA(x, y, color.RGBA{
				clamp8(lerp(18, 6, t)), clamp8(lerp(10, 4, t)), clamp8(lerp(34, 12, t)), 255,
			})
		}
	}
	// Stage haze.
	glow(img, float64(w)*0.5, float64(h)*0.34, float64(w)*0.9,
		color.RGBA{60, 24, 80, 255}, 0.55)

	// Beams from the rig.
	beams := []struct {
		x float64
		c color.RGBA
	}{
		{0.18, color.RGBA{255, 70, 130, 255}},
		{0.38, color.RGBA{120, 90, 255, 255}},
		{0.62, color.RGBA{255, 150, 60, 255}},
		{0.82, color.RGBA{80, 220, 255, 255}},
	}
	for _, b := range beams {
		ox := float64(w) * b.x
		fillTriangle(img, ox, float64(h)*0.06,
			ox-float64(w)*0.20, float64(h)*0.62,
			ox+float64(w)*0.20, float64(h)*0.62,
			color.RGBA{b.c.R / 7, b.c.G / 7, b.c.B / 7, 255})
		glow(img, ox, float64(h)*0.10, float64(w)*0.16, b.c, 0.95)
	}

	// Stage lip.
	for y := int(float64(h) * 0.60); y < int(float64(h)*0.64); y++ {
		for x := 0; x < w; x++ {
			img.SetRGBA(x, y, color.RGBA{90, 40, 110, 255})
		}
	}

	// Crowd: overlapping silhouettes, some with raised phones.
	rng := rand.New(rand.NewSource(7))
	for i := 0; i < 90; i++ {
		cx := rng.Float64() * float64(w)
		base := float64(h) * (0.70 + rng.Float64()*0.28)
		r := float64(w) * (0.035 + rng.Float64()*0.03)
		fillEllipse(img, cx, base-r*2.4, r*0.62, r*0.72, color.RGBA{4, 3, 9, 255})
		fillEllipse(img, cx, base, r*1.25, r*2.0, color.RGBA{4, 3, 9, 255})
		if rng.Float64() < 0.18 {
			px, py := cx+r*1.1, base-r*3.4
			fillEllipse(img, px, py, r*0.20, r*0.34, color.RGBA{16, 14, 24, 255})
			glow(img, px, py, r*0.9, color.RGBA{200, 220, 255, 255}, 0.7)
		}
	}
	vignette(img, 0.55)
	grain(img, 16, 11)
	return img
}

// cat: a tabby asleep on a warm surface.
func cat(w, h int) image.Image {
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		t := float64(y) / float64(h)
		for x := 0; x < w; x++ {
			u := float64(x) / float64(w)
			img.SetRGBA(x, y, color.RGBA{
				clamp8(lerp(214, 168, t) + 12*math.Sin(u*7)),
				clamp8(lerp(198, 150, t) + 10*math.Sin(u*7)),
				clamp8(lerp(178, 132, t) + 8*math.Sin(u*7)),
				255,
			})
		}
	}
	// Blanket folds.
	for i := 0; i < 8; i++ {
		yy := float64(h) * (0.55 + float64(i)*0.055)
		for x := 0; x < w; x++ {
			off := 26 * math.Sin(float64(x)/float64(w)*6+float64(i))
			for d := 0; d < 16; d++ {
				y := int(yy + off + float64(d))
				if y >= 0 && y < h {
					c := img.RGBAAt(x, y)
					img.SetRGBA(x, y, color.RGBA{
						clamp8(float64(c.R) * 0.93), clamp8(float64(c.G) * 0.93),
						clamp8(float64(c.B) * 0.93), 255})
				}
			}
		}
	}

	fur := color.RGBA{196, 150, 96, 255}
	dark := color.RGBA{120, 88, 54, 255}
	cx, cy := float64(w)*0.5, float64(h)*0.56

	// Body and curled tail.
	fillEllipse(img, cx, cy+float64(h)*0.10, float64(w)*0.40, float64(h)*0.15, fur)
	fillEllipse(img, cx+float64(w)*0.34, cy+float64(h)*0.13, float64(w)*0.13, float64(h)*0.035, fur)
	// Head.
	fillEllipse(img, cx, cy-float64(h)*0.045, float64(w)*0.21, float64(h)*0.105, fur)
	// Ears.
	fillTriangle(img, cx-float64(w)*0.20, cy-float64(h)*0.085,
		cx-float64(w)*0.07, cy-float64(h)*0.135,
		cx-float64(w)*0.055, cy-float64(h)*0.055, fur)
	fillTriangle(img, cx+float64(w)*0.20, cy-float64(h)*0.085,
		cx+float64(w)*0.07, cy-float64(h)*0.135,
		cx+float64(w)*0.055, cy-float64(h)*0.055, fur)
	// Tabby stripes.
	for i := 0; i < 5; i++ {
		yy := cy + float64(h)*(0.045+float64(i)*0.028)
		fillEllipse(img, cx-float64(w)*0.16+float64(i)*float64(w)*0.08, yy,
			float64(w)*0.035, float64(h)*0.009, dark)
	}
	// Closed eyes and nose.
	fillEllipse(img, cx-float64(w)*0.075, cy-float64(h)*0.052, float64(w)*0.034, float64(h)*0.004, dark)
	fillEllipse(img, cx+float64(w)*0.075, cy-float64(h)*0.052, float64(w)*0.034, float64(h)*0.004, dark)
	fillEllipse(img, cx, cy-float64(h)*0.022, float64(w)*0.020, float64(h)*0.009,
		color.RGBA{206, 122, 126, 255})

	glow(img, float64(w)*0.22, float64(h)*0.16, float64(w)*0.7,
		color.RGBA{255, 214, 150, 255}, 0.30)
	vignette(img, 0.35)
	grain(img, 12, 23)
	return img
}

// food: a bowl of noodles shot from above.
func food(w, h int) image.Image {
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			n := 8 * math.Sin(float64(x)/23) * math.Cos(float64(y)/29)
			img.SetRGBA(x, y, color.RGBA{
				clamp8(52 + n), clamp8(44 + n), clamp8(40 + n), 255})
		}
	}
	cx, cy := float64(w)*0.5, float64(h)*0.52
	rx, ry := float64(w)*0.30, float64(h)*0.44
	fillEllipse(img, cx, cy, rx*1.08, ry*1.08, color.RGBA{232, 228, 220, 255})
	fillEllipse(img, cx, cy, rx, ry, color.RGBA{206, 200, 190, 255})
	fillEllipse(img, cx, cy, rx*0.92, ry*0.92, color.RGBA{168, 116, 62, 255})

	// Noodles.
	rng := rand.New(rand.NewSource(31))
	for i := 0; i < 260; i++ {
		a := rng.Float64() * 2 * math.Pi
		rr := rng.Float64() * 0.8
		nx := cx + math.Cos(a)*rx*rr
		ny := cy + math.Sin(a)*ry*rr
		fillEllipse(img, nx, ny, float64(w)*0.035, float64(h)*0.007,
			color.RGBA{clamp8(236 + rng.Float64()*14), clamp8(206 + rng.Float64()*16), 138, 255})
	}
	// Toppings: egg, greens, chilli oil.
	fillEllipse(img, cx-rx*0.34, cy-ry*0.18, float64(w)*0.062, float64(h)*0.085,
		color.RGBA{248, 244, 232, 255})
	fillEllipse(img, cx-rx*0.34, cy-ry*0.18, float64(w)*0.030, float64(h)*0.042,
		color.RGBA{240, 176, 52, 255})
	for i := 0; i < 22; i++ {
		a := rng.Float64() * 2 * math.Pi
		rr := 0.3 + rng.Float64()*0.5
		fillEllipse(img, cx+math.Cos(a)*rx*rr, cy+math.Sin(a)*ry*rr,
			float64(w)*0.016, float64(h)*0.020, color.RGBA{62, 122, 54, 255})
	}
	for i := 0; i < 14; i++ {
		a := rng.Float64() * 2 * math.Pi
		rr := rng.Float64() * 0.7
		fillEllipse(img, cx+math.Cos(a)*rx*rr, cy+math.Sin(a)*ry*rr,
			float64(w)*0.012, float64(h)*0.014, color.RGBA{186, 52, 30, 255})
	}
	glow(img, float64(w)*0.30, float64(h)*0.18, float64(w)*0.6,
		color.RGBA{255, 230, 190, 255}, 0.22)
	vignette(img, 0.42)
	grain(img, 10, 41)
	return img
}

// sunset: a wide horizon.
func sunset(w, h int) image.Image {
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	horizon := float64(h) * 0.62
	for y := 0; y < h; y++ {
		t := float64(y) / horizon
		var r, g, b float64
		if float64(y) < horizon {
			r = lerp(48, 252, t)
			g = lerp(34, 146, t)
			b = lerp(92, 92, t)
		} else {
			u := (float64(y) - horizon) / (float64(h) - horizon)
			r = lerp(214, 28, u)
			g = lerp(112, 22, u)
			b = lerp(96, 46, u)
		}
		for x := 0; x < w; x++ {
			img.SetRGBA(x, y, color.RGBA{clamp8(r), clamp8(g), clamp8(b), 255})
		}
	}
	glow(img, float64(w)*0.62, horizon-float64(h)*0.02, float64(w)*0.34,
		color.RGBA{255, 214, 120, 255}, 1.0)
	fillEllipse(img, float64(w)*0.62, horizon-float64(h)*0.03,
		float64(w)*0.055, float64(h)*0.075, color.RGBA{255, 238, 190, 255})
	// Distant ridge.
	for x := 0; x < w; x++ {
		ridge := horizon - float64(h)*0.05*
			(0.5+0.5*math.Sin(float64(x)/float64(w)*5.2))
		for y := int(ridge); y < int(horizon); y++ {
			img.SetRGBA(x, y, color.RGBA{52, 34, 62, 255})
		}
	}
	vignette(img, 0.40)
	grain(img, 9, 53)
	return img
}
