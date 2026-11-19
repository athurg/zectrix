// Package textimg renders a title and CJK body text into a 400×300 black/white
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

// DefaultFonts are macOS Chinese fonts that golang.org/x/image/font/sfnt can
// parse (STHeiti's cmap is not supported), in order of preference. PingFang
// is an on-demand system asset, so its directory name is a hash.
var DefaultFonts = []FontSpec{
	{"/System/Library/AssetsV2/com_apple_MobileAsset_Font*/*/AssetData/PingFang.ttc", "PingFang SC Medium"},
	{"/System/Library/Fonts/Hiragino Sans GB.ttc", "Hiragino Sans GB W6"},
	{"/System/Library/Fonts/Supplemental/Arial Unicode.ttf", ""},
}

// LoadFont loads path, or the first usable DefaultFonts entry when path is
// empty. For a collection (.ttc) given by path, the first Chinese face is used.
func LoadFont(path string) (*opentype.Font, error) {
	specs := DefaultFonts
	if path != "" {
		specs = []FontSpec{{Path: path}}
	}
	var errs []error
	for _, s := range specs {
		f, err := loadSpec(s)
		if err == nil {
			return f, nil
		}
		errs = append(errs, fmt.Errorf("%s: %w", s.Path, err))
	}
	return nil, fmt.Errorf("textimg: no usable Chinese font: %w", errors.Join(errs...))
}

func loadSpec(s FontSpec) (*opentype.Font, error) {
	path := s.Path
	if m, _ := filepath.Glob(path); len(m) > 0 {
		path = m[0]
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	c, err := opentype.ParseCollection(data) // also accepts a single font
	if err != nil {
		return nil, err
	}
	for i := 0; i < c.NumFonts(); i++ {
		f, err := c.Font(i)
		if err != nil {
			continue
		}
		if s.Face != "" {
			if name, _ := f.Name(nil, sfnt.NameIDFull); name != s.Face {
				continue
			}
		}
		// Parsing succeeds lazily; make sure the face really maps Chinese glyphs.
		if hasChinese(f) {
			return f, nil
		}
	}
	if s.Face != "" {
		return nil, fmt.Errorf("no usable face %q", s.Face)
	}
	return nil, errors.New("font has no Chinese glyphs")
}

func hasChinese(f *opentype.Font) bool {
	fc, err := face(f, 16)
	if err != nil {
		return false
	}
	defer fc.Close()
	_, ok := fc.GlyphAdvance('道')
	return ok
}

// Closing punctuation may hang past the right margin instead of starting a line.
const noLineStart = "，。、；：！？）」』”’》〉…"

func face(f *opentype.Font, size float64) (font.Face, error) {
	return opentype.NewFace(f, &opentype.FaceOptions{Size: size, DPI: 72, Hinting: font.HintingFull})
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

// footerSize is the pixel size of the footer line.
const footerSize = 14

// Render draws title (centred, with a rule below), body, and an optional
// one-line footer at the bottom (with a rule above), picking the largest body
// size from 24 down to 12 px that fits. The result is pure black/white:
// anti-aliased edges are snapped so the 1bpp screen stays crisp.
func Render(f *opentype.Font, title, body, footer string) (*image.Gray, error) {
	img := image.NewGray(image.Rect(0, 0, Width, Height))
	draw.Draw(img, img.Bounds(), image.White, image.Point{}, draw.Src)

	top := margin
	if title != "" {
		tf, err := face(f, 26)
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
	for size := 24.0; size >= 12; size-- {
		bf, err := face(f, size)
		if err != nil {
			return nil, err
		}
		lineH := int(size*1.5 + 0.5)
		lines := wrap(bf, body, fixed.I(Width-2*margin))
		if len(lines)*lineH > avail {
			bf.Close()
			if size > 12 {
				continue
			}
			return nil, errors.New("textimg: text does not fit on 400x300 even at 12px")
		}
		dr := &font.Drawer{Dst: img, Src: image.Black, Face: bf}
		asc := bf.Metrics().Ascent
		for i, l := range lines {
			dr.Dot = fixed.Point26_6{X: fixed.I(margin), Y: fixed.I(top+i*lineH) + asc}
			dr.DrawString(l)
		}
		bf.Close()
		break
	}

	for i, v := range img.Pix {
		if v < 128 {
			img.Pix[i] = 0
		} else {
			img.Pix[i] = 255
		}
	}
	return img, nil
}
