package zectrix

import (
	"bytes"
	"compress/zlib"
	"context"
	"encoding/binary"
	"errors"
	"io"
	"net"
	"sync"
	"testing"
	"time"
)

const fakeConn = 0x1122334455667788

// fakeDevice is a loopback UDP Note 4 implementing INFO, CONNECT, PING,
// STATUS, CLOSE and the image upload (BEGIN / DATA / IMAGE_STATUS).
type fakeDevice struct {
	t      *testing.T
	sock   *net.UDPConn
	model  string
	status []byte // STATUS payload after the result code; nil = ignore (old firmware)

	mu       sync.Mutex
	client   *net.UDPAddr
	got      map[uint8]int // requests by type
	acks     map[uint8]int // event acks by type
	closeReq chan struct{}

	// Image upload state.
	viaResult bool // report completion by RESULT event, status stays REFRESHING
	dropChunk int  // drop this chunk index once (-1 = none) to force a resend
	imgID     uint32
	imgBpp    uint8
	imgLen    int
	chunks    map[uint32][]byte
	imgDone   bool
	shown     [][]byte // decompressed frames that "reached the screen"
	resultSeq uint32

	audioID          uint32
	audioVolume      uint8
	audioFrames      map[uint32][]byte
	audioTotal       uint32
	audioTail        uint16
	audioDone        bool
	audioViaResult   bool
	audioBeginResult uint8
	imageDuringAudio bool
	order            []uint8
}

var defaultStatus = []byte{73, 0, 195}

func newFake(t *testing.T, model string, status []byte) *fakeDevice {
	sock, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		t.Fatal(err)
	}
	f := &fakeDevice{t: t, sock: sock, model: model, status: status,
		got: map[uint8]int{}, acks: map[uint8]int{}, closeReq: make(chan struct{}, 4),
		dropChunk: -1, resultSeq: 100, audioViaResult: true}
	t.Cleanup(func() { sock.Close() })
	go f.serve()
	return f
}

func (f *fakeDevice) addr() string { return f.sock.LocalAddr().String() }

func (f *fakeDevice) count(kind uint8) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.got[kind]
}

func (f *fakeDevice) ackCount(kind uint8) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.acks[kind]
}

