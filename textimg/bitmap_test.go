package textimg

import (
	"image"
	"testing"

	"golang.org/x/image/math/fixed"
)

func TestEmbeddedGlyphUsesBitmapPosition(t *testing.T) {
	font, err := LoadFont("")
	if err != nil {
		t.Fatal(err)
	}
	face, err := face(font, 16)
	if err != nil {
		t.Fatal(err)
	}
	defer face.Close()
	dr, mask, maskp, advance, ok := face.Glyph(fixed.Point26_6{X: fixed.I(16), Y: fixed.I(29)}, '大')
	alpha, isAlpha := mask.(*image.Alpha)
	if !ok || !isAlpha || advance != fixed.I(16) {
		t.Fatalf("glyph not loaded: ok=%v mask=%T advance=%v", ok, mask, advance)
	}
	ink := 0
	for _, v := range alpha.Pix {
		switch v {
		case 0:
		case 255:
			ink++
		default:
			t.Fatalf("expected a 1bpp glyph mask, got alpha %d", v)
		}
	}
	if want := image.Pt(16, 15); dr.Min != want {
		t.Fatalf("glyph origin %v, want %v", dr.Min, want)
	}
	if ink == 0 {
		t.Fatal("glyph mask is empty")
	}
	if maskp != (image.Point{}) {
		t.Fatalf("mask point %v", maskp)
	}
}

func TestEmbeddedFontUsesOutlineOutsideStrikes(t *testing.T) {
	font, err := LoadFont("")
	if err != nil {
		t.Fatal(err)
	}
	id, err := font.GlyphIndex(nil, '大')
	if err != nil {
		t.Fatal(err)
	}
	for size := MinFontSize; size <= MaxFontSize; size++ {
		_, hasBitmap := font.bitmaps.strike(size).glyph(id)
		if hasBitmap != (size <= 24) {
			t.Fatalf("%dpx: has bitmap = %v", size, hasBitmap)
		}
	}
	fc, err := face(font, 25)
	if err != nil {
		t.Fatal(err)
	}
	defer fc.Close()
	_, mask, _, advance, ok := fc.Glyph(fixed.Point26_6{Y: fixed.I(30)}, '大')
	if !ok || advance <= 0 {
		t.Fatalf("outline glyph not loaded: ok=%v advance=%v", ok, advance)
	}
	for _, v := range mask.(*image.Alpha).Pix {
		if v > 0 && v < 255 {
			return
		}
	}
	t.Fatal("outline glyph has no antialiased pixels")
}
