package badge

import (
	"image"
	"image/color"
	"testing"
)

func blank(n int) *image.NRGBA {
	img := image.NewNRGBA(image.Rect(0, 0, n, n))
	for i := 0; i < len(img.Pix); i += 4 {
		img.Pix[i+3] = 0xFF // opaque black glyph
	}
	return img
}

func near(a, b uint8) bool { return int(a)-int(b) < 8 && int(b)-int(a) < 8 }

func isColor(img image.Image, x, y int, c color.NRGBA) bool {
	r, g, b, a := rgba(img, x, y)
	return a == 0xFF && near(r, c.R) && near(g, c.G) && near(b, c.B)
}

func TestDotPixels(t *testing.T) {
	base := blank(32)
	// Dot centre is (24, 8); diameter 12.
	if !isColor(Dot(base, DotSynced), 24, 8, Green) {
		t.Error("synced centre not green")
	}
	off := Dot(base, DotOffline)
	if !isColor(off, 24-5, 8, Grey) {
		t.Error("offline ring pixel not grey")
	}
	if _, _, _, a := rgba(off, 24, 8); a != 0 {
		t.Errorf("offline centre alpha %d, want transparent", a)
	}
	if !isColor(Dot(base, DotError), 24, 8, Red) {
		t.Error("error centre not red")
	}
	if _, _, _, a := rgba(Dot(base, DotSyncingA), 24, 8); a != 0 {
		t.Error("syncing A centre should be a ring")
	}
	if !isColor(Dot(base, DotSyncingB), 24, 8, Green) {
		t.Error("syncing B centre not green")
	}
	// Glyph cleared 1 px around the dot: just outside the 13 px disc.
	if _, _, _, a := rgba(Dot(base, DotSynced), 24+6, 8); a != 0 {
		t.Error("glyph not cleared next to the dot")
	}
	if _, _, _, a := rgba(Dot(base, DotSynced), 24, 17); a != 0xFF {
		t.Error("glyph cleared too far from the dot")
	}
	if got := Dot(base, DotNone); got != image.Image(base) {
		t.Error("DotNone must return base unchanged")
	}
}

func TestDotTemplateAlphaOnly(t *testing.T) {
	for _, s := range []DotStyle{DotSynced, DotSyncingA, DotSyncingB, DotOffline, DotError} {
		img := DotTemplate(blank(32), s)
		for y := 0; y < 32; y++ {
			for x := 0; x < 32; x++ {
				r, g, b, a := rgba(img, x, y)
				if a > 0 && (r != 0 || g != 0 || b != 0) {
					t.Fatalf("style %d (%d,%d) = %d,%d,%d,%d: not black", s, x, y, r, g, b, a)
				}
			}
		}
	}
	// Error has a knocked-out bar through the middle; synced is filled.
	if _, _, _, a := rgba(DotTemplate(blank(32), DotError), 24, 8); a != 0 {
		t.Error("error bar not knocked out")
	}
	if _, _, _, a := rgba(DotTemplate(blank(32), DotSynced), 24, 8); a != 0xFF {
		t.Error("synced not filled")
	}
}

func TestDotAndBadgeDontOverlap(t *testing.T) {
	img := Dot(Compose(blank(32), 3), DotSynced)
	if !isColor(img, 18, 24, Red) {
		t.Error("bottom-right badge not red")
	}
	if !isColor(img, 24, 8, Green) {
		t.Error("top-right dot not green")
	}
}