func (f *fakeDevice) serve() {
	buf := make([]byte, 1500)
	for {
		n, from, err := f.sock.ReadFromUDP(buf)
		if err != nil {
			return
		}
		p, ok := Decode(buf[:n])
		if !ok {
			continue
		}
		f.mu.Lock()
		if p.Kind&responseBit != 0 {
			f.acks[p.Kind&^responseBit]++
			f.mu.Unlock()
			continue
		}
		f.got[p.Kind]++
		if p.Kind == TypeAudioBegin || p.Kind == TypeImageBegin {
			f.order = append(f.order, p.Kind)
		}
		f.mu.Unlock()
		resp := []byte{0}
		switch p.Kind {
		case TypeInfo:
			resp = append(resp, 0, 12)
			resp = append(resp, "aabbccddeeff"...)
			resp = append(resp, byte(len(f.model)))
			resp = append(resp, f.model...)
			resp = append(resp, fullCanvas...)
		case TypeConnect:
			f.mu.Lock()
			f.client = from
			f.mu.Unlock()
		case TypePing:
		case TypeStatus:
			if f.status == nil {
				continue
			}
			resp = append(resp, f.status...)
		case TypeClose:
			f.closeReq <- struct{}{}
		case TypeImageBegin:
			f.mu.Lock()
			if f.audioID != 0 && !f.audioDone {
				f.imageDuringAudio = true
			}
			if f.imgID != p.Seq {
				f.imgID, f.imgBpp = p.Seq, p.Body[0]
				f.imgLen = int(binary.BigEndian.Uint16(p.Body[1:]))
				f.chunks = map[uint32][]byte{}
				f.imgDone = false
			}
			f.mu.Unlock()
		case TypeImageData:
			f.mu.Lock()
			if binary.BigEndian.Uint32(p.Body) == f.imgID {
				if int(p.Seq) == f.dropChunk {
					f.dropChunk = -1
				} else {
					f.chunks[p.Seq] = p.Body[4:]
				}
			}
			f.mu.Unlock()
			continue
		case TypeImageStatus:
			resp = append(resp, f.imageState(from)...)
		case TypeAudioBegin:
			f.mu.Lock()
			resp[0] = f.audioBeginResult
			if resp[0] == 0 {
				f.audioID = p.Seq
				f.audioVolume = p.Body[0]
				f.audioFrames = map[uint32][]byte{}
				f.audioDone = false
			}
			f.mu.Unlock()
		case TypeAudioData:
			f.mu.Lock()
			if len(p.Body) >= 4 && binary.BigEndian.Uint32(p.Body) == f.audioID {
				f.audioFrames[p.Seq] = append([]byte(nil), p.Body[4:]...)
			}
			f.mu.Unlock()
			continue
		case TypeAudioEnd:
			f.mu.Lock()
			if p.Seq <= f.audioID {
				f.mu.Unlock()
				resp[0] = 2
				break
			}
			if binary.BigEndian.Uint32(p.Body) == f.audioID {
				f.audioTotal = binary.BigEndian.Uint32(p.Body[4:])
				f.audioTail = binary.BigEndian.Uint16(p.Body[8:])
				f.audioDone = true
			}
			f.resultSeq++
			seq := f.resultSeq
			viaResult := f.audioViaResult
			id := f.audioID
			f.mu.Unlock()
			if viaResult {
				body := binary.BigEndian.AppendUint32(nil, id)
				body = append(body, 2, 0)
				f.send(TypeResult, seq, body)
			}
		case TypeState:
			resp = make([]byte, 30)
			f.mu.Lock()
			if f.audioDone {
				binary.BigEndian.PutUint32(resp[25:], f.audioID)
			}
			f.mu.Unlock()
		default:
			continue
		}
		f.sock.WriteToUDP(Packet{Kind: p.Kind | responseBit, Conn: fakeConn, Seq: p.Seq, Body: resp}.Encode(), from)
	}
}

// imageState returns [state, u64 bitmap]; on completion it decompresses the
// frame once and, in viaResult mode, sends a RESULT event instead of DONE.
func (f *fakeDevice) imageState(to *net.UDPAddr) []byte {
	f.mu.Lock()
	defer f.mu.Unlock()
	var bitmap uint64
	total := 0
	for i, c := range f.chunks {
		bitmap |= 1 << i
		total += len(c)
	}
	if !f.imgDone && f.imgLen > 0 && total == f.imgLen {
		var z []byte
		for i := uint32(0); i < uint32(len(f.chunks)); i++ {
			z = append(z, f.chunks[i]...)
		}
		r, err := zlib.NewReader(bytes.NewReader(z))
		if err != nil {
			f.t.Errorf("zlib: %v", err)
			return []byte{imageFailed, 0, 0, 0, 0, 0, 0, 0, 0}
		}
		raw, _ := io.ReadAll(r)
		f.shown = append(f.shown, raw)
		f.imgDone = true
		if f.viaResult {
			body := binary.BigEndian.AppendUint32(nil, f.imgID)
			body = append(body, 1, 0)
			f.resultSeq++
			f.sock.WriteToUDP(Packet{Kind: TypeResult, Conn: fakeConn, Seq: f.resultSeq, Body: body}.Encode(), to)
		}
	}
	state := byte(imageReceiving)
	if f.imgDone {
		state = imageDone
		if f.viaResult {
			state = imageRefreshing
		}
	}
	return binary.BigEndian.AppendUint64([]byte{state}, bitmap)
}

func (f *fakeDevice) frames() [][]byte {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.shown
}

