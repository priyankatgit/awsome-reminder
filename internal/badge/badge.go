// Package badge draws the pending-count badge: a red disc with a bold white
// number, used as the Windows taskbar overlay and composited onto the tray
// icon.
package badge

import (
	"image"
	"image/color"
	"image/draw"
	"math"
	"strconv"
	"sync"

	"golang.org/x/image/font"
	"golang.org/x/image/font/gofont/gobold"
	"golang.org/x/image/font/opentype"
	"golang.org/x/image/math/fixed"
)

// Red is the badge disc: the UI's danger red (--danger in the light theme,
// #DC2626), which has 4.8:1 contrast with the white number.
var Red = color.NRGBA{R: 0xDC, G: 0x26, B: 0x26, A: 0xFF}

// Label is the badge text for n open items: "" for none, "1".."9", and
// "9+" above 9.
func Label(n int) string {
	switch {
	case n <= 0:
		return ""
	case n > 9:
		return "9+"
	default:
		return strconv.Itoa(n)
	}
}

var (
	fontOnce sync.Once
	boldFont *opentype.Font
	fontErr  error
)

func bold() (*opentype.Font, error) {
	fontOnce.Do(func() { boldFont, fontErr = opentype.Parse(gobold.TTF) })
	return boldFont, fontErr
}

// Overlay is a size x size badge for n items: a red disc with the label in
// white bold. It is nil for 0.
func Overlay(size, n int) image.Image {
	if Label(n) == "" || size <= 0 {
		return nil
	}
	return render(size, n, false)
}

// render renders the badge. With knockout the label is cut out of the disc
// (transparent) instead of painted white.
func render(size, n int, knockout bool) *image.NRGBA {
	label := Label(n)
	img := image.NewNRGBA(image.Rect(0, 0, size, size))
	disc(img, Red)

	// The label as an alpha mask, centred on the disc by its ink bounds.
	mask := image.NewAlpha(img.Rect)
	if f, err := bold(); err == nil {
		px := 0.78 * float64(size) // one digit: cap height about half the disc
		if len(label) > 1 {
			px = 0.60 * float64(size)
		}
		face, err := opentype.NewFace(f, &opentype.FaceOptions{Size: px, DPI: 72, Hinting: font.HintingNone})
		if err == nil {
			d := &font.Drawer{Dst: mask, Src: image.Opaque, Face: face}
			b, _ := d.BoundString(label)
			w := (b.Max.X - b.Min.X).Ceil()
			h := (b.Max.Y - b.Min.Y).Ceil()
			// Keep a margin inside the disc for wide labels.
			if maxW := int(0.80 * float64(size)); w > maxW && len(label) > 1 {
				face.Close()
				px *= float64(maxW) / float64(w)
				face, _ = opentype.NewFace(f, &opentype.FaceOptions{Size: px, DPI: 72, Hinting: font.HintingNone})
				d.Face = face
				b, _ = d.BoundString(label)
				w = (b.Max.X - b.Min.X).Ceil()
				h = (b.Max.Y - b.Min.Y).Ceil()
			}
			x := fixed.I((size-w)/2) - b.Min.X
			y := fixed.I((size-h)/2) - b.Min.Y
			d.Dot = fixed.Point26_6{X: x, Y: y}
			d.DrawString(label)
			face.Close()
		}
	}
	for i, a := range mask.Pix {
		if a == 0 {
			continue
		}
		p := img.Pix[i*4 : i*4+4]
		if knockout {
			p[3] = uint8(uint32(p[3]) * uint32(255-a) / 255)
			continue
		}
		// White over red, by the glyph's coverage.
		for c := 0; c < 3; c++ {
			p[c] = uint8((uint32(p[c])*uint32(255-a) + 255*uint32(a)) / 255)
		}
	}
	return img
}

// disc fills an anti-aliased circle covering img.
func disc(img *image.NRGBA, c color.NRGBA) {
	size := img.Rect.Dx()
	r := float64(size) / 2
	const ss = 4 // 4x4 supersampling for the edge
	for y := 0; y < size; y++ {
		for x := 0; x < size; x++ {
			in := 0
			for sy := 0; sy < ss; sy++ {
				for sx := 0; sx < ss; sx++ {
					dx := float64(x) + (float64(sx)+0.5)/ss - r
					dy := float64(y) + (float64(sy)+0.5)/ss - r
					if dx*dx+dy*dy <= r*r {
						in++
					}
				}
			}
			if in == 0 {
				continue
			}
			img.SetNRGBA(x, y, color.NRGBA{c.R, c.G, c.B, uint8(math.Round(float64(c.A) * float64(in) / ss / ss))})
		}
	}
}

// Compose returns base with the badge for n drawn over its bottom-right
// quarter. It returns base itself for 0.
func Compose(base image.Image, n int) image.Image {
	return compose(base, n, false)
}

// ComposeTemplate is Compose for a macOS template image, which the system
// draws from its alpha alone: the number is cut out of the disc so it stays
// readable.
func ComposeTemplate(base image.Image, n int) image.Image {
	return compose(base, n, true)
}

func compose(base image.Image, n int, knockout bool) image.Image {
	if Label(n) == "" {
		return base
	}
	b := base.Bounds()
	size := min(b.Dx(), b.Dy()) / 2
	if size <= 0 {
		return base
	}
	out := image.NewNRGBA(b)
	draw.Draw(out, b, base, b.Min, draw.Src)
	at := image.Rect(b.Max.X-size, b.Max.Y-size, b.Max.X, b.Max.Y)
	// Clear the quarter under the disc first, so the glyph behind it does
	// not show through the knocked-out number or the anti-aliased edge.
	bdg := render(size, n, knockout)
	cut := image.NewNRGBA(bdg.Rect)
	disc(cut, color.NRGBA{A: 0xFF})
	draw.DrawMask(out, at, image.Transparent, image.Point{}, cut, image.Point{}, draw.Src)
	draw.Draw(out, at, bdg, image.Point{}, draw.Over)
	return out
}
