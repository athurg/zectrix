package textimg

/*
#cgo pkg-config: freetype2
#include <stdlib.h>
#include <string.h>
#include <ft2build.h>
#include FT_FREETYPE_H

typedef struct {
	FT_Library library;
	unsigned char *data;
	long size;
} ZFont;

typedef struct {
	int width;
	int rows;
	int left;
	int top;
	int advance;
} ZGlyph;

static ZFont *zfont_new(const void *input, long size, int *err) {
	ZFont *font = calloc(1, sizeof(ZFont));
	if (!font) return NULL;
	font->data = malloc(size);
	font->size = size;
	if (!font->data) {
		free(font);
		return NULL;
	}
	memcpy(font->data, input, size);
	*err = FT_Init_FreeType(&font->library);
	if (*err) {
		free(font->data);
		free(font);
		return NULL;
	}
	FT_Face face;
	*err = FT_New_Memory_Face(font->library, font->data, size, 0, &face);
	if (!*err) FT_Done_Face(face);
	if (*err) {
		FT_Done_FreeType(font->library);
		free(font->data);
		free(font);
		return NULL;
	}
	return font;
}

static void zfont_free(ZFont *font) {
	if (!font) return;
	FT_Done_FreeType(font->library);
	free(font->data);
	free(font);
}

static FT_Face zfont_face_new(ZFont *font, int size, int *err) {
	FT_Face face;
	*err = FT_New_Memory_Face(font->library, font->data, font->size, 0, &face);
	if (*err) return NULL;
	*err = FT_Set_Pixel_Sizes(face, 0, size);
	if (*err) {
		FT_Done_Face(face);
		return NULL;
	}
	return face;
}

static int zfont_glyph(FT_Face face, unsigned long ch, ZGlyph *glyph) {
	int err = FT_Load_Char(face, ch, FT_LOAD_RENDER | FT_LOAD_TARGET_NORMAL);
	if (err) return err;
	FT_GlyphSlot slot = face->glyph;
	glyph->width = slot->bitmap.width;
	glyph->rows = slot->bitmap.rows;
	glyph->left = slot->bitmap_left;
	glyph->top = slot->bitmap_top;
	glyph->advance = slot->advance.x;
	return 0;
}

static void zfont_copy_glyph(FT_Face face, unsigned char *dst) {
	FT_Bitmap bitmap = face->glyph->bitmap;
	for (unsigned int y = 0; y < bitmap.rows; y++) {
		const unsigned char *row;
		if (bitmap.pitch >= 0) {
			row = bitmap.buffer + y * bitmap.pitch;
		} else {
			row = bitmap.buffer + (bitmap.rows - 1 - y) * (-bitmap.pitch);
		}
		for (unsigned int x = 0; x < bitmap.width; x++) {
			unsigned char value = 0;
			if (bitmap.pixel_mode == FT_PIXEL_MODE_MONO) {
				value = (row[x >> 3] & (0x80 >> (x & 7))) ? 255 : 0;
			} else if (bitmap.pixel_mode == FT_PIXEL_MODE_GRAY) {
				value = row[x];
			}
			dst[y * bitmap.width + x] = value;
		}
	}
}

static int zfont_ascent(FT_Face face) { return face->size->metrics.ascender; }
static int zfont_descent(FT_Face face) { return -face->size->metrics.descender; }
static int zfont_height(FT_Face face) { return face->size->metrics.height; }

static int zfont_kern(FT_Face face, unsigned int left, unsigned int right) {
	FT_Vector delta;
	if (FT_Get_Kerning(face, left, right, FT_KERNING_DEFAULT, &delta)) return 0;
	return delta.x;
}
*/
import "C"

import (
	"errors"
	"image"
	"runtime"
	"sync"
	"unsafe"

	"golang.org/x/image/font"
	"golang.org/x/image/font/opentype"
	"golang.org/x/image/math/fixed"
)

type Font struct {
	*opentype.Font
	native *C.ZFont
}