// send pushes a device-originated event to the connected client.
func (f *fakeDevice) send(kind uint8, seq uint32, body []byte) {
	f.mu.Lock()
	to := f.client
	f.mu.Unlock()
	f.sock.WriteToUDP(Packet{Kind: kind, Conn: fakeConn, Seq: seq, Body: body}.Encode(), to)
}

func open(t *testing.T, f *fakeDevice) *Device {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	d, err := Open(ctx, f.addr(), "test")
	if err != nil {
		t.Fatal(err)
	}
	return d
}

func TestPacketRoundTrip(t *testing.T) {
	p := Packet{Kind: 14, Conn: 0x0102030405060708, Seq: 0x090a0b0c, Body: []byte{3, 1}}
	want := []byte{'Z', 'L', 1, 14, 1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 3, 1}
	if got := p.Encode(); string(got) != string(want) {
		t.Fatalf("encode = %v", got)
	}
	q, ok := Decode(want)
	if !ok || q.Kind != p.Kind || q.Conn != p.Conn || q.Seq != p.Seq || string(q.Body) != string(p.Body) {
		t.Fatalf("decode = %+v", q)
	}
	if _, ok := Decode([]byte("ZL\x02xxxxxxxxxxxxxx")); ok {
		t.Fatal("wrong version accepted")
	}
}

func TestParseStatus(t *testing.T) {
	s, err := parseStatus([]byte{0, 73, 1, 195})
	if err != nil || !s.BatteryKnown || s.Battery != 73 || !s.Charging || s.RSSI != -61 {
		t.Fatalf("%+v %v", s, err)
	}
	s, err = parseStatus([]byte{0, 255, 255, 127})
	if err != nil || s.BatteryKnown || s.ChargingKnown || s.RSSIKnown {
		t.Fatalf("unknowns: %+v %v", s, err)
	}
	for _, bad := range [][]byte{{0, 101, 0, 195}, {0, 50, 2, 195}, {0, 50, 0, 5}, {0, 50, 0}} {
		if _, err := parseStatus(bad); err == nil {
			t.Fatalf("accepted %v", bad)
		}
	}
}

func TestNormalizeAddress(t *testing.T) {
	if a, _ := NormalizeAddress("192.168.1.100"); a != "192.168.1.100:32342" {
		t.Fatal(a)
	}
	if a, _ := NormalizeAddress("192.168.1.100:4000"); a != "192.168.1.100:4000" {
		t.Fatal(a)
	}
	for _, bad := range []string{"", "note4.local", "[::1]:32342", "1.2.3.4:0"} {
		if _, err := NormalizeAddress(bad); err == nil {
			t.Fatalf("accepted %q", bad)
		}
	}
}

func TestStatusAndCloseSendsClose(t *testing.T) {
	f := newFake(t, Note4Model, defaultStatus)
	d := open(t, f)
	if d.Info.ID != "aabbccddeeff" || d.Info.Model != Note4Model {
		t.Fatalf("info %+v", d.Info)
	}
	s, err := d.Status(context.Background())
	if err != nil || s.Battery != 73 || s.Charging || s.RSSI != -61 {
		t.Fatalf("status %+v %v", s, err)
	}
	if err := d.Close(); err != nil {
		t.Fatal(err)
	}
	if f.count(TypeClose) != 1 {
		t.Fatalf("CLOSE sent %d times", f.count(TypeClose))
	}
	if reason, local := d.Reason(); !local || reason != ReasonNormal {
		t.Fatalf("reason %v local %v", reason, local)
	}
	if err := d.Close(); err != nil || f.count(TypeClose) != 1 {
		t.Fatal("second Close must be a no-op")
	}
	if _, err := d.Status(context.Background()); !errors.Is(err, ErrDisconnected) {
		t.Fatalf("status after close: %v", err)
	}
}

