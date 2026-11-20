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
