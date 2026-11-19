package zectrix

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"net"
	"strconv"
	"sync"
	"sync/atomic"
	"time"
)

const (
	pingInterval     = 5 * time.Second
	pingWait         = 4500 * time.Millisecond
	keepaliveTimeout = 15 * time.Second
	retryInitial     = 1500 * time.Millisecond
	retryMax         = 4 * time.Second
	requestDeadline  = 15 * time.Second
	closeWait        = 2 * time.Second
	statusWait       = 5 * time.Second
)

// Info is the read-only identity returned by INFO. It never takes ownership.
type Info struct {
	Address string
	ID      string
	Model   string
	// Manage is 0 LOCAL, 1 CONNECTING, 2 REMOTE, 3 EXITING.
	Manage uint8
	// Conn is the device's current connection id, used by CONNECT.
	Conn uint64
}

func (i Info) Busy() bool { return i.Manage != 0 }

// Status is the STATUS reply. *Known=false means the device has no value.
type Status struct {
	Battery       uint8 // percent, 0..100
	BatteryKnown  bool
	Charging      bool
	ChargingKnown bool
	RSSI          int8 // dBm
	RSSIKnown     bool
}

func (s Status) String() string {
	battery, charging, rssi := "unknown", "unknown", "unknown"
	if s.BatteryKnown {
		battery = fmt.Sprintf("%d%%", s.Battery)
	}
	if s.ChargingKnown {
		charging = strconv.FormatBool(s.Charging)
	}
	if s.RSSIKnown {
		rssi = fmt.Sprintf("%d dBm", s.RSSI)
	}
	return fmt.Sprintf("battery=%s charging=%s wifi_rssi=%s", battery, charging, rssi)
}

// Key events reported by the device in remote mode.
type Key uint8

const (
	KeyUp      Key = 1
	KeyDown    Key = 2
	KeyConfirm Key = 3
)

func (k Key) String() string {
	switch k {
	case KeyUp:
		return "up"
	case KeyDown:
		return "down"
	case KeyConfirm:
		return "confirm"
	}
	return fmt.Sprintf("key(%d)", uint8(k))
}

type KeyEvent struct {
	Key  Key
	Long bool
}

// NormalizeAddress accepts "IPv4" or "IPv4:port" and returns "IPv4:port".
func NormalizeAddress(s string) (string, error) {
	if ip := net.ParseIP(s); ip != nil && ip.To4() != nil {
		return net.JoinHostPort(ip.String(), strconv.Itoa(DefaultPort)), nil
	}
	host, port, err := net.SplitHostPort(s)
	if err == nil {
		if ip := net.ParseIP(host); ip != nil && ip.To4() != nil {
			if p, err := strconv.Atoi(port); err == nil && p > 0 && p < 65536 {
				return s, nil
			}
		}
	}
	return "", fmt.Errorf("zectrix: address must be IPv4 or IPv4:port, got %q", s)
}

func dial(addr string) (*net.UDPConn, string, error) {
	addr, err := NormalizeAddress(addr)
	if err != nil {
		return nil, "", err
	}
	raddr, err := net.ResolveUDPAddr("udp4", addr)
	if err != nil {
		return nil, "", err
	}
	c, err := net.DialUDP("udp4", nil, raddr)
	return c, addr, err
}

// exchange is the pre-session request/response used for INFO and CONNECT,
// before the receive goroutine owns the socket. Retransmits keep the same
// sequence and body.
func exchange(ctx context.Context, c *net.UDPConn, p Packet, total time.Duration) (Packet, error) {
	stop := context.AfterFunc(ctx, func() { c.SetReadDeadline(time.Now()) })
	defer stop()
	defer c.SetReadDeadline(time.Time{})
	deadline := time.Now().Add(total)
	wire := p.Encode()
	buf := make([]byte, 1500)
	for {
		if _, err := c.Write(wire); err != nil {
			return Packet{}, err
		}
		next := time.Now().Add(retryInitial)
		if next.After(deadline) {
			next = deadline
		}
		c.SetReadDeadline(next)
		for {
			n, err := c.Read(buf)
			if err != nil {
				var ne net.Error
				if errors.As(err, &ne) && ne.Timeout() {
					break
				}
				return Packet{}, err
			}
			r, ok := Decode(buf[:n])
			if ok && r.Kind == p.Kind|responseBit && r.Seq == p.Seq &&
				(p.Kind == TypeInfo || r.Conn == p.Conn) {
				return r, resultErr(r.Body)
			}
		}
		if err := ctx.Err(); err != nil {
			return Packet{}, err
		}
		if !time.Now().Before(deadline) {
			return Packet{}, ErrResultUnknown
		}
	}
}

var fullCanvas = []byte{1, 144, 1, 44, 0, 0, 0, 0, 0, 0, 0, 0} // 400, 300, no reserved area