func TestKeepalivePingsImmediately(t *testing.T) {
	f := newFake(t, Note4Model, defaultStatus)
	d := open(t, f)
	defer d.Close()
	deadline := time.Now().Add(2 * time.Second)
	for f.count(TypePing) == 0 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if f.count(TypePing) == 0 {
		t.Fatal("no PING")
	}
}

func TestWrongModelNeverConnects(t *testing.T) {
	f := newFake(t, "zectrix-note4c", defaultStatus)
	if _, err := Open(context.Background(), f.addr(), "test"); !errors.Is(err, ErrWrongModel) {
		t.Fatalf("wrong model: %v", err)
	}
	if f.count(TypeConnect) != 0 {
		t.Fatal("CONNECT sent to wrong model")
	}
}

func TestDeviceCloseEndsSessionAndKeysAreAckedOnce(t *testing.T) {
	f := newFake(t, Note4Model, defaultStatus)
	d := open(t, f)
	f.send(TypeKey, 10, []byte{1, 2})
	f.send(TypeKey, 10, []byte{1, 2}) // retransmit
	select {
	case k := <-d.Keys():
		if k.Key != KeyUp || !k.Long {
			t.Fatalf("key %+v", k)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("no key")
	}
	select {
	case k := <-d.Keys():
		t.Fatalf("duplicate delivered: %+v", k)
	case <-time.After(200 * time.Millisecond):
	}
	if f.ackCount(TypeKey) != 2 {
		t.Fatalf("acks %d, want 2", f.ackCount(TypeKey))
	}
	f.send(TypeClose, 11, []byte{byte(ReasonDeviceExit)})
	select {
	case <-d.Done():
	case <-time.After(2 * time.Second):
		t.Fatal("session did not end")
	}
	if reason, local := d.Reason(); local || reason != ReasonDeviceExit {
		t.Fatalf("reason %v local %v", reason, local)
	}
	if err := d.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestStatusTimeoutKeepsConnection(t *testing.T) {
	f := newFake(t, Note4Model, nil) // old firmware ignores STATUS
	d := open(t, f)
	defer d.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	if _, err := d.Status(ctx); err == nil {
		t.Fatal("expected timeout")
	}
	select {
	case <-d.Done():
		t.Fatal("status timeout must not disconnect")
	default:
	}
}

func TestShowMonoSendsAllChunksAndWaitsForDone(t *testing.T) {
	f := newFake(t, Note4Model, defaultStatus)
	f.dropChunk = 0 // first copy of chunk 0 is lost; it must be resent
	d := open(t, f)
	defer d.Close()
	img := testPattern()
	want, _ := PackMono(img)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := d.ShowMono(ctx, img); err != nil {
		t.Fatal(err)
	}
	frames := f.frames()
	if len(frames) != 1 || !bytes.Equal(frames[0], want) {
		t.Fatalf("frames=%d, payload match=%v", len(frames), len(frames) == 1 && bytes.Equal(frames[0], want))
	}
	if f.imgBpp != 1 || f.count(TypeImageBegin) != 1 {
		t.Fatalf("bpp=%d begins=%d", f.imgBpp, f.count(TypeImageBegin))
	}
}

func TestShowMonoCompletesOnResultEvent(t *testing.T) {
	f := newFake(t, Note4Model, defaultStatus)
	f.viaResult = true
	d := open(t, f)
	defer d.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := d.ShowMono(ctx, testPattern()); err != nil {
		t.Fatal(err)
	}
	if f.ackCount(TypeResult) == 0 {
		t.Fatal("RESULT was not acked")
	}
}

func TestOnPingReportsEachHeartbeat(t *testing.T) {
	f := newFake(t, Note4Model, defaultStatus)
	got := make(chan PingResult, 4)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	d, err := OpenWithOptions(ctx, f.addr(), Options{ClientName: "test", OnPing: func(r PingResult) { got <- r }})
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	select {
	case r := <-got:
		if r.Err != nil || r.Seq != 1 {
			t.Fatalf("%+v", r)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("OnPing not called")
	}
}
