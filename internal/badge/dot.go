package badge

import (
	"image"
	"image/color"
	"image/draw"
)

// DotStyle is the sync status dot drawn over the top-right quarter of the
// tray icon.
type DotStyle int

const (
	DotNone     DotStyle = iota // no dot (sync off)
	DotSynced                   // solid green
	DotSyncingA                 // green ring
	DotSyncingB                 // solid green (alternates with A)
	DotOffline                  // grey ring
	DotError                    // solid red
)

var (
	// Green is the synced / syncing dot.
	Green = color.NRGBA{0x16, 0xA3, 0x4A, 0xFF}
	// Grey is the offline ring.
	Grey = color.NRGBA{0x64, 0x74, 0x8B, 0xFF}
)

// Dot returns base with the status dot s drawn over its top-right quarter
// (diameter 40% of the icon, the glyph under it cleared 1 px around). It
// returns base itself for DotNone.
func Dot(base image.Image, s DotStyle) image.Image { return dot(base, s, false) }

// DotTemplate is Dot for a macOS template image, which the system draws from
// its alpha alone: the shapes are black and differ by form (filled, ring,
// filled with a knocked-out bar).
func DotTemplate(base image.Image, s DotStyle) image.Image { return dot(base, s, true) }

func dot(base image.Image, s DotStyle, template bool) image.Image {
	if s == DotNone {
		return base
	}
	b := base.Bounds()
	q := min(b.Dx(), b.Dy()) / 2
	d := min(b.Dx(), b.Dy()) * 4 / 10
	if q <= 0 || d < 3 {
		return base
	}
	out := image.NewNRGBA(b)
	draw.Draw(out, b, base, b.Min, draw.Src)

	// The dot is centred in the top-right quarter; the cleared disc is 1 px
	// larger on every side.
	cx, cy := b.Max.X-q/2, b.Min.Y+q/2
	cleared := d + 2
	clearAt := image.Rect(cx-cleared/2, cy-cleared/2, cx-cleared/2+cleared, cy-cleared/2+cleared)
	cut := image.NewNRGBA(image.Rect(0, 0, cleared, cleared))
	disc(cut, color.NRGBA{A: 0xFF})
	draw.DrawMask(out, clearAt, image.Transparent, image.Point{}, cut, image.Point{}, draw.Src)

	shape := dotShape(d, s, template)
	at := image.Rect(cx-d/2, cy-d/2, cx-d/2+d, cy-d/2+d)
	draw.Draw(out, at, shape, image.Point{}, draw.Over)
	return out
}

// dotShape renders the d x d dot.
func dotShape(d int, s DotStyle, template bool) *image.NRGBA {
	var c color.NRGBA
	ring, bar := false, false
	switch s {
	case DotSynced:
		c = Green
	case DotSyncingA:
		c, ring = Green, true
	case DotSyncingB:
		c = Green
	case DotOffline:
		c, ring = Grey, true
	case DotError:
		c, bar = Red, true
	}
	if template {
		c = color.NRGBA{A: 0xFF}
	}
	img := image.NewNRGBA(image.Rect(0, 0, d, d))
	disc(img, c)
	hole := func(r image.Rectangle, m image.Image) {
		draw.DrawMask(img, r, image.Transparent, image.Point{}, m, image.Point{}, draw.Src)
	}
	if ring {
		t := max(2, d*22/100)
		in := d - 2*t
		if in > 0 {
			m := image.NewNRGBA(image.Rect(0, 0, in, in))
			disc(m, color.NRGBA{A: 0xFF})
			hole(image.Rect(t, t, t+in, t+in), m)
		}
	}
	if bar && template {
		h := max(2, d*20/100)
		w := d * 60 / 100
		r := image.Rect((d-w)/2, (d-h)/2, (d-w)/2+w, (d-h)/2+h)
		hole(r, image.NewUniform(color.Alpha{A: 0xFF}))
	}
	return img
}
