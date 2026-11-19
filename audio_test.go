package zectrix

import (
	"bytes"
	"context"
	"errors"
	"testing"
	"time"
)

func TestPromptAssets(t *testing.T) {
	if len(promptCatalog) != 6 {
		t.Fatalf("got %d prompts", len(promptCatalog))
	}
	seen := map[string]bool{}
	for _, prompt := range promptCatalog {
		if seen[prompt.ID] {
			t.Fatalf("duplicate prompt id %s", prompt.ID)
		}
		seen[prompt.ID] = true
		data, err := promptFiles.ReadFile(prompt.path)
		if err != nil {
			t.Fatal(err)
		}
		audio, err := DecodeAudio(data)
		if err != nil {
			t.Fatalf("prompt %s: %v", prompt.ID, err)
		}
		samples := int(prompt.Duration * 16000 / time.Second)
		wantFrames := (samples + 959) / 960
		wantTail := uint16((samples-1)%960 + 1)
		if len(audio.frames) != wantFrames || audio.tail != wantTail {
			t.Fatalf("prompt %s: frames=%d tail=%d, want %d/%d", prompt.ID, len(audio.frames), audio.tail, wantFrames, wantTail)
		}
		for i, frame := range audio.frames {
			if len(frame) == 0 || len(frame) > maxAudioFrame {
				t.Fatalf("prompt %s frame %d has %d bytes", prompt.ID, i, len(frame))
			}
		}
	}
}

func TestStartPromptOverlapsImage(t *testing.T) {
	f := newFake(t, Note4Model, defaultStatus)
	d := open(t, f)
	defer d.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	playback := d.StartPrompt(ctx, "1", 35)
	<-playback.Started
	if err := d.ShowMono(ctx, testPattern()); err != nil {
		t.Fatal(err)
	}
	if err := <-playback.Done; err != nil {
		t.Fatal(err)
	}

	data, _ := promptFiles.ReadFile(promptCatalog[0].path)
	want, _ := DecodeAudio(data)
	f.mu.Lock()
	if f.audioVolume != 35 || f.audioTotal != uint32(len(want.frames)) || f.audioTail != want.tail {
		t.Fatalf("volume=%d total=%d tail=%d", f.audioVolume, f.audioTotal, f.audioTail)
	}
	if len(f.audioFrames) != len(want.frames) {
		t.Fatalf("got %d frames, want %d", len(f.audioFrames), len(want.frames))
	}
	for i, frame := range want.frames {
		if !bytes.Equal(f.audioFrames[uint32(i)], frame) {
			t.Fatalf("frame %d differs", i)
		}
	}
	if len(f.order) != 2 || f.order[0] != TypeAudioBegin || f.order[1] != TypeImageBegin {
		t.Fatalf("request order %v", f.order)
	}
	if !f.imageDuringAudio {
		t.Fatal("image upload did not overlap audio playback")
	}
	f.mu.Unlock()
	deadline := time.Now().Add(time.Second)
	for f.ackCount(TypeResult) == 0 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if f.ackCount(TypeResult) == 0 {
		t.Fatal("RESULT was not acked")
	}
}

func TestPlayPromptUsesStateFallback(t *testing.T) {
	f := newFake(t, Note4Model, defaultStatus)
	f.audioViaResult = false
	d := open(t, f)
	defer d.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := d.PlayPrompt(ctx, "1", 35); err != nil {
		t.Fatal(err)
	}
	if f.count(TypeState) == 0 {
		t.Fatal("STATE fallback was not queried")
	}
}

func TestPlayPromptRejectsBusyBadVolumeAndUnknownID(t *testing.T) {
	f := newFake(t, Note4Model, defaultStatus)
	f.audioBeginResult = 1
	d := open(t, f)
	defer d.Close()
	if err := d.PlayPrompt(context.Background(), "1", 35); !errors.Is(err, ErrBusy) {
		t.Fatalf("busy: %v", err)
	}
	if err := d.PlayPrompt(context.Background(), "1", 101); err == nil {
		t.Fatal("volume 101 accepted")
	}
	if err := d.PlayPrompt(context.Background(), "unknown", 35); err == nil {
		t.Fatal("unknown prompt accepted")
	}
	if f.count(TypeAudioData) != 0 {
		t.Fatal("sent audio after rejected begin")
	}
}
