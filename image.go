package zectrix

import (
	"bytes"
	"compress/zlib"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"math"
	"time"

	xdraw "golang.org/x/image/draw"
)

const (
	Width       = 400
	Height      = 300
	chunkSize   = 1024
	monoRawSize = Width * Height / 8 // 15000

	imageReceiveDeadline = 30 * time.Second
	imageRefreshDeadline = 60 * time.Second
	imagePollInterval    = 350 * time.Millisecond
	imageChunkGap        = 2 * time.Millisecond
)

// Image states reported by IMAGE_STATUS / STATE.
const (
	imageReceiving  = 1
	imageRefreshing = 2
	imageDone       = 3
	imageFailed     = 4
)

// ErrImageFailed means the device rejected or could not display the image;
// the previous picture stays on screen.
var ErrImageFailed = errors.New("zectrix: device failed to display the image")

var srgbToLinear = func() (t [256]float64) {
	for i := range t {
		c := float64(i) / 255
		if c <= 0.04045 {
			t[i] = c / 12.92
		} else {
			t[i] = math.Pow((c+0.055)/1.055, 2.4)
		}
	}
	return
}()

// PackMono converts a 400×300 image to the 1bpp wire layout (rows of 50
// bytes, MSB = leftmost pixel, 1 = black). Pixels are treated as sRGB,
// converted to linear luminance, composited on white and Floyd–Steinberg
// dithered to black/white. Pure black/white input passes through unchanged.
func PackMono(img image.Image) ([]byte, error) {
	b := img.Bounds()
	if b.Dx() != Width || b.Dy() != Height {
		return nil, fmt.Errorf("zectrix: image must be %dx%d, got %dx%d", Width, Height, b.Dx(), b.Dy())
	}
	lum := make([]float64, Width*Height)
	for y := 0; y < Height; y++ {
		for x := 0; x < Width; x++ {
			c := color.NRGBAModel.Convert(img.At(b.Min.X+x, b.Min.Y+y)).(color.NRGBA)
			l := 255 * (0.2126*srgbToLinear[c.R] + 0.7152*srgbToLinear[c.G] + 0.0722*srgbToLinear[c.B])
			a := float64(c.A) / 255
			lum[y*Width+x] = l*a + 255*(1-a)
		}
	}
	raw := make([]byte, monoRawSize)
	for y := 0; y < Height; y++ {
		for x := 0; x < Width; x++ {
			i := y*Width + x
			v := math.Min(math.Max(lum[i], 0), 255)
			target, black := 255.0, false
			if v < 127.5 {
				target, black = 0, true
			}
			if black {
				raw[i/8] |= 0x80 >> (i % 8)
			}
			e := v - target
			if x+1 < Width {
				lum[i+1] += e * 7 / 16
			}
			if y+1 < Height {
				if x > 0 {
					lum[i+Width-1] += e * 3 / 16
				}
				lum[i+Width] += e * 5 / 16
				if x+1 < Width {
					lum[i+Width+1] += e / 16
				}
			}
		}
	}
	return raw, nil
}

func compress(raw []byte) ([]byte, error) {
	var buf bytes.Buffer
	w, err := zlib.NewWriterLevel(&buf, 6)
	if err != nil {
		return nil, err
	}
	if _, err := w.Write(raw); err != nil {
		return nil, err
	}
	if err := w.Close(); err != nil {
		return nil, err
	}
	if buf.Len() == 0 || buf.Len() > 65535 {
		return nil, fmt.Errorf("zectrix: compressed image is %d bytes, limit 65535", buf.Len())
	}
	return buf.Bytes(), nil
}

// ShowImage scales an image to fit the 400×300 screen, centers it on white,
// then displays it in black/white.
func (d *Device) ShowImage(ctx context.Context, img image.Image) error {
	if img == nil {
		return fmt.Errorf("zectrix: image is nil")
	}
	return d.ShowMono(ctx, fitImage(img))
}

func fitImage(img image.Image) *image.NRGBA {
	out := image.NewNRGBA(image.Rect(0, 0, Width, Height))
	draw.Draw(out, out.Bounds(), image.White, image.Point{}, draw.Src)
	bounds := img.Bounds()
	scale := min(float64(Width)/float64(bounds.Dx()), float64(Height)/float64(bounds.Dy()))
	w := max(1, int(float64(bounds.Dx())*scale+0.5))
	h := max(1, int(float64(bounds.Dy())*scale+0.5))
	dst := image.Rect((Width-w)/2, (Height-h)/2, (Width+w)/2, (Height+h)/2)
	xdraw.CatmullRom.Scale(out, dst, img, bounds, xdraw.Over, nil)
	return out
}

