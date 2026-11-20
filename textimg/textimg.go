// Package textimg renders a title and CJK body text into a 400×300 grayscale
// image for the Note 4.
package textimg

import (
	"errors"
	"fmt"
	"image"
	"image/draw"
	"os"
	"path/filepath"
	"strings"

	"github.com/athurg/zectrix/assets"
	"golang.org/x/image/font"
	"golang.org/x/image/font/opentype"
	"golang.org/x/image/font/sfnt"
	"golang.org/x/image/math/fixed"
)

const (
	Width  = 400
	Height = 300
	margin = 16
)

// FontSpec names a font file (a glob pattern is allowed) and, for
// collections, the full name of the face to use. An empty Face picks the
// first face that maps Chinese glyphs.
type FontSpec struct{ Path, Face string }

// LoadFont loads the embedded Zfull-GB font when path is empty, or path
// otherwise. For a collection (.ttc), the first Chinese face is used.
func LoadFont(path string) (*Font, error) {
	if path == "" {
		return parseFont(assets.ZfullGB, "")
	}
	f, err := loadSpec(FontSpec{Path: path})
	if err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	return f, nil
}

func loadSpec(s FontSpec) (*Font, error) {
	path := s.Path
	if m, _ := filepath.Glob(path); len(m) > 0 {
		path = m[0]
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return parseFont(data, s.Face)
}

func parseFont(data []byte, faceName string) (*Font, error) {
	c, err := opentype.ParseCollection(data) // also accepts a single font
	if err != nil {
		return nil, err
	}
	for i := 0; i < c.NumFonts(); i++ {
		f, err := c.Font(i)
		if err != nil {
			continue
		}
		if faceName != "" {
			if name, _ := f.Name(nil, sfnt.NameIDFull); name != faceName {
				continue
			}
		}
		// Parsing succeeds lazily; make sure the face really maps Chinese glyphs.
		if hasChinese(f) {
			return newFont(data, f)
		}
	}
	if faceName != "" {
		return nil, fmt.Errorf("no usable face %q", faceName)
	}
	return nil, errors.New("font has no Chinese glyphs")
}

func hasChinese(f *opentype.Font) bool {
	fc, err := opentype.NewFace(f, &opentype.FaceOptions{Size: 16, DPI: 72, Hinting: font.HintingFull})
	if err != nil {
		return false
	}
	defer fc.Close()
	_, ok := fc.GlyphAdvance('道')
	return ok
}

// Closing punctuation may hang past the right margin instead of starting a line.
const noLineStart = "，。、；：！？）」』”’》〉…"

func face(f *Font, size int) (font.Face, error) {
	return newFreeTypeFace(f, size)
}

// wrap breaks paragraphs (split on '\n') into lines no wider than max.
func wrap(fc font.Face, text string, max fixed.Int26_6) []string {
	var lines []string
	for _, para := range strings.Split(text, "\n") {
		var line []rune
		var w fixed.Int26_6
		for _, r := range para {
			adv, _ := fc.GlyphAdvance(r)
			if len(line) > 0 && w+adv > max && !strings.ContainsRune(noLineStart, r) {
				lines = append(lines, string(line))
				line, w = nil, 0
			}
			line = append(line, r)
			w += adv
		}
		lines = append(lines, string(line))
	}
	return lines
}

const (
	DefaultTitleSize = 16
	DefaultBodySize  = 14
	MinFontSize      = 8
	MaxFontSize      = 48
	footerSize       = 14
)

type Options struct {
	TitleSize int
	BodySize  int
}

// Render uses the default 16px title and 14px body sizes.
func Render(f *Font, title, body, footer string) (*image.Gray, error) {
	return RenderWithOptions(f, title, body, footer, Options{
		TitleSize: DefaultTitleSize,
		BodySize:  DefaultBodySize,
	})
}

// RenderWithOptions draws text using FreeType and rejects text that does not
// fit. Grayscale glyph edges are preserved for PackMono's 1bpp conversion.
func RenderWithOptions(f *Font, title, body, footer string, options Options) (*image.Gray, error) {
	if options.TitleSize < MinFontSize || options.TitleSize > MaxFontSize ||
		options.BodySize < MinFontSize || options.BodySize > MaxFontSize {
		return nil, fmt.Errorf("textimg: font sizes must be %d..%d", MinFontSize, MaxFontSize)
	}
	img := image.NewGray(image.Rect(0, 0, Width, Height))
	draw.Draw(img, img.Bounds(), image.White, image.Point{}, draw.Src)

	top := margin
	if title != "" {
		tf, err := face(f, options.TitleSize)
		if err != nil {
			return nil, err
		}
		dr := &font.Drawer{Dst: img, Src: image.Black, Face: tf}
		w := dr.MeasureString(title)
		asc := tf.Metrics().Ascent
		dr.Dot = fixed.Point26_6{X: (fixed.I(Width) - w) / 2, Y: fixed.I(top) + asc}
		dr.DrawString(title)
		top += (asc + tf.Metrics().Descent).Ceil() + 6
		draw.Draw(img, image.Rect(margin, top, Width-margin, top+2), image.Black, image.Point{}, draw.Src)
		top += 10
		tf.Close()
	}

	bottom := Height - margin
	if footer != "" {
		ff, err := face(f, footerSize)
		if err != nil {
			return nil, err
		}
		dr := &font.Drawer{Dst: img, Src: image.Black, Face: ff}
		if dr.MeasureString(footer) > fixed.I(Width-2*margin) {
			ff.Close()
			return nil, errors.New("textimg: footer does not fit on one line")
		}
		m := ff.Metrics()
		bottom = Height - margin/2 - (m.Ascent + m.Descent).Ceil()
		dr.Dot = fixed.Point26_6{X: fixed.I(margin), Y: fixed.I(bottom) + m.Ascent}
		dr.DrawString(footer)
		ff.Close()
		draw.Draw(img, image.Rect(margin, bottom-5, Width-margin, bottom-4), image.Black, image.Point{}, draw.Src)
		bottom -= 8
	}

	avail := bottom - top
	bf, err := face(f, options.BodySize)
	if err != nil {
		return nil, err
	}
	lineH := options.BodySize * 3 / 2
	lines := wrap(bf, body, fixed.I(Width-2*margin))
	if len(lines)*lineH > avail {
		bf.Close()
		return nil, fmt.Errorf("textimg: text does not fit on 400x300 at %dpx", options.BodySize)
	}
	dr := &font.Drawer{Dst: img, Src: image.Black, Face: bf}
	asc := bf.Metrics().Ascent
	for i, l := range lines {
		dr.Dot = fixed.Point26_6{X: fixed.I(margin), Y: fixed.I(top+i*lineH) + asc}
		dr.DrawString(l)
	}
	bf.Close()

	return img, nil
}