func parseInfo(p Packet, addr string) (Info, error) {
	b := p.Body
	bad := fmt.Errorf("%w: INFO", ErrBadResponse)
	if p.Conn == 0 || len(b) < 3 {
		return Info{}, bad
	}
	n := int(b[2])
	if len(b) < 4+n {
		return Info{}, bad
	}
	id := string(b[3 : 3+n])
	m := int(b[3+n])
	if len(b) < 4+n+m {
		return Info{}, bad
	}
	model := string(b[4+n : 4+n+m])
	if !bytes.Equal(b[4+n+m:], fullCanvas) {
		return Info{}, fmt.Errorf("zectrix: device does not expose the full 400x300 canvas; firmware 3.7.2+ required")
	}
	return Info{Address: addr, ID: id, Model: model, Manage: b[1], Conn: p.Conn}, nil
}

// QueryInfo reads device identity without connecting or taking ownership.
func QueryInfo(ctx context.Context, addr string) (Info, error) {
	c, addr, err := dial(addr)
	if err != nil {
		return Info{}, err
	}
	defer c.Close()
	r, err := exchange(ctx, c, Packet{Kind: TypeInfo, Seq: 1}, 2*time.Second)
	if err != nil {
		return Info{}, err
	}
	return parseInfo(r, addr)
}

type pendingKey struct {
	kind uint8
	seq  uint32
}

type resultKey struct {
	object uint8 // 1 image, 2 playback
	id     uint32
}

// Device is one managed connection. A lost connection is terminal; call Open
// again explicitly to reconnect. Always call Close on normal exit.
type Device struct {
	Info Info

	udp  *net.UDPConn
	conn uint64

	control atomic.Uint32 // next control sequence
	ping    atomic.Uint32
	query   atomic.Uint32

	pendMu  sync.Mutex
	pending map[pendingKey]chan Packet

	lastPing atomic.Int64 // unix nanos of last successful PING

	statusMu sync.Mutex
	closeMu  sync.Mutex
	// cmdMu keeps one state-changing control command in flight (CLOSE may jump it).
	cmdMu sync.Mutex
	// imageMu serialises image uploads.
	imageMu sync.Mutex
	// audioMu serialises playback.
	audioMu  sync.Mutex
	playback atomic.Uint32

	resMu    sync.Mutex
	results  map[resultKey]uint8
	resOrder []resultKey

	events chan KeyEvent
	onPing func(PingResult)
	done   chan struct{}
	once   sync.Once
	reason CloseReason
	local  bool

	seenMu sync.Mutex
	seen   map[pendingKey]struct{}
	order  []pendingKey
}

// PingResult reports one keepalive round trip.
type PingResult struct {
	Seq uint32
	RTT time.Duration
	Err error // nil = acknowledged
}

// Options configures OpenWithOptions.
type Options struct {
	// ClientName is shown on the device; 1..48 bytes.
	ClientName string
	// OnPing, if set, is called after every keepalive PING (from a background
	// goroutine). It must not block.
	OnPing func(PingResult)
}

// Open queries INFO, verifies a Note 4 with the full canvas, sends CONNECT and
// starts keepalive. It never connects to any address other than addr.
func Open(ctx context.Context, addr, clientName string) (*Device, error) {
	return OpenWithOptions(ctx, addr, Options{ClientName: clientName})
}

// OpenWithOptions is Open with optional hooks.
func OpenWithOptions(ctx context.Context, addr string, opts Options) (*Device, error) {
	clientName := opts.ClientName
	if clientName == "" || len(clientName) > 48 {
		return nil, fmt.Errorf("zectrix: client name must be 1..48 bytes")
	}
	c, addr, err := dial(addr)
	if err != nil {
		return nil, err
	}
	r, err := exchange(ctx, c, Packet{Kind: TypeInfo, Seq: 1}, 2*time.Second)
	if err != nil {
		c.Close()
		return nil, fmt.Errorf("zectrix: INFO %s: %w", addr, err)
	}
	info, err := parseInfo(r, addr)
	if err != nil {
		c.Close()
		return nil, err
	}
	if info.Model != Note4Model {
		c.Close()
		return nil, fmt.Errorf("%w: model %q, want %s", ErrWrongModel, info.Model, Note4Model)
	}
	if info.Busy() {
		c.Close()
		return nil, ErrBusy
	}
	body := append([]byte{byte(len(clientName))}, clientName...)
	_, err = exchange(ctx, c, Packet{Kind: TypeConnect, Conn: info.Conn, Seq: 1, Body: body}, requestDeadline)
	if err != nil {
		// The device may have accepted; release it best-effort.
		c.Write(Packet{Kind: TypeClose, Conn: info.Conn, Seq: 2, Body: []byte{0}}.Encode())
		c.Close()
		return nil, fmt.Errorf("zectrix: CONNECT: %w", err)
	}
	d := &Device{
		Info:    info,
		udp:     c,
		conn:    info.Conn,
		pending: map[pendingKey]chan Packet{},
		events:  make(chan KeyEvent, 16),
		done:    make(chan struct{}),
		seen:    map[pendingKey]struct{}{},
		results: map[resultKey]uint8{},
		onPing:  opts.OnPing,
	}
	d.control.Store(2)
	d.ping.Store(1)
	d.query.Store(1)
	d.lastPing.Store(time.Now().UnixNano())
	go d.receive()
	go d.keepalive()
	return d, nil
}