func newFont(data []byte, outline *opentype.Font) (*Font, error) {
	if len(data) == 0 {
		return nil, errors.New("textimg: empty font")
	}
	var code C.int
	native := C.zfont_new(unsafe.Pointer(&data[0]), C.long(len(data)), &code)
	if native == nil {
		return nil, errors.New("textimg: FreeType cannot load font")
	}
	f := &Font{Font: outline, native: native}
	runtime.SetFinalizer(f, func(f *Font) {
		C.zfont_free(f.native)
	})
	return f, nil
}

type freeTypeFace struct {
	face C.FT_Face
	mu   sync.Mutex
}

func newFreeTypeFace(f *Font, size int) (*freeTypeFace, error) {
	var code C.int
	face := C.zfont_face_new(f.native, C.int(size), &code)
	if face == nil {
		return nil, errors.New("textimg: FreeType cannot set font size")
	}
	return &freeTypeFace{face: face}, nil
}

func (f *freeTypeFace) Close() error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.face != nil {
		C.FT_Done_Face(f.face)
		f.face = nil
	}
	return nil
}

func (f *freeTypeFace) Glyph(dot fixed.Point26_6, r rune) (image.Rectangle, image.Image, image.Point, fixed.Int26_6, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var glyph C.ZGlyph
	if C.zfont_glyph(f.face, C.ulong(r), &glyph) != 0 {
		return image.Rectangle{}, image.NewAlpha(image.Rectangle{}), image.Point{}, 0, false
	}
	w, h := int(glyph.width), int(glyph.rows)
	alpha := image.NewAlpha(image.Rect(0, 0, w, h))
	if w > 0 && h > 0 {
		C.zfont_copy_glyph(f.face, (*C.uchar)(unsafe.Pointer(&alpha.Pix[0])))
	}
	min := image.Pt(dot.X.Round()+int(glyph.left), dot.Y.Round()-int(glyph.top))
	dr := image.Rectangle{Min: min, Max: min.Add(image.Pt(w, h))}
	return dr, alpha, image.Point{}, fixed.Int26_6(glyph.advance), true
}

func (f *freeTypeFace) GlyphBounds(r rune) (fixed.Rectangle26_6, fixed.Int26_6, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var glyph C.ZGlyph
	if C.zfont_glyph(f.face, C.ulong(r), &glyph) != 0 {
		return fixed.Rectangle26_6{}, 0, false
	}
	bounds := fixed.Rectangle26_6{
		Min: fixed.Point26_6{X: fixed.I(int(glyph.left)), Y: fixed.I(-int(glyph.top))},
		Max: fixed.Point26_6{X: fixed.I(int(glyph.left + glyph.width)), Y: fixed.I(int(glyph.rows - glyph.top))},
	}
	return bounds, fixed.Int26_6(glyph.advance), true
}

func (f *freeTypeFace) GlyphAdvance(r rune) (fixed.Int26_6, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var glyph C.ZGlyph
	if C.zfont_glyph(f.face, C.ulong(r), &glyph) != 0 {
		return 0, false
	}
	return fixed.Int26_6(glyph.advance), true
}

func (f *freeTypeFace) Kern(r0, r1 rune) fixed.Int26_6 {
	f.mu.Lock()
	defer f.mu.Unlock()
	left := C.FT_Get_Char_Index(f.face, C.ulong(r0))
	right := C.FT_Get_Char_Index(f.face, C.ulong(r1))
	return fixed.Int26_6(C.zfont_kern(f.face, left, right))
}

func (f *freeTypeFace) Metrics() font.Metrics {
	f.mu.Lock()
	defer f.mu.Unlock()
	ascent := fixed.Int26_6(C.zfont_ascent(f.face))
	descent := fixed.Int26_6(C.zfont_descent(f.face))
	height := fixed.Int26_6(C.zfont_height(f.face))
	return font.Metrics{
		Ascent:  ascent,
		Descent: descent,
		Height:  height,
	}
}
