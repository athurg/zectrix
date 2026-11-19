package main

import (
	"bytes"
	"context"
	_ "embed"
	"encoding/base64"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"image"
	_ "image/gif"
	_ "image/jpeg"
	_ "image/png"
	"io"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	zectrix "github.com/itopinion/zectrix-local-push-sdk-golang"
	"golang.org/x/image/font/opentype"
)

const (
	defaultVolume   = 80
	defaultSound    = "Glass"
	defaultHold     = 3
	maxRequestBytes = 12 << 20
)

//go:embed web/index.html
var indexHTML []byte

type pushRequest struct {
	Title  string
	Text   string
	Image  image.Image
	Volume int
	Sound  string
	Hold   int
}

type webServer struct {
	ctx    context.Context
	device string
	font   *opentype.Font
	mu     sync.Mutex
	push   func(context.Context, pushRequest) error
}

func newWebServer(ctx context.Context, device string, font *opentype.Font) *webServer {
	s := &webServer{ctx: ctx, device: device, font: font}
	s.push = s.pushDevice
	return s
}

func cmdDemo(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("demo", flag.ExitOnError)
	device := fs.String("device", "", "device address or id")
	listen := fs.String("listen", "127.0.0.1:8080", "HTTP listen address")
	fontPath := fs.String("font", "", "font file (default: macOS Chinese fonts)")
	fs.Parse(args)
	addr, err := resolve(ctx, *device)
	if err != nil {
		return err
	}
	font, err := zectrix.LoadFont(*fontPath)
	if err != nil {
		return err
	}
	web := newWebServer(ctx, addr, font)
	listener, err := net.Listen("tcp", *listen)
	if err != nil {
		return err
	}
	server := &http.Server{Handler: web.routes(), ReadHeaderTimeout: 5 * time.Second}
	errCh := make(chan error, 1)
	go func() { errCh <- server.Serve(listener) }()
	fmt.Printf("ZECTRIX web demo: http://%s\n", listener.Addr())
	select {
	case err := <-errCh:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		return server.Shutdown(shutdownCtx)
	}
}

func (s *webServer) routes() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Write(indexHTML)
	})
	mux.HandleFunc("GET /api/prompts", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, map[string]any{"prompts": zectrix.Prompts()})
	})
	mux.HandleFunc("POST /api/push", s.handlePush)
	return mux
}

func (s *webServer) handlePush(w http.ResponseWriter, r *http.Request) {
	req, err := parsePushRequest(r)
	if err != nil {
		writeJSON(w, http.StatusUnprocessableEntity, map[string]any{"ok": false, "error": err.Error()})
		return
	}
	if !s.mu.TryLock() {
		writeJSON(w, http.StatusConflict, map[string]any{"ok": false, "error": "another push is active"})
		return
	}
	defer s.mu.Unlock()
	ctx, cancel := context.WithCancel(r.Context())
	stop := context.AfterFunc(s.ctx, cancel)
	defer stop()
	defer cancel()
	if err := s.push(ctx, req); err != nil {
		if errors.Is(err, context.Canceled) && r.Context().Err() != nil {
			return
		}
		status := http.StatusBadGateway
		if errors.Is(err, zectrix.ErrBusy) {
			status = http.StatusConflict
		}
		writeJSON(w, status, map[string]any{"ok": false, "error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"ok": true, "sound": req.Sound, "volume": req.Volume, "hold": req.Hold,
	})
}

type jsonPushRequest struct {
	Title  string `json:"title"`
	Text   string `json:"text"`
	Image  string `json:"image"`
	Volume *int   `json:"volume"`
	Sound  string `json:"sound"`
	Hold   *int   `json:"hold"`
}

func parsePushRequest(r *http.Request) (pushRequest, error) {
	req := pushRequest{Volume: defaultVolume, Sound: defaultSound, Hold: defaultHold}
	contentType := r.Header.Get("Content-Type")
	var err error
	switch {
	case strings.HasPrefix(contentType, "application/json"):
		req, err = parseJSONPush(r)
	case strings.HasPrefix(contentType, "multipart/form-data"):
		req, err = parseFormPush(r)
	default:
		return req, fmt.Errorf("Content-Type must be application/json or multipart/form-data")
	}
	if err != nil {
		return req, err
	}
	return normalizePushRequest(req)
}

func parseJSONPush(r *http.Request) (pushRequest, error) {
	req := pushRequest{Volume: defaultVolume, Sound: defaultSound, Hold: defaultHold}
	var input jsonPushRequest
	decoder := json.NewDecoder(io.LimitReader(r.Body, maxRequestBytes))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&input); err != nil {
		return req, err
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		return req, fmt.Errorf("request must contain one JSON object")
	}
	req.Title, req.Text, req.Sound = input.Title, input.Text, input.Sound
	if input.Volume != nil {
		req.Volume = *input.Volume
	}
	if input.Hold != nil {
		req.Hold = *input.Hold
	}
	if req.Sound == "" {
		req.Sound = defaultSound
	}
	if input.Image != "" {
		encoded := input.Image
		if strings.HasPrefix(encoded, "data:") {
			_, encoded, _ = strings.Cut(encoded, ",")
		}
		data, err := base64.StdEncoding.DecodeString(encoded)
		if err != nil {
			return req, fmt.Errorf("image must be base64: %w", err)
		}
		req.Image, err = decodeImage(data)
		if err != nil {
			return req, err
		}
	}
	return req, nil
}

func parseFormPush(r *http.Request) (pushRequest, error) {
	req := pushRequest{Volume: defaultVolume, Sound: defaultSound, Hold: defaultHold}
	if err := r.ParseMultipartForm(maxRequestBytes); err != nil {
		return req, err
	}
	req.Title, req.Text = r.FormValue("title"), r.FormValue("text")
	if value := r.FormValue("sound"); value != "" {
		req.Sound = value
	}
	var err error
	if value := r.FormValue("volume"); value != "" {
		req.Volume, err = strconv.Atoi(value)
		if err != nil {
			return req, fmt.Errorf("volume must be an integer")
		}
	}
	if value := r.FormValue("hold"); value != "" {
		req.Hold, err = strconv.Atoi(value)
		if err != nil {
			return req, fmt.Errorf("hold must be an integer")
		}
	}
	file, _, err := r.FormFile("image")
	if err == nil {
		defer file.Close()
		data, err := io.ReadAll(io.LimitReader(file, maxRequestBytes+1))
		if err != nil {
			return req, err
		}
		req.Image, err = decodeImage(data)
		if err != nil {
			return req, err
		}
	} else if !errors.Is(err, http.ErrMissingFile) {
		return req, err
	}
	return req, nil
}

func decodeImage(data []byte) (image.Image, error) {
	img, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		return nil, fmt.Errorf("invalid image: %w", err)
	}
	return img, nil
}

func normalizePushRequest(req pushRequest) (pushRequest, error) {
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
	if (req.Text == "") == (req.Image == nil) {
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

func (s *webServer) pushDevice(ctx context.Context, req pushRequest) (err error) {
	audio, err := zectrix.PromptAudio(req.Sound)
	if err != nil {
		return err
	}
	var page image.Image
	if req.Image != nil {
		page = req.Image
	} else {
		page, err = zectrix.RenderText(s.font, req.Title, req.Text)
		if err != nil {
			return err
		}
	}
	d, err := openHTTPDevice(ctx, s.device)
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

func openHTTPDevice(ctx context.Context, addr string) (*zectrix.Device, error) {
	for attempt := 0; attempt < 4; attempt++ {
		d, err := zectrix.Open(ctx, addr, "Go HTTP Demo")
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

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(value)
}