func next(counter *atomic.Uint32) uint32 { return counter.Add(1) - 1 }

// Done is closed when the session ends for any reason.
func (d *Device) Done() <-chan struct{} { return d.done }

// Reason is valid after Done is closed. LocalClose reports whether Close ended it.
func (d *Device) Reason() (reason CloseReason, localClose bool) { return d.reason, d.local }

// Keys delivers key events. Events are dropped if the channel is full.
func (d *Device) Keys() <-chan KeyEvent { return d.events }

func (d *Device) closed() bool {
	select {
	case <-d.done:
		return true
	default:
		return false
	}
}

func (d *Device) request(ctx context.Context, kind uint8, seq uint32, body []byte) (Packet, error) {
	key := pendingKey{kind | responseBit, seq}
	ch := make(chan Packet, 1)
	d.pendMu.Lock()
	d.pending[key] = ch
	d.pendMu.Unlock()
	defer func() {
		d.pendMu.Lock()
		delete(d.pending, key)
		d.pendMu.Unlock()
	}()
	wire := Packet{Kind: kind, Conn: d.conn, Seq: seq, Body: body}.Encode()
	deadline := time.Now().Add(requestDeadline)
	delay := retryInitial
	for {
		if d.closed() {
			return Packet{}, ErrDisconnected
		}
		if _, err := d.udp.Write(wire); err != nil {
			return Packet{}, err
		}
		wait := time.Until(deadline)
		if delay < wait {
			wait = delay
		}
		timer := time.NewTimer(wait)
		select {
		case p := <-ch:
			timer.Stop()
			return p, resultErr(p.Body)
		case <-d.done:
			timer.Stop()
			return Packet{}, ErrDisconnected
		case <-ctx.Done():
			timer.Stop()
			return Packet{}, ctx.Err()
		case <-timer.C:
		}
		if !time.Now().Before(deadline) {
			return Packet{}, ErrResultUnknown
		}
		delay = min(delay*2, retryMax)
	}
}

// Status queries battery, charging and Wi-Fi RSSI. Read-only; query at most
// every 30 s. A concurrent call returns ErrBusy.
func (d *Device) Status(ctx context.Context) (Status, error) {
	if d.closed() {
		return Status{}, ErrDisconnected
	}
	if !d.statusMu.TryLock() {
		return Status{}, ErrBusy
	}
	defer d.statusMu.Unlock()
	ctx, cancel := context.WithTimeout(ctx, statusWait)
	defer cancel()
	p, err := d.request(ctx, TypeStatus, next(&d.query), nil)
	if errors.Is(err, context.DeadlineExceeded) {
		return Status{}, fmt.Errorf("zectrix: status query timed out (check connection / firmware): %w", err)
	}
	if err != nil {
		return Status{}, err
	}
	return parseStatus(p.Body)
}

func parseStatus(b []byte) (Status, error) {
	if len(b) != 4 {
		return Status{}, fmt.Errorf("%w: STATUS length %d", ErrBadResponse, len(b))
	}
	var s Status
	switch {
	case b[1] <= 100:
		s.Battery, s.BatteryKnown = b[1], true
	case b[1] != 255:
		return Status{}, fmt.Errorf("%w: battery %d", ErrBadResponse, b[1])
	}
	switch b[2] {
	case 0, 1:
		s.Charging, s.ChargingKnown = b[2] == 1, true
	case 255:
	default:
		return Status{}, fmt.Errorf("%w: charging %d", ErrBadResponse, b[2])
	}
	switch r := int8(b[3]); {
	case r >= -127 && r <= 0:
		s.RSSI, s.RSSIKnown = r, true
	case r != 127:
		return Status{}, fmt.Errorf("%w: rssi %d", ErrBadResponse, r)
	}
	return s, nil
}

// Close sends CLOSE (waiting up to ~2 s for the ack) and ends the session.
// Safe to call repeatedly and after the device has disconnected.
func (d *Device) Close() error {
	d.closeMu.Lock()
	defer d.closeMu.Unlock()
	if d.closed() {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), closeWait)
	defer cancel()
	_, err := d.request(ctx, TypeClose, next(&d.control), []byte{0})
	d.shutdown(ReasonNormal, true, false)
	if err != nil && !errors.Is(err, ErrStale) {
		return fmt.Errorf("zectrix: CLOSE not acknowledged: %w", err)
	}
	return nil
}

