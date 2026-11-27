package badge

import (
	"image"
	"image/color"
	"testing"
)

func TestLabel(t *testing.T) {
	for n, want := range map[int]string{0: "", -1: "", 1: "1", 9: "9", 10: "9+", 250: "9+"} {
		if got := Label(n); got != want {
			t.Errorf("Label(%d) = %q, want %q", n, got, want)
		}
	}
}

func rgba(img image.Image, x, y int) (r, g, b, a uint8) {
	c := color.NRGBAModel.Convert(img.At(x, y)).(color.NRGBA)
	return c.R, c.G, c.B, c.A
}

func redDominant(img image.Image, x, y int) bool {
	r, g, b, a := rgba(img, x, y)
	return a > 200 && r > 180 && g < 90 && b < 90
}

func whiteish(img image.Image, x, y int) bool {
	r, g, b, a := rgba(img, x, y)
	return a > 200 && r > 200 && g > 200 && b > 200
}

func TestOverlayPixels(t *testing.T) {
	if Overlay(32, 0) != nil {
		t.Fatal("Overlay(32, 0) should be nil")
	}
	img := Overlay(32, 3)
	if img == nil {
		t.Fatal("Overlay(32, 3) is nil")
	}
	if b := img.Bounds(); b.Dx() != 32 || b.Dy() != 32 {
		t.Fatalf("bounds %v", b)
	}
	// The circle: red inside, near its top edge (above the glyph), and
	// transparent in the corners.
	if !redDominant(img, 16, 3) {
		t.Errorf("(16,3) not red: %v", img.At(16, 3))
	}
	if !redDominant(img, 4, 16) {
		t.Errorf("(4,16) not red: %v", img.At(4, 16))
	}
	if _, _, _, a := rgba(img, 0, 0); a != 0 {
		t.Errorf("corner alpha %d, want 0", a)
	}
	// The glyph: some white pixel in the middle of the badge.
	found := false
	for y := 10; y < 22 && !found; y++ {
		for x := 10; x < 22; x++ {
			if whiteish(img, x, y) {
				found = true
				break
			}
		}
	}
	if !found {
		t.Error("no white text pixel near the center")
	}
	// The label "9+" also fits inside the circle (no white outside it).
	wide := Overlay(16, 12)
	for _, p := range []image.Point{{0, 8}, {15, 8}, {8, 0}, {8, 15}} {
		if whiteish(wide, p.X, p.Y) {
			t.Errorf("9+ text reaches the edge at %v", p)
		}
	}
}

func TestComposeKeepsBaseWhenZero(t *testing.T) {
	base := image.NewNRGBA(image.Rect(0, 0, 64, 64))
	base.Set(1, 1, color.NRGBA{10, 20, 30, 255})
	if got := Compose(base, 0); got != image.Image(base) {
		t.Fatal("Compose(base, 0) should return base unchanged")
	}
	if got := ComposeTemplate(base, 0); got != image.Image(base) {
		t.Fatal("ComposeTemplate(base, 0) should return base unchanged")
	}
}

func TestComposeBottomRightQuarter(t *testing.T) {
	base := image.NewNRGBA(image.Rect(0, 0, 64, 64))
	for y := 0; y < 64; y++ {
		for x := 0; x < 64; x++ {
			base.Set(x, y, color.NRGBA{0, 0, 255, 255})
		}
	}
	got := Compose(base, 5)
	// Top-left untouched, badge red in the bottom-right quarter.
	if r, g, b, _ := rgba(got, 10, 10); r != 0 || g != 0 || b != 255 {
		t.Errorf("top-left changed: %v", got.At(10, 10))
	}
	if !redDominant(got, 48, 34) {
		t.Errorf("(48,34) not red: %v", got.At(48, 34))
	}
	if base.NRGBAAt(48, 34).B != 255 {
		t.Error("Compose modified base")
	}
}

func TestComposeTemplateKnocksOutText(t *testing.T) {
	base := image.NewNRGBA(image.Rect(0, 0, 64, 64))
	got := ComposeTemplate(base, 5)
	// The disc is opaque; the digit is cut out of it (a template image
	// only keeps alpha).
	if _, _, _, a := rgba(got, 48, 34); a < 200 {
		t.Errorf("disc alpha %d", a)
	}
	clear := false
	for y := 42; y < 54 && !clear; y++ {
		for x := 42; x < 54; x++ {
			if _, _, _, a := rgba(got, x, y); a < 60 {
				clear = true
				break
			}
		}
	}
	if !clear {
		t.Error("digit not knocked out of the template badge")
	}
}
