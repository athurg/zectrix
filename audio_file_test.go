package zectrix

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestLoadAudioFileAndPlayAudio(t *testing.T) {
	data, err := promptFiles.ReadFile(promptCatalog[0].path)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "prompt.opusframes")
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	audio, err := LoadAudioFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if audio.Duration != promptCatalog[0].Duration {
		t.Fatalf("duration=%s", audio.Duration)
	}
	if _, err := PromptAudio("pUrR"); err != nil {
		t.Fatal(err)
	}
	f := newFake(t, Note4Model, defaultStatus)
	d := open(t, f)
	defer d.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := d.PlayAudio(ctx, audio, 80); err != nil {
		t.Fatal(err)
	}
	if f.count(TypeAudioEnd) != 1 {
		t.Fatal("audio file was not played")
	}
}