// shutdown ends the session once. For remote or timeout endings a final
// best-effort CLOSE is still sent with the old connection id.
func (d *Device) shutdown(reason CloseReason, local, sendClose bool) {
	d.once.Do(func() {
		d.reason, d.local = reason, local
		close(d.done)
		if sendClose {
			seq := next(&d.control)
			d.udp.Write(Packet{Kind: TypeClose, Conn: d.conn, Seq: seq, Body: []byte{0}}.Encode())
		}
		d.udp.Close()
	})
}

func (d *Device) keepalive() {
	ticker := time.NewTicker(pingInterval)
	defer ticker.Stop()
	check := time.NewTicker(time.Second)
	defer check.Stop()
	var inflight atomic.Bool
	ping := func() {
		if !inflight.CompareAndSwap(false, true) {
			return
		}
		go func() {
			defer inflight.Store(false)
			ctx, cancel := context.WithTimeout(context.Background(), pingWait)
			defer cancel()
			seq, start := next(&d.ping), time.Now()
			_, err := d.request(ctx, TypePing, seq, nil)
			if err == nil {
				d.lastPing.Store(time.Now().UnixNano())
			}
			if d.onPing != nil && !d.closed() {
				d.onPing(PingResult{Seq: seq, RTT: time.Since(start), Err: err})
			}
		}()
	}
	ping()
	for {
		select {
		case <-d.done:
			return
		case <-ticker.C:
			ping()
		case <-check.C:
			if time.Since(time.Unix(0, d.lastPing.Load())) >= keepaliveTimeout {
				d.shutdown(ReasonKeepaliveTimeout, false, true)
				return
			}
		}
	}
}

func validEvent(p Packet) bool {
	b := p.Body
	switch p.Kind {
	case TypeKey:
		return len(b) == 2 && b[0] >= 1 && b[0] <= 3 && b[1] >= 1 && b[1] <= 2 && !(b[0] == 3 && b[1] == 2)
	case TypeResult:
		return len(b) == 6 && b[4] >= 1 && b[4] <= 2 && b[5] <= 7
	case TypeClose:
		return len(b) == 1 && b[0] <= 7
	case TypeAudioBegin:
		return len(b) == 1 && b[0] == 0 && p.Seq > 0
	case TypeAudioEnd:
		return len(b) == 11 && b[10] <= 7
	case TypeAudioData:
		return len(b) >= 5 && len(b) <= 1184
	}
	return false
}

// firstSeen records device events so retransmits are acked but delivered once.
func (d *Device) firstSeen(k pendingKey) bool {
	d.seenMu.Lock()
	defer d.seenMu.Unlock()
	if _, ok := d.seen[k]; ok {
		return false
	}
	d.seen[k] = struct{}{}
	d.order = append(d.order, k)
	if len(d.order) > 256 {
		delete(d.seen, d.order[0])
		d.order = d.order[1:]
	}
	return true
}

func (d *Device) receive() {
	buf := make([]byte, 1500)
	for {
		n, err := d.udp.Read(buf)
		if err != nil {
			d.shutdown(ReasonNetwork, false, false)
			return
		}
		p, ok := Decode(buf[:n])
		if !ok || p.Conn != d.conn {
			continue
		}
		if p.Kind&responseBit != 0 {
			d.pendMu.Lock()
			ch := d.pending[pendingKey{p.Kind, p.Seq}]
			d.pendMu.Unlock()
			if ch != nil {
				select {
				case ch <- p:
				default:
				}
			}
			if len(p.Body) > 0 && p.Body[0] == 3 { // STALE
				d.shutdown(ReasonDeviceExit, false, false)
				return
			}
			continue
		}
		if !validEvent(p) {
			continue
		}
		if p.Kind == TypeAudioData {
			continue // recordings are not handled by this minimal client
		}
		// Every device event is acked, including duplicates.
		d.udp.Write(Packet{Kind: p.Kind | responseBit, Conn: d.conn, Seq: p.Seq, Body: []byte{0}}.Encode())
		if !d.firstSeen(pendingKey{p.Kind, p.Seq}) {
			continue
		}
		switch p.Kind {
		case TypeClose:
			d.shutdown(CloseReason(p.Body[0]), false, true)
			return
		case TypeResult:
			d.storeResult(p.Body[4], binary.BigEndian.Uint32(p.Body), p.Body[5])
		case TypeAudioBegin:
			if id := d.playback.Load(); id != 0 {
				d.storeResult(2, id, uint8(ReasonInterrupted))
			}
		case TypeKey:
			select {
			case d.events <- KeyEvent{Key: Key(p.Body[0]), Long: p.Body[1] == 2}:
			default:
			}
		}
	}
}
