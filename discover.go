package zectrix

import (
	"context"
	"fmt"
	"net"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/grandcat/zeroconf"
)

// Found is one device seen via mDNS. Discovery is read-only: nothing is sent
// to the device itself.
type Found struct {
	ID          string
	Model       string
	WireVersion string
	Addresses   []string // IPv4:port
}

// Discover browses _zectrix._udp.local. for the given window.
func Discover(ctx context.Context, window time.Duration) ([]Found, error) {
	resolver, err := zeroconf.NewResolver(zeroconf.SelectIPTraffic(zeroconf.IPv4))
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(ctx, window)
	defer cancel()
	entries := make(chan *zeroconf.ServiceEntry, 16)
	if err := resolver.Browse(ctx, "_zectrix._udp", "local.", entries); err != nil {
		return nil, err
	}
	byID := map[string]*Found{}
	for {
		select {
		case <-ctx.Done():
			return collect(byID), nil
		case e, ok := <-entries:
			if !ok {
				return collect(byID), nil
			}
			txt := map[string]string{}
			for _, kv := range e.Text {
				if k, v, ok := strings.Cut(kv, "="); ok {
					txt[k] = v
				}
			}
			id := strings.ToLower(txt["id"])
			if id == "" {
				continue
			}
			f := byID[id]
			if f == nil {
				f = &Found{ID: id, Model: txt["model"], WireVersion: txt["version"]}
				byID[id] = f
			}
			for _, ip := range e.AddrIPv4 {
				addr := net.JoinHostPort(ip.String(), strconv.Itoa(e.Port))
				if !contains(f.Addresses, addr) {
					f.Addresses = append(f.Addresses, addr)
				}
			}
		}
	}
}

// ResolveID finds the address of one device by its 12-hex id.
func ResolveID(ctx context.Context, id string, window time.Duration) (string, error) {
	found, err := Discover(ctx, window)
	if err != nil {
		return "", err
	}
	for _, f := range found {
		if strings.EqualFold(f.ID, id) && len(f.Addresses) > 0 {
			return f.Addresses[0], nil
		}
	}
	return "", fmt.Errorf("zectrix: device %s not found via mDNS", id)
}

func collect(m map[string]*Found) []Found {
	out := make([]Found, 0, len(m))
	for _, f := range m {
		out = append(out, *f)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

func contains(s []string, v string) bool {
	for _, x := range s {
		if x == v {
			return true
		}
	}
	return false
}
