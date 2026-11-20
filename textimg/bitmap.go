package textimg

import (
	"encoding/binary"
	"image"
	"sort"

	"golang.org/x/image/font/sfnt"
	"golang.org/x/image/math/fixed"
)

// EBLC locates embedded bitmap strikes. EBDT holds their glyph pixels.
type bitmapFont struct {
	locations, data []byte
}

func readBitmapFont(source []byte, faceIndex int) bitmapFont {
	if len(source) < 12 {
		return bitmapFont{}
	}
	var directory uint64
	if string(source[:4]) == "ttcf" {
		offset := tableBytes(source, 12+4*uint64(faceIndex), 4)
		if offset == nil {
			return bitmapFont{}
		}
		directory = uint64(binary.BigEndian.Uint32(offset))
	}
	header := tableBytes(source, directory, 12)
	if header == nil {
		return bitmapFont{}
	}
	count := uint64(binary.BigEndian.Uint16(header[4:]))
	records := tableBytes(source, directory+12, 16*count)
	var result bitmapFont
	for len(records) >= 16 {
		offset := uint64(binary.BigEndian.Uint32(records[8:]))
		length := uint64(binary.BigEndian.Uint32(records[12:]))
		switch string(records[:4]) {
		case "EBLC":
			result.locations = tableBytes(source, offset, length)
		case "EBDT":
			result.data = tableBytes(source, offset, length)
		}
		records = records[16:]
	}
	if len(result.locations) < 8 || len(result.data) < 4 ||
		binary.BigEndian.Uint32(result.locations) != 0x00020000 ||
		binary.BigEndian.Uint32(result.data) != 0x00020000 {
		return bitmapFont{}
	}
	return result
}

func tableBytes(data []byte, offset, length uint64) []byte {
	if offset > uint64(len(data)) || length > uint64(len(data))-offset {
		return nil
	}
	return data[offset : offset+length]
}

type bitmapStrike struct {
	index, records, data []byte
	depth                int
}

func (f bitmapFont) strike(size int) bitmapStrike {
	if len(f.locations) == 0 {
		return bitmapStrike{}
	}
	count := uint64(binary.BigEndian.Uint32(f.locations[4:]))
	sizes := tableBytes(f.locations, 8, 48*count)
	for len(sizes) >= 48 {
		record := sizes[:48]
		sizes = sizes[48:]
		if int(record[44]) != size || int(record[45]) != size || record[47]&1 == 0 {
			continue
		}
		depth := int(record[46])
		if depth != 1 && depth != 2 && depth != 4 && depth != 8 {
			continue
		}
		offset := uint64(binary.BigEndian.Uint32(record))
		length := uint64(binary.BigEndian.Uint32(record[4:]))
		index := tableBytes(f.locations, offset, length)
		records := tableBytes(index, 0, 8*uint64(binary.BigEndian.Uint32(record[8:])))
		if len(records) > 0 {
			return bitmapStrike{index: index, records: records, data: f.data, depth: depth}
		}
	}
	return bitmapStrike{}
}

type bitmapGlyph struct {
	pixels         []byte
	width, height  int
	left, top      int
	depth, rowBits int
	advance        fixed.Int26_6
}

