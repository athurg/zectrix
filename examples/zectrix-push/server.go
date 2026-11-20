package main

import (
	"bytes"
	"context"
	_ "embed"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"html/template"
	"image"
	_ "image/gif"
	_ "image/jpeg"
	_ "image/png"
	"io"
	"net/http"
	"strconv"
	"strings"
	"sync"

	zectrix "github.com/athurg/zectrix"
	"github.com/athurg/zectrix/assets"
)

const maxRequestBytes = 12 << 20

//go:embed web/index.html
var indexHTML string

var indexPage = template.Must(template.New("index").Parse(indexHTML))

type webServer struct {
	ctx      context.Context
	defaults pushRequest
	mu       sync.Mutex
	push     func(context.Context, pushRequest) error
}

func newWebServer(ctx context.Context, defaults pushRequest, font *zectrix.Font) *webServer {
	return &webServer{
		ctx:      ctx,
		defaults: defaults,
		push: func(ctx context.Context, req pushRequest) error {
			return pushDevice(ctx, req, font, "Go HTTP Demo")
		},
	}
}

func (s *webServer) routes() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		indexPage.Execute(w, struct {
			Defaults pushRequest
			Prompts  []zectrix.PromptInfo
		}{s.defaults, zectrix.Prompts()})
	})
	mux.HandleFunc("GET /assets/Zfull-GB.ttf", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "font/ttf")
		w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
		w.Write(assets.ZfullGB)
	})
	mux.HandleFunc("GET /api/prompts", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, map[string]any{"prompts": zectrix.Prompts()})
	})
	mux.HandleFunc("POST /api/push", s.handlePush)
	return mux
}

func (s *webServer) handlePush(w http.ResponseWriter, r *http.Request) {
	req, err := parsePushRequest(r, s.defaults)
	if err == nil {
		if req.Device == "" {
			req.Device = s.defaults.Device
		}
		if req.Device == "" {
			err = errors.New("device is required when no default device is configured")
		} else {
			req.Device, err = zectrix.NormalizeAddress(req.Device)
		}
	}
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
		"title_size": req.TitleSize, "body_size": req.BodySize,
	})
}

type jsonPushRequest struct {
	Device    string  `json:"device"`
	Title     *string `json:"title"`
	Text      *string `json:"text"`
	Footer    *string `json:"footer"`
	Image     string  `json:"image"`
	Volume    *int    `json:"volume"`
	Sound     string  `json:"sound"`
	Hold      *int    `json:"hold"`
	TitleSize *int    `json:"title_size"`
	BodySize  *int    `json:"body_size"`
}

func parsePushRequest(r *http.Request, defaults pushRequest) (pushRequest, error) {
	var req pushRequest
	contentType := r.Header.Get("Content-Type")
	var err error
	switch {
	case strings.HasPrefix(contentType, "application/json"):
		req, err = parseJSONPush(r, defaults)
	case strings.HasPrefix(contentType, "multipart/form-data"):
		req, err = parseFormPush(r, defaults)
	default:
		return req, fmt.Errorf("Content-Type must be application/json or multipart/form-data")
	}
	if err != nil {
		return req, err
	}
	return normalizePushRequest(req, true)
}

func parseJSONPush(r *http.Request, req pushRequest) (pushRequest, error) {
	var input jsonPushRequest
	decoder := json.NewDecoder(io.LimitReader(r.Body, maxRequestBytes))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&input); err != nil {
		return req, err
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		return req, fmt.Errorf("request must contain one JSON object")
	}
	req.Device = input.Device
	if input.Title != nil {
		req.Title = *input.Title
	}
	if input.Footer != nil {
		req.Footer = *input.Footer
	}
	if strings.TrimSpace(input.Sound) != "" {
		req.Sound = input.Sound
	}
	if input.Volume != nil {
		req.Volume = *input.Volume
	}
	if input.Hold != nil {
		req.Hold = *input.Hold
	}
	if input.TitleSize != nil {
		req.TitleSize = *input.TitleSize
	}
	if input.BodySize != nil {
		req.BodySize = *input.BodySize
	}
	if input.Text != nil || input.Image != "" {
		req.Text, req.Image = "", nil
		if input.Text != nil {
			req.Text = *input.Text
		}
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

func parseFormPush(r *http.Request, req pushRequest) (pushRequest, error) {
	if err := r.ParseMultipartForm(maxRequestBytes); err != nil {
		return req, err
	}
	req.Device = r.PostFormValue("device")
	if r.Form.Has("title") {
		req.Title = r.FormValue("title")
	}
	if r.Form.Has("footer") {
		req.Footer = r.FormValue("footer")
	}
	hasText := r.Form.Has("text")
	if hasText {
		req.Text, req.Image = r.FormValue("text"), nil
	}
	if value := r.FormValue("sound"); strings.TrimSpace(value) != "" {
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
	if value := r.FormValue("title_size"); value != "" {
		req.TitleSize, err = strconv.Atoi(value)
		if err != nil {
			return req, fmt.Errorf("title_size must be an integer")
		}
	}
	if value := r.FormValue("body_size"); value != "" {
		req.BodySize, err = strconv.Atoi(value)
		if err != nil {
			return req, fmt.Errorf("body_size must be an integer")
		}
	}
	file, _, err := r.FormFile("image")
	if err == nil {
		defer file.Close()
		if !hasText {
			req.Text = ""
		}
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

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(value)
}
