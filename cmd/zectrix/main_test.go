package main

import (
	"bytes"
	"context"
	"image"
	"image/png"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	zectrix "github.com/itopinion/zectrix-local-push-sdk-golang"
)

func testWebServer() (*webServer, *pushRequest) {
	s := &webServer{ctx: context.Background()}
	captured := &pushRequest{}
	s.push = func(_ context.Context, req pushRequest) error {
		*captured = req
		return nil
	}
	return s, captured
}

func TestPushJSONUsesDefaults(t *testing.T) {
	s, captured := testWebServer()
	r := httptest.NewRequest(http.MethodPost, "/api/push", strings.NewReader(`{"title":"通知","text":"你好"}`))
	r.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	s.routes().ServeHTTP(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("status %d: %s", w.Code, w.Body)
	}
	if captured.Title != "通知" || captured.Text != "你好" || captured.Volume != 80 || captured.Sound != "Glass" || captured.Hold != 3 {
		t.Fatalf("%+v", captured)
	}
}

func TestPushJSONAcceptsExplicitZeroAndManualHold(t *testing.T) {
	s, captured := testWebServer()
	r := httptest.NewRequest(http.MethodPost, "/api/push", strings.NewReader(`{"text":"你好","volume":0,"sound":"glass","hold":0}`))
	r.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	s.routes().ServeHTTP(w, r)
	if w.Code != http.StatusOK || captured.Volume != 0 || captured.Sound != "Glass" || captured.Hold != 0 {
		t.Fatalf("status %d request %+v", w.Code, captured)
	}
}

func TestPushMultipartImage(t *testing.T) {
	var encoded bytes.Buffer
	writer := multipart.NewWriter(&encoded)
	writer.WriteField("volume", "90")
	writer.WriteField("sound", "Tink")
	writer.WriteField("hold", "0")
	file, err := writer.CreateFormFile("image", "test.png")
	if err != nil {
		t.Fatal(err)
	}
	img := image.NewNRGBA(image.Rect(0, 0, 20, 10))
	if err := png.Encode(file, img); err != nil {
		t.Fatal(err)
	}
	writer.Close()

	s, captured := testWebServer()
	r := httptest.NewRequest(http.MethodPost, "/api/push", &encoded)
	r.Header.Set("Content-Type", writer.FormDataContentType())
	w := httptest.NewRecorder()
	s.routes().ServeHTTP(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("status %d: %s", w.Code, w.Body)
	}
	if captured.Image == nil || captured.Image.Bounds().Dx() != 20 || captured.Volume != 90 || captured.Sound != "Tink" || captured.Hold != 0 {
		t.Fatalf("%+v", captured)
	}
}

func TestPushRejectsInvalidRequests(t *testing.T) {
	for _, body := range []string{
		`{}`,
		`{"text":"x","hold":-1}`,
		`{"text":"x","hold":-2}`,
		`{"text":"x","volume":101}`,
		`{"text":"x","sound":"missing"}`,
		`{"text":"x","unknown":true}`,
	} {
		s, _ := testWebServer()
		r := httptest.NewRequest(http.MethodPost, "/api/push", strings.NewReader(body))
		r.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		s.routes().ServeHTTP(w, r)
		if w.Code != http.StatusUnprocessableEntity {
			t.Fatalf("%s: status %d", body, w.Code)
		}
	}
}

func TestWaitHoldAcceptsAnyKeyForManualAndTimedDisplay(t *testing.T) {
	for _, test := range []struct {
		hold int
		key  zectrix.Key
	}{{0, zectrix.KeyUp}, {0, zectrix.KeyDown}, {3, zectrix.KeyConfirm}} {
		keys := make(chan zectrix.KeyEvent, 1)
		done := make(chan struct{})
		keys <- zectrix.KeyEvent{Key: test.key}
		if err := waitHold(context.Background(), test.hold, keys, done); err != nil {
			t.Fatalf("hold=%d key=%v: %v", test.hold, test.key, err)
		}
	}
}

func TestWebPageAndPromptCatalog(t *testing.T) {
	s, _ := testWebServer()
	for _, path := range []string{"/", "/api/prompts"} {
		r := httptest.NewRequest(http.MethodGet, path, nil)
		w := httptest.NewRecorder()
		s.routes().ServeHTTP(w, r)
		if w.Code != http.StatusOK || w.Body.Len() == 0 {
			t.Fatalf("%s: status %d", path, w.Code)
		}
	}
}