func (s bitmapStrike) glyph(id sfnt.GlyphIndex) (bitmapGlyph, bool) {
	count := len(s.records) / 8
	i := sort.Search(count, func(i int) bool {
		return binary.BigEndian.Uint16(s.records[8*i+2:]) >= uint16(id)
	})
	if i == count {
		return bitmapGlyph{}, false
	}
	record := s.records[8*i:]
	first := binary.BigEndian.Uint16(record)
	offset := uint64(binary.BigEndian.Uint32(record[4:]))
	if uint16(id) < first || offset < uint64(len(s.records)) || offset+8 > uint64(len(s.index)) {
		return bitmapGlyph{}, false
	}
	subtable := s.index[offset:]
	imageFormat := binary.BigEndian.Uint16(subtable[2:])
	base := uint64(binary.BigEndian.Uint32(subtable[4:]))
	pixels, metrics := bitmapImage(s.data, subtable, base, uint16(id), uint64(uint16(id)-first))
	var metricBytes int
	switch imageFormat {
	case 1, 2:
		metricBytes = 5
	case 5:
		if len(metrics) < 8 {
			return bitmapGlyph{}, false
		}
	case 6, 7:
		metricBytes = 8
	default:
		return bitmapGlyph{}, false
	}
	if metricBytes > 0 {
		if len(pixels) < metricBytes {
			return bitmapGlyph{}, false
		}
		metrics, pixels = pixels[:metricBytes], pixels[metricBytes:]
	}
	width, height := int(metrics[1]), int(metrics[0])
	rowBits := width * s.depth
	if imageFormat == 1 || imageFormat == 6 {
		rowBits = (rowBits + 7) &^ 7
	}
	if len(pixels) < (rowBits*height+7)/8 {
		return bitmapGlyph{}, false
	}
	return bitmapGlyph{
		pixels: pixels, width: width, height: height, depth: s.depth, rowBits: rowBits,
		left: int(int8(metrics[2])), top: int(int8(metrics[3])), advance: fixed.I(int(metrics[4])),
	}, true
}

// bitmapImage resolves the range's relative offsets or common image size.
func bitmapImage(data, subtable []byte, base uint64, id uint16, index uint64) ([]byte, []byte) {
	var start, end uint64
	switch binary.BigEndian.Uint16(subtable) {
	case 1:
		pair := tableBytes(subtable, 8+4*index, 8)
		if pair == nil {
			return nil, nil
		}
		start, end = uint64(binary.BigEndian.Uint32(pair)), uint64(binary.BigEndian.Uint32(pair[4:]))
	case 2:
		return fixedBitmapImage(data, subtable, base, index)
	case 3:
		pair := tableBytes(subtable, 8+2*index, 4)
		if pair == nil {
			return nil, nil
		}
		start, end = uint64(binary.BigEndian.Uint16(pair)), uint64(binary.BigEndian.Uint16(pair[2:]))
	case 4:
		if len(subtable) < 12 {
			return nil, nil
		}
		count := uint64(binary.BigEndian.Uint32(subtable[8:]))
		pairs := tableBytes(subtable, 12, 4*(count+1))
		if pairs == nil {
			return nil, nil
		}
		i := findBitmapID(pairs[:4*count], 4, id)
		if i < 0 {
			return nil, nil
		}
		start = uint64(binary.BigEndian.Uint16(pairs[i+2:]))
		end = uint64(binary.BigEndian.Uint16(pairs[i+6:]))
	case 5:
		if len(subtable) < 24 {
			return nil, nil
		}
		ids := tableBytes(subtable, 24, 2*uint64(binary.BigEndian.Uint32(subtable[20:])))
		i := findBitmapID(ids, 2, id)
		if i < 0 {
			return nil, nil
		}
		return fixedBitmapImage(data, subtable, base, uint64(i/2))
	}
	if end <= start {
		return nil, nil
	}
	return tableBytes(data, base+start, end-start), nil
}

func fixedBitmapImage(data, subtable []byte, base, index uint64) ([]byte, []byte) {
	if len(subtable) < 20 {
		return nil, nil
	}
	length := uint64(binary.BigEndian.Uint32(subtable[8:]))
	pixels := tableBytes(data, base+index*length, length)
	if pixels == nil {
		return nil, nil
	}
	return pixels, subtable[12:20]
}

func findBitmapID(ids []byte, stride int, id uint16) int {
	count := len(ids) / stride
	i := sort.Search(count, func(i int) bool {
		return binary.BigEndian.Uint16(ids[i*stride:]) >= id
	})
	if i == count || binary.BigEndian.Uint16(ids[i*stride:]) != id {
		return -1
	}
	return i * stride
}

func (g bitmapGlyph) mask() *image.Alpha {
	mask := image.NewAlpha(image.Rect(0, 0, g.width, g.height))
	max := (1 << g.depth) - 1
	for y := 0; y < g.height; y++ {
		for x := 0; x < g.width; x++ {
			bit := y*g.rowBits + x*g.depth
			value := (int(g.pixels[bit/8]) >> (8 - g.depth - bit%8)) & max
			mask.Pix[y*mask.Stride+x] = uint8(value * 255 / max)
		}
	}
	return mask
}
