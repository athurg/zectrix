package main

import (
	"context"
	"errors"
	"fmt"
	"image"
	"strings"
	"time"

	"github.com/athurg/zectrix"
	"github.com/athurg/zectrix/textimg"
)

const (
	defaultTitle  = "通知"
	defaultFooter = "按任意键退出..."
	defaultText   = "道可道，非常道；名可名，非常名。无名天地之始，" +
		"有名万物之母。故常无欲，以观其妙；常有欲，以观其徼。" +
		"此两者同出而异名，同谓之玄。玄之又玄，众妙之门。" +
		"上善若水，水善利万物而不争，处众人之所恶，故几于道。" +
		"居善地，心善渊，与善仁，言善信，事善能，动善时。" +
		"知人者智，自知者明。胜人者有力，自胜者强。知足者富。" +
		"强行者有志，不失其所者久，死而不亡者寿。知足不辱，" +
		"知止不殆，可以长久。为学日益，为道日损。损之又损，" +
		"以至于无为，无为而无不为。取天下常以无事，" +
		"大方无隅，大器晚成，大音希声，大象无形，道隐无名。" +
		"大直若屈，大巧若拙，大辩若讷。躁胜寒，静胜热。" +
		"清静为天下正。祸兮福之所倚，福兮祸之所伏。" +
		"治大国若烹小鲜。以道莅天下，其鬼不神。非其鬼不神，" +
		"其神不伤人，圣人亦不伤人。夫两不相伤，故德交归焉。" +
		"天下皆知美之为美，斯恶已；皆知善之为善，斯不善已。" +
		"故有无相生，难易相成，长短相形，高下相倾。"
	defaultVolume = 80
	defaultSound  = "Glass"
	defaultHold   = 10
)

type pushRequest struct {
	Device    string
	Title     string
	Text      string
	Footer    string
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
		Footer:    defaultFooter,
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
	req.Footer = strings.TrimSpace(req.Footer)
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
		page, err = textimg.RenderWithOptions(font, req.Title, req.Text, req.Footer, textimg.Options{
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
