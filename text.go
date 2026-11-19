package zectrix

import (
	"image"

	"github.com/itopinion/zectrix-local-push-sdk-golang/textimg"
	"golang.org/x/image/font/opentype"
)

// LoadFont loads a Chinese font, using the system defaults when path is empty.
func LoadFont(path string) (*opentype.Font, error) {
	return textimg.LoadFont(path)
}

// RenderText renders a title and UTF-8 body onto a 400×300 black/white page.
func RenderText(font *opentype.Font, title, body string) (*image.Gray, error) {
	return textimg.Render(font, title, body, "")
}
