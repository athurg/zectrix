package zectrix

import (
	"image"
	"image/color"
	"math/bits"
	"testing"
)

// testPattern: noisy enough that the zlib stream spans several 1024-byte chunks.
func testPattern() *image.Gray {
	img := image.NewGray(image.Rect(0, 0, Width, Height))
	seed := uint32(1)
	for i := range img.Pix {
		seed = seed*1664525 + 1013904223
		img.Pix[i] = uint8(seed >> 24)
	}
	return img
}

func TestPackMonoLayout(t *testing.T) {
	img := image.NewGray(image.Rect(0, 0, Width, Height))
	for i := range img.Pix {
		img.Pix[i] = 255
	}
	pts := [][2]int{{0, 0}, {399, 0}, {0, 299}, {399, 299}, {9, 7}}
	for _, p := range pts {
		img.SetGray(p[0], p[1], color.Gray{0})
	}
	raw, err := PackMono(img)
	if err != nil {
		t.Fatal(err)
	}
	if len(raw) != 15000 {
		t.Fatalf("len %d", len(raw))
	}
	want := make([]byte, 15000)
	for _, p := range pts {
		want[p[1]*50+p[0]/8] |= 0x80 >> (p[0] % 8)
	}
	for i := range want {
		if raw[i] != want[i] {
			t.Fatalf("byte %d = %08b, want %08b", i, raw[i], want[i])
		}
	}
}

func TestPackMonoTransparentIsWhiteAndGrayDithers(t *testing.T) {
	clear := image.NewNRGBA(image.Rect(0, 0, Width, Height)) // all alpha 0
	raw, _ := PackMono(clear)
	for _, b := range raw {
		if b != 0 {
			t.Fatal("transparent pixels must be white (bit 0)")
		}
	}
	gray := image.NewGray(image.Rect(0, 0, Width, Height))
	for i := range gray.Pix {
		gray.Pix[i] = 188 // ≈ 50% linear luminance
	}
	raw, _ = PackMono(gray)
	black := 0
	for _, b := range raw {
		black += bits.OnesCount8(b)
	}
	if frac := float64(black) / (Width * Height); frac < 0.4 || frac > 0.6 {
		t.Fatalf("black fraction %.2f, want about 0.5", frac)
	}
	if _, err := PackMono(image.NewGray(image.Rect(0, 0, 10, 10))); err == nil {
		t.Fatal("wrong size accepted")
	}
}

func TestCompressedPatternNeedsSeveralChunks(t *testing.T) {
	raw, _ := PackMono(testPattern())
	z, err := compress(raw)
	if err != nil {
		t.Fatal(err)
	}
	if len(z) <= chunkSize {
		t.Fatalf("pattern compresses to %d bytes; want multi-chunk", len(z))
	}
}

func TestFitImageScalesAndCentersOnWhite(t *testing.T) {
	img := image.NewNRGBA(image.Rect(0, 0, 800, 800))
	for i := 0; i < len(img.Pix); i += 4 {
		img.Pix[i+3] = 255
	}
	got := fitImage(img)
	if got.Bounds().Dx() != Width || got.Bounds().Dy() != Height {
		t.Fatalf("bounds %v", got.Bounds())
	}
	if c := got.NRGBAAt(0, 0); c.R != 255 || c.G != 255 || c.B != 255 || c.A != 255 {
		t.Fatalf("corner is not white: %+v", c)
	}
	if c := got.NRGBAAt(Width/2, Height/2); c.R != 0 || c.G != 0 || c.B != 0 || c.A != 255 {
		t.Fatalf("center lost source image: %+v", c)
	}
}
