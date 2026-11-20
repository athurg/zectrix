package textimg

import (
	"errors"
	"image"

	"golang.org/x/image/font"
	"golang.org/x/image/font/opentype"
	"golang.org/x/image/font/sfnt"
	"golang.org/x/image/math/fixed"
)

func face(f *Font, size int) (font.Face, error) {
	if f == nil || f.Font == nil {
		return nil, errors.New("textimg: font is required")
	}
	outline, err := opentype.NewFace(f.Font, &opentype.FaceOptions{
		Size: float64(size), DPI: 72, Hinting: font.HintingFull,
	})
	if err != nil {
		return nil, err
	}
	metrics := outline.Metrics()
	// Keep the scaled outline ascender and descender, even for bitmap glyphs.
	// FreeType rounds the outline line height to the nearest pixel.
	unhinted, _ := f.Metrics(nil, fixed.I(size), font.HintingNone)
	metrics.Height = fixed.I(unhinted.Height.Round())
	return &renderFace{
		Face: outline, outline: f.Font, strike: f.bitmaps.strike(size), metrics: metrics,
	}, nil
}

type renderFace struct {
	font.Face
	outline *opentype.Font
	strike  bitmapStrike
	metrics font.Metrics
	buffer  sfnt.Buffer
}

func (f *renderFace) Metrics() font.Metrics {
	return f.metrics
}

func (f *renderFace) bitmap(r rune) (bitmapGlyph, bool) {
	if len(f.strike.records) == 0 {
		return bitmapGlyph{}, false
	}
	id, err := f.outline.GlyphIndex(&f.buffer, r)
	if err != nil || id == 0 {
		return bitmapGlyph{}, false
	}
	return f.strike.glyph(id)
}

func (f *renderFace) Glyph(dot fixed.Point26_6, r rune) (image.Rectangle, image.Image, image.Point, fixed.Int26_6, bool) {
	glyph, ok := f.bitmap(r)
	if !ok {
		return f.Face.Glyph(dot, r)
	}
	mask := glyph.mask()
	min := image.Pt(dot.X.Round()+glyph.left, dot.Y.Round()-glyph.top)
	return image.Rectangle{Min: min, Max: min.Add(mask.Bounds().Size())}, mask, image.Point{}, glyph.advance, true
}

func (f *renderFace) GlyphBounds(r rune) (fixed.Rectangle26_6, fixed.Int26_6, bool) {
	glyph, ok := f.bitmap(r)
	if !ok {
		return f.Face.GlyphBounds(r)
	}
	return fixed.R(glyph.left, -glyph.top, glyph.left+glyph.width, glyph.height-glyph.top), glyph.advance, true
}

func (f *renderFace) GlyphAdvance(r rune) (fixed.Int26_6, bool) {
	glyph, ok := f.bitmap(r)
	if !ok {
		return f.Face.GlyphAdvance(r)
	}
	return glyph.advance, true
}
