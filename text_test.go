package zectrix

import (
	"math/bits"
	"testing"
)

func TestRenderTextThroughImageFitHasInk(t *testing.T) {
	font, err := LoadFont("")
	if err != nil {
		t.Fatal(err)
	}
	page, err := RenderText(font, "标题", "这段文字应该正常显示。")
	if err != nil {
		t.Fatal(err)
	}
	packed, err := PackMono(fitImage(page))
	if err != nil {
		t.Fatal(err)
	}
	black := 0
	for _, b := range packed {
		black += bits.OnesCount8(b)
	}
	if black == 0 {
		t.Fatal("rendered text became an all-white image")
	}
}
