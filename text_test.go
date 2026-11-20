package zectrix

import (
	"crypto/sha256"
	"fmt"
	"math/bits"
	"testing"
)

func TestRenderTextMatchesBitmapLayout(t *testing.T) {
	font, err := LoadFont("")
	if err != nil {
		t.Fatal(err)
	}
	body := "道可道，非常道；名可名，非常名。\n上善若水，水善利万物而不争。\n知人者智，自知者明。千里之行，始于足下。\n\n设备在线，电量 73%，Wi-Fi 信号良好。\n更新时间 17:40，下一次刷新预计在 15 分钟后。"
	page, err := RenderText(font, "设备状态通知", body)
	if err != nil {
		t.Fatal(err)
	}
	packed, err := PackMono(fitImage(page))
	if err != nil {
		t.Fatal(err)
	}
	// The golden image covers the inverse title bar and body spacing.
	const want = "17a8263c39c903dcf3c2bb79f04230b44cb352729b5ea1df2787ccdbc0952be2"
	if got := fmt.Sprintf("%x", sha256.Sum256(packed)); got != want {
		t.Fatalf("1bpp image hash = %s, want %s", got, want)
	}
}

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

func TestRenderTextWithOptions(t *testing.T) {
	font, err := LoadFont("")
	if err != nil {
		t.Fatal(err)
	}
	page, err := RenderTextWithOptions(font, "标题", "正文", TextOptions{TitleSize: 20, BodySize: 14})
	if err != nil {
		t.Fatal(err)
	}
	if page.Bounds().Dx() != Width || page.Bounds().Dy() != Height {
		t.Fatalf("size %v", page.Bounds())
	}
	if _, err := RenderTextWithOptions(font, "标题", "正文", TextOptions{TitleSize: 7, BodySize: 14}); err == nil {
		t.Fatal("invalid title size accepted")
	}
}
