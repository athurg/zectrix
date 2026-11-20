package zectrix

import (
	"context"
	"embed"
	"encoding/binary"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"
)

const (
	audioFrameInterval = 60 * time.Millisecond
	audioMaxLate       = 360 * time.Millisecond
	audioEndDeadline   = 15 * time.Second
	audioPollInterval  = 500 * time.Millisecond
	maxAudioFrame      = 1180
)

// ErrPlaybackFailed means playback ended without completing normally.
var ErrPlaybackFailed = errors.New("zectrix: device did not complete playback")

// Audio is a pre-encoded 16 kHz mono Opus stream with 60 ms frames.
type Audio struct {
	frames   [][]byte
	tail     uint16
	Duration time.Duration
}

// DecodeAudio decodes the ZPF1 audio-file format used by the SDK. Each frame
// is already Opus encoded, so playback requires no native codec library.
func DecodeAudio(data []byte) (*Audio, error) {
	if len(data) < 8 || string(data[:4]) != "ZPF1" {
		return nil, fmt.Errorf("zectrix: invalid audio file header")
	}
	data = append([]byte(nil), data...)
	tail := binary.BigEndian.Uint16(data[4:6])
	if tail == 0 || tail > 960 {
		return nil, fmt.Errorf("zectrix: invalid audio tail %d", tail)
	}
	var frames [][]byte
	for offset := 6; offset < len(data); {
		if len(data)-offset < 2 {
			return nil, fmt.Errorf("zectrix: truncated audio frame length")
		}
		n := int(binary.BigEndian.Uint16(data[offset : offset+2]))
		offset += 2
		if n == 0 || n > maxAudioFrame || n > len(data)-offset {
			return nil, fmt.Errorf("zectrix: invalid audio frame length %d", n)
		}
		frames = append(frames, data[offset:offset+n])
		offset += n
	}
	if len(frames) == 0 || len(frames) > 2000 {
		return nil, fmt.Errorf("zectrix: invalid audio frame count %d", len(frames))
	}
	samples := (len(frames)-1)*960 + int(tail)
	return &Audio{frames: frames, tail: tail, Duration: time.Duration(samples) * time.Second / 16000}, nil
}

// LoadAudioFile loads a pre-encoded ZPF1 audio file.
func LoadAudioFile(path string) (*Audio, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return DecodeAudio(data)
}

// PromptInfo identifies one built-in prompt sound.
type PromptInfo struct {
	ID       string
	Name     string
	Duration time.Duration
}

type promptSound struct {
	PromptInfo
	path string
}

var promptCatalog = []promptSound{
	{PromptInfo: PromptInfo{ID: "1", Name: "Purr", Duration: 12123 * time.Second / 16000}, path: "assets/1-purr.opusframes"},
	{PromptInfo: PromptInfo{ID: "2", Name: "Tink", Duration: 9026 * time.Second / 16000}, path: "assets/2-tink.opusframes"},
	{PromptInfo: PromptInfo{ID: "3", Name: "Bottle", Duration: 12372 * time.Second / 16000}, path: "assets/3-bottle.opusframes"},
	{PromptInfo: PromptInfo{ID: "4", Name: "Pop", Duration: 26039 * time.Second / 16000}, path: "assets/4-pop.opusframes"},
	{PromptInfo: PromptInfo{ID: "5", Name: "Glass", Duration: 26401 * time.Second / 16000}, path: "assets/5-glass.opusframes"},
	{PromptInfo: PromptInfo{ID: "6", Name: "Submarine", Duration: 23879 * time.Second / 16000}, path: "assets/6-submarine.opusframes"},
}

//go:embed assets/*.opusframes
var promptFiles embed.FS

// Prompts returns the available prompt IDs in playback order.
func Prompts() []PromptInfo {
	out := make([]PromptInfo, len(promptCatalog))
	for i, prompt := range promptCatalog {
		out[i] = prompt.PromptInfo
	}
	return out
}

// PromptAudio loads a built-in prompt by numeric ID or case-insensitive name.
func PromptAudio(idOrName string) (*Audio, error) {
	for _, prompt := range promptCatalog {
		if prompt.ID == idOrName || strings.EqualFold(prompt.Name, idOrName) {
			data, err := promptFiles.ReadFile(prompt.path)
			if err != nil {
				return nil, fmt.Errorf("zectrix: load prompt %s: %w", idOrName, err)
			}
			return DecodeAudio(data)
		}
	}
	return nil, fmt.Errorf("zectrix: unknown prompt %q", idOrName)
}

// Playback tracks asynchronous audio. Started is closed once AUDIO_BEGIN has
// been answered; Done receives the final playback result.
type Playback struct {
	Started <-chan struct{}
	Done    <-chan error
}

// PlayPrompt plays a built-in prompt by ID or name and waits for completion.
func (d *Device) PlayPrompt(ctx context.Context, idOrName string, volume uint8) error {
	return <-d.StartPrompt(ctx, idOrName, volume).Done
}

