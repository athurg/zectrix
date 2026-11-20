package textimg

import (
	"image"
	"testing"

	"golang.org/x/image/math/fixed"
)

func TestFreeTypeGlyphProducesVisibleBitmap(t *testing.T) {
	font, err := LoadFont("")
	if err != nil {
		t.Fatal(err)
	}
	face, err := newFreeTypeFace(font, 16)
	if err != nil {
		t.Fatal(err)
	}
	defer face.Close()
	dr, mask, maskp, advance, ok := face.Glyph(fixed.Point26_6{X: fixed.I(16), Y: fixed.I(29)}, '大')
	alpha := mask.(*image.Alpha)
	ink := 0
	for _, v := range alpha.Pix {
		if v != 0 {
			ink++
		}
	}
	if !ok || advance <= 0 {
		t.Fatalf("glyph not loaded: ok=%v advance=%v", ok, advance)
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