// ShowMono displays a 400×300 image in black/white and returns only after the
// device reports the refresh finished. Transfer completion is not success.
// ErrResultUnknown means it may or may not have been shown: do not resend
// automatically.
func (d *Device) ShowMono(ctx context.Context, img image.Image) error {
	raw, err := PackMono(img)
	if err != nil {
		return err
	}
	data, err := compress(raw)
	if err != nil {
		return err
	}
	return d.showPacked(ctx, 1, data)
}

func (d *Device) showPacked(ctx context.Context, bpp uint8, data []byte) error {
	d.imageMu.Lock()
	uploading := true
	defer func() {
		if uploading {
			d.imageMu.Unlock()
		}
	}()

	begin := []byte{bpp, 0, 0}
	binary.BigEndian.PutUint16(begin[1:], uint16(len(data)))
	d.cmdMu.Lock()
	id := next(&d.control)
	_, err := d.request(ctx, TypeImageBegin, id, begin)
	d.cmdMu.Unlock()
	if err != nil {
		return fmt.Errorf("zectrix: IMAGE_BEGIN: %w", err)
	}

	var chunks [][]byte
	for off := 0; off < len(data); off += chunkSize {
		chunks = append(chunks, data[off:min(off+chunkSize, len(data))])
	}
	all := ^uint64(0) >> (64 - len(chunks))
	idBytes := binary.BigEndian.AppendUint32(nil, id)
	var received uint64
	refreshing := false
	deadline := time.Now().Add(imageReceiveDeadline)

	for {
		if r, ok := d.result(1, id); ok {
			return resultToErr(r)
		}
		if !refreshing {
			for i, c := range chunks {
				if received&(1<<i) != 0 {
					continue
				}
				body := append(append([]byte(nil), idBytes...), c...)
				if _, err := d.udp.Write(Packet{Kind: TypeImageData, Conn: d.conn, Seq: uint32(i), Body: body}.Encode()); err != nil {
					return ErrDisconnected
				}
				time.Sleep(imageChunkGap)
			}
		}
		qctx, cancel := context.WithTimeout(ctx, 2*time.Second)
		p, err := d.request(qctx, TypeImageStatus, next(&d.query), idBytes)
		cancel()
		switch {
		case err == nil && len(p.Body) == 10:
			received |= binary.BigEndian.Uint64(p.Body[2:]) & all
			switch p.Body[1] {
			case imageDone:
				return nil
			case imageFailed:
				return ErrImageFailed
			case imageRefreshing:
				if !refreshing {
					// All chunks verified; let the next upload start while this one refreshes.
					refreshing = true
					uploading = false
					d.imageMu.Unlock()
					deadline = time.Now().Add(imageRefreshDeadline)
				}
			}
		case errors.Is(err, ErrDisconnected), errors.Is(err, ErrStale):
			return ErrDisconnected
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		if !time.Now().Before(deadline) {
			if r, ok := d.stateResult(ctx, 1, id); ok {
				return resultToErr(r)
			}
			return ErrResultUnknown
		}
		select {
		case <-time.After(imagePollInterval):
		case <-d.done:
			return ErrDisconnected
		case <-ctx.Done():
			return ctx.Err()
		}
	}
}

func resultToErr(reason uint8) error {
	if reason == 0 {
		return nil
	}
	return fmt.Errorf("%w (%s)", ErrImageFailed, CloseReason(reason))
}

func (d *Device) storeResult(object uint8, id uint32, reason uint8) {
	d.resMu.Lock()
	defer d.resMu.Unlock()
	k := resultKey{object, id}
	if _, ok := d.results[k]; !ok {
		d.resOrder = append(d.resOrder, k)
		if len(d.resOrder) > 16 {
			delete(d.results, d.resOrder[0])
			d.resOrder = d.resOrder[1:]
		}
	}
	d.results[k] = reason
}

func (d *Device) result(object uint8, id uint32) (uint8, bool) {
	d.resMu.Lock()
	defer d.resMu.Unlock()
	r, ok := d.results[resultKey{object, id}]
	return r, ok
}

// stateResult asks STATE for the latest image/playback outcome of id.
func (d *Device) stateResult(ctx context.Context, object uint8, id uint32) (uint8, bool) {
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	p, err := d.request(ctx, TypeState, next(&d.query), nil)
	if err != nil || len(p.Body) != 30 {
		return 0, false
	}
	at := 20
	if object == 2 {
		at = 25
	}
	if binary.BigEndian.Uint32(p.Body[at:]) != id {
		return 0, false
	}
	return p.Body[at+4], true
}
