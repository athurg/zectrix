package main

import (
	"context"
	"errors"
	"fmt"
	"image"
	"strings"
	"time"

	"github.com/athurg/zectrix"
)

const (
	defaultTitle  = "通知"
	defaultText   = "一二三四五六七八九，一二三四五六七八九，一二三四五六\n二\n三\n四\n五\n六\n七\n八\n九\n十\n一"
	defaultVolume = 80
	defaultSound  = "Glass"
	defaultHold   = 10
)

type pushRequest struct {
	Device    string
	Title     string
	Text      string
	Image     image.Image
	Volume    int
	Sound     string
	Hold      int
	TitleSize int
	BodySize  int
}

func defaultPushRequest() pushRequest {
	return pushRequest{
		Title:     defaultTitle,
		Text:      defaultText,
		Volume:    defaultVolume,
		Sound:     defaultSound,
		Hold:      defaultHold,
		TitleSize: zectrix.DefaultTitleSize,
		BodySize:  zectrix.DefaultBodySize,
	}
}

func normalizePushRequest(req pushRequest, requireContent bool) (pushRequest, error) {
	req.Device = strings.TrimSpace(req.Device)
	req.Title = strings.TrimSpace(req.Title)
	req.Text = strings.TrimSpace(req.Text)
	req.Sound = strings.TrimSpace(req.Sound)
	if req.Sound == "" {
		req.Sound = defaultSound
	}
	if req.Volume < 0 || req.Volume > 100 {
		return req, fmt.Errorf("volume must be 0..100")
	}
	if req.Hold < 0 {
		return req, fmt.Errorf("hold must be a non-negative number of seconds")
	}
	if req.TitleSize < zectrix.MinFontSize || req.TitleSize > zectrix.MaxFontSize ||
		req.BodySize < zectrix.MinFontSize || req.BodySize > zectrix.MaxFontSize {
		return req, fmt.Errorf("title_size and body_size must be %d..%d", zectrix.MinFontSize, zectrix.MaxFontSize)
	}
	hasText, hasImage := req.Text != "", req.Image != nil
	if hasText && hasImage || requireContent && !hasText && !hasImage {
		return req, fmt.Errorf("provide exactly one of text or image")
	}
	for _, prompt := range zectrix.Prompts() {
		if prompt.ID == req.Sound || strings.EqualFold(prompt.Name, req.Sound) {
			req.Sound = prompt.Name
			return req, nil
		}
	}
	return req, fmt.Errorf("unknown sound %q", req.Sound)
}

func pushDevice(ctx context.Context, req pushRequest, font *zectrix.Font, clientName string) error {
	audio, err := zectrix.PromptAudio(req.Sound)
	if err != nil {
		return err
	}
	var page image.Image
	if req.Image != nil {
		page = req.Image
	} else {
		page, err = zectrix.RenderTextWithOptions(font, req.Title, req.Text, zectrix.TextOptions{
			TitleSize: req.TitleSize,
			BodySize:  req.BodySize,
		})
		if err != nil {
			return err
		}
	}
	d, err := openPushDevice(ctx, req.Device, clientName)
	if err != nil {
		return err
	}
	defer func() { _ = d.Close() }()
	playback := d.StartAudio(ctx, audio, uint8(req.Volume))
	<-playback.Started
	select {
	case err := <-playback.Done:
		if err != nil {
			return err
		}
	default:
	}
	imageErr := d.ShowImage(ctx, page)
	audioErr := <-playback.Done
	if err := errors.Join(audioErr, imageErr); err != nil {
		return err
	}
	return waitHold(ctx, req.Hold, d.Keys(), d.Done())
}

func openPushDevice(ctx context.Context, addr, clientName string) (*zectrix.Device, error) {
	for attempt := 0; attempt < 4; attempt++ {
		d, err := zectrix.Open(ctx, addr, clientName)
		if err == nil || !errors.Is(err, zectrix.ErrBusy) || attempt == 3 {
			return d, err
		}
		select {
		case <-time.After(2 * time.Second):
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	panic("unreachable")
}

func waitHold(ctx context.Context, hold int, keys <-chan zectrix.KeyEvent, done <-chan struct{}) error {
	var timeout <-chan time.Time
	var timer *time.Timer
	if hold > 0 {
		timer = time.NewTimer(time.Duration(hold) * time.Second)
		defer timer.Stop()
		timeout = timer.C
	}
	for {
		select {
		case <-keys:
			return nil
		case <-timeout:
			return nil
		case <-done:
			return zectrix.ErrDisconnected
		case <-ctx.Done():
			return ctx.Err()
		}
	}
}
