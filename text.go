package zectrix

import (
	"image"

	"github.com/athurg/zectrix/textimg"
)

type Font = textimg.Font
type TextOptions = textimg.Options

const (
	DefaultTitleSize = textimg.DefaultTitleSize
	DefaultBodySize  = textimg.DefaultBodySize
	MinFontSize      = textimg.MinFontSize
	MaxFontSize      = textimg.MaxFontSize
)

// LoadFont loads a Chinese font, using embedded Zfull-GB when path is empty.
func LoadFont(path string) (*textimg.Font, error) {
	return textimg.LoadFont(path)
}

// RenderText renders a title and UTF-8 body onto a 400×300 grayscale page.
func RenderText(font *textimg.Font, title, body string) (*image.Gray, error) {
	return textimg.Render(font, title, body, "")
}

// RenderTextWithOptions renders text with explicit title and body sizes.
func RenderTextWithOptions(font *textimg.Font, title, body string, options TextOptions) (*image.Gray, error) {
	return textimg.RenderWithOptions(font, title, body, "", options)
}
