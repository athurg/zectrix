package textimg

import (
	"strings"
	"testing"

	"golang.org/x/image/font/sfnt"
)

func TestRenderFits(t *testing.T) {
	f, err := LoadFont("")
	if err != nil {
		t.Fatal(err)
	}
	img, err := Render(f, "标题", strings.Repeat("上善若水，水善利万物而不争。", 12), "")
	if err != nil {
		t.Fatal(err)
	}
	if b := img.Bounds(); b.Dx() != Width || b.Dy() != Height {
		t.Fatalf("size %v", b)
	}
	black := 0
	for _, v := range img.Pix {
		if v == 0 {
			black++
		}
	}
	if black == 0 {
		t.Fatal("nothing drawn")
	}
	if _, err := Render(f, "", strings.Repeat("字", 5000), ""); err == nil {
		t.Fatal("overflowing text accepted")
	}
}

func TestRenderFitsTwelveLinesWithTitle(t *testing.T) {
	f, err := LoadFont("")
	if err != nil {
		t.Fatal(err)
	}
	body := "一二三四五六七八九，一二三四五六七八九，一二三四五六\n二\n三\n四\n五\n六\n七\n八\n九\n十\n一\n十二"
	img, err := Render(f, "通知", body, "")
	if err != nil {
		t.Fatal(err)
	}
	bottom := Height - margin
	for _, pixel := range img.Pix[bottom*Width:] {
		if pixel != 255 {
			t.Fatal("body overlaps the bottom margin")
		}
	}
}

func TestInverseBarsAndMaximumBodyLines(t *testing.T) {
	f, err := LoadFont("")
	if err != nil {
		t.Fatal(err)
	}
	body := "一二三四五六七八九，一二三四五六七八九，一二三四五六\n二\n三\n四\n五\n六\n七\n八\n九\n十\n十一\n十二\n十三\n十四"
	options := Options{TitleSize: 16, BodySize: 14}
	img, err := RenderWithOptions(f, "通知", body, "电量 73% · 17:40 更新", options)
	if err != nil {
		t.Fatal(err)
	}
	if img.GrayAt(0, 0).Y != 0 || img.GrayAt(0, Height-1).Y != 0 {
		t.Fatal("title and footer bars are not full-width black")
	}
	footerTop := Height - 1
	for footerTop > 0 && img.GrayAt(0, footerTop).Y == 0 {
		footerTop--
	}
	if got := Height - footerTop - 1; got != 20 {
		t.Fatalf("footer height = %d, want 20", got)
	}
	for y := footerTop + 1; y < Height; y++ {
		for x := Width - margin; x < Width; x++ {
			if img.GrayAt(x, y).Y != 0 {
				t.Fatalf("footer text enters the right margin at %d,%d", x, y)
			}
		}
	}
	if _, err := RenderWithOptions(f, "通知", body+"\n十五", "电量 73% · 17:40 更新", options); err == nil {
		t.Fatal("fifteen body lines accepted with title and footer")
	}
}

func TestDefaultFontIsEmbedded(t *testing.T) {
	embedded, err := LoadFont("")
	if err != nil {
		t.Fatal(err)
	}
	explicit, err := LoadFont("../assets/Zfull-GB.ttf")
	if err != nil {
		t.Fatal(err)
	}
	embeddedName, _ := embedded.Name(nil, sfnt.NameIDFull)
	explicitName, _ := explicit.Name(nil, sfnt.NameIDFull)
	if embeddedName == "" || embeddedName != explicitName {
		t.Fatalf("embedded font %q does not match explicit font %q", embeddedName, explicitName)
	}
}

func TestLoadFontErrors(t *testing.T) {
	if _, err := LoadFont("/nonexistent.ttf"); err == nil {
		t.Fatal("missing font accepted")
	}
	if _, err := loadSpec(FontSpec{"../assets/Zfull-GB.ttf", "No Such Face"}); err == nil {
		t.Fatal("missing face accepted")
	}
}

func TestFooterDrawnAtBottom(t *testing.T) {
	f, err := LoadFont("")
	if err != nil {
		t.Fatal(err)
	}
	body := "知人者智，自知者明。"
	plain, err := Render(f, "标题", body, "")
	if err != nil {
		t.Fatal(err)
	}
	withFooter, err := Render(f, "标题", body, "电量 73% · Wi-Fi -61 dBm（良好） · 17:40 更新")
	if err != nil {
		t.Fatal(err)
	}
	count := func(pix []uint8, fromRow int) (n int) {
		for _, v := range pix[fromRow*Width:] {
			if v == 0 {
				n++
			}
		}
		return
	}
	if count(plain.Pix, Height-30) != 0 {
		t.Fatal("short body reached the footer area")
	}
	if count(withFooter.Pix, Height-30) == 0 {
		t.Fatal("footer not drawn")
	}
	if _, err := Render(f, "", body, strings.Repeat("很长的状态", 20)); err == nil {
		t.Fatal("overlong footer accepted")
	}
}