// StartPrompt starts a built-in prompt without waiting for playback to finish.
func (d *Device) StartPrompt(ctx context.Context, idOrName string, volume uint8) *Playback {
	audio, err := PromptAudio(idOrName)
	if err != nil {
		return failedPlayback(err)
	}
	return d.StartAudio(ctx, audio, volume)
}

// PlayAudio plays a pre-encoded audio file and waits for completion. Volume is
// temporary, from 0 to 100.
func (d *Device) PlayAudio(ctx context.Context, audio *Audio, volume uint8) error {
	return <-d.StartAudio(ctx, audio, volume).Done
}

// StartAudio starts pre-encoded audio without waiting for playback to finish.
func (d *Device) StartAudio(ctx context.Context, audio *Audio, volume uint8) *Playback {
	started := make(chan struct{})
	done := make(chan error, 1)
	go func() {
		if volume > 100 {
			close(started)
			done <- fmt.Errorf("zectrix: volume must be 0..100")
		} else if audio == nil {
			close(started)
			done <- fmt.Errorf("zectrix: audio is nil")
		} else {
			d.audioMu.Lock()
			done <- d.playFrames(ctx, audio.frames, audio.tail, volume, started)
			d.audioMu.Unlock()
		}
		close(done)
	}()
	return &Playback{Started: started, Done: done}
}

func failedPlayback(err error) *Playback {
	started := make(chan struct{})
	done := make(chan error, 1)
	close(started)
	done <- err
	close(done)
	return &Playback{Started: started, Done: done}
}

func playbackResult(reason uint8) error {
	if reason == 0 {
		return nil
	}
	return fmt.Errorf("%w (%s)", ErrPlaybackFailed, CloseReason(reason))
}

func (d *Device) playFrames(ctx context.Context, frames [][]byte, tail uint16, volume uint8, started chan struct{}) error {
	if d.closed() {
		close(started)
		return ErrDisconnected
	}
	d.cmdMu.Lock()
	id := next(&d.control)
	d.playback.Store(id)
	_, err := d.request(ctx, TypeAudioBegin, id, []byte{volume})
	d.cmdMu.Unlock()
	close(started)
	if err != nil {
		d.playback.Store(0)
		return fmt.Errorf("zectrix: AUDIO_BEGIN: %w", err)
	}
	defer d.playback.Store(0)

	start := time.Now()
	for i, frame := range frames {
		at := start.Add(time.Duration(i) * audioFrameInterval)
		if err := waitAudio(ctx, d.done, at); err != nil {
			d.stopAudio(id)
			return err
		}
		if r, ok := d.result(2, id); ok {
			return playbackResult(r)
		}
		if time.Since(at) > audioMaxLate {
			d.stopAudio(id)
			return fmt.Errorf("zectrix: audio sending stalled")
		}
		body := make([]byte, 4, 4+len(frame))
		binary.BigEndian.PutUint32(body, id)
		body = append(body, frame...)
		if _, err := d.udp.Write(Packet{Kind: TypeAudioData, Conn: d.conn, Seq: uint32(i), Body: body}.Encode()); err != nil {
			return fmt.Errorf("zectrix: AUDIO_DATA: %w", err)
		}
	}

	body := make([]byte, 11)
	binary.BigEndian.PutUint32(body, id)
	binary.BigEndian.PutUint32(body[4:], uint32(len(frames)))
	binary.BigEndian.PutUint16(body[8:], tail)
	d.cmdMu.Lock()
	_, err = d.request(ctx, TypeAudioEnd, next(&d.control), body)
	d.cmdMu.Unlock()
	if err != nil {
		if r, ok := d.result(2, id); ok {
			return playbackResult(r)
		}
		if ctx.Err() != nil {
			d.stopAudio(id)
		}
		return fmt.Errorf("zectrix: AUDIO_END: %w", err)
	}

	deadline := time.Now().Add(audioEndDeadline)
	for {
		if r, ok := d.result(2, id); ok {
			return playbackResult(r)
		}
		if d.closed() {
			return ErrDisconnected
		}
		if err := ctx.Err(); err != nil {
			d.stopAudio(id)
			return err
		}
		if !time.Now().Before(deadline) {
			return ErrResultUnknown
		}
		if r, ok := d.stateResult(ctx, 2, id); ok {
			return playbackResult(r)
		}
		if err := waitAudio(ctx, d.done, time.Now().Add(audioPollInterval)); err != nil {
			if ctx.Err() != nil {
				d.stopAudio(id)
			}
			return err
		}
	}
}

func waitAudio(ctx context.Context, done <-chan struct{}, at time.Time) error {
	wait := time.Until(at)
	if wait <= 0 {
		select {
		case <-done:
			return ErrDisconnected
		default:
			return ctx.Err()
		}
	}
	timer := time.NewTimer(wait)
	defer timer.Stop()
	select {
	case <-timer.C:
		return nil
	case <-done:
		return ErrDisconnected
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (d *Device) stopAudio(id uint32) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	body := binary.BigEndian.AppendUint32(nil, id)
	d.cmdMu.Lock()
	_, _ = d.request(ctx, TypeAudioStop, next(&d.control), body)
	d.cmdMu.Unlock()
}
