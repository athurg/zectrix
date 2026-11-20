// Command zectrix provides Note 4 device tools and the local HTTP demo.
//
//	zectrix discover [-t 5s]
//	zectrix info    -device <IPv4[:port] | 12-hex id>
//	zectrix status  -device <IPv4[:port] | 12-hex id> [-interval 30s] [-name "Go Client"]
//	zectrix prompts -device <addr> [-volume 100] [-interval 1s]
//	zectrix demo    -device <addr> [-listen 127.0.0.1:8080] [-font F]
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	zectrix "github.com/athurg/zectrix"
)

const minInterval = 30 * time.Second

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(2)
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	var err error
	switch os.Args[1] {
	case "discover":
		err = cmdDiscover(ctx, os.Args[2:])
	case "info":
		err = cmdInfo(ctx, os.Args[2:])
	case "status":
		err = cmdStatus(ctx, os.Args[2:])
	case "prompts":
		err = cmdPrompts(ctx, os.Args[2:])
	case "demo":
		err = cmdDemo(ctx, os.Args[2:])
	case "-h", "--help", "help":
		usage()
		return
	default:
		usage()
		os.Exit(2)
	}
	if err != nil && !errors.Is(err, context.Canceled) {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

func usage() {
	fmt.Fprint(os.Stderr, `usage:
  zectrix discover [-t 5s]                 list devices via mDNS (read-only)
  zectrix info -device ADDR                read INFO without connecting
  zectrix status -device ADDR [-interval 30s] [-name NAME]
                                           connect, query status, CLOSE on exit
  zectrix prompts -device ADDR [-volume 100] [-interval 1s]
                                           play every built-in prompt once, printing its ID
  zectrix demo -device ADDR [-listen 127.0.0.1:8080] [-font F]
                                           serve the web page and HTTP push API
ADDR is IPv4, IPv4:port or a 12-hex device id.
`)
}

func cmdDiscover(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("discover", flag.ExitOnError)
	window := fs.Duration("t", 5*time.Second, "browse window")
	fs.Parse(args)
	found, err := zectrix.Discover(ctx, *window)
	if err != nil {
		return err
	}
	if len(found) == 0 {
		fmt.Println("no devices found")
		return nil
	}
	for _, f := range found {
		fmt.Printf("id=%s model=%s version=%s addr=%v\n", f.ID, f.Model, f.WireVersion, f.Addresses)
	}
	return nil
}

func resolve(ctx context.Context, device string) (string, error) {
	if device == "" {
		return "", errors.New("-device is required")
	}
	if len(device) == 12 {
		if _, err := zectrix.NormalizeAddress(device); err != nil {
			return zectrix.ResolveID(ctx, device, 5*time.Second)
		}
	}
	return device, nil
}

func cmdInfo(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("info", flag.ExitOnError)
	device := fs.String("device", "", "device address or id")
	fs.Parse(args)
	addr, err := resolve(ctx, *device)
	if err != nil {
		return err
	}
	info, err := zectrix.QueryInfo(ctx, addr)
	if err != nil {
		return err
	}
	fmt.Printf("id=%s model=%s addr=%s manage=%d busy=%v\n",
		info.ID, info.Model, info.Address, info.Manage, info.Busy())
	return nil
}

func cmdStatus(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("status", flag.ExitOnError)
	device := fs.String("device", "", "device address or id")
	interval := fs.Duration("interval", 0, "repeat status query (>= 30s); 0 = query once")
	name := fs.String("name", "Go Client", "client name shown on the device")
	fs.Parse(args)
	if *interval != 0 && *interval < minInterval {
		return fmt.Errorf("-interval must be at least %s", minInterval)
	}
	addr, err := resolve(ctx, *device)
	if err != nil {
		return err
	}
	d, err := zectrix.OpenWithOptions(ctx, addr, zectrix.Options{ClientName: *name, OnPing: logPing})
	if err != nil {
		return err
	}
	defer func() {
		if err := d.Close(); err != nil {
			fmt.Fprintln(os.Stderr, "close:", err)
		} else {
			fmt.Println("closed")
		}
	}()
	fmt.Printf("connected id=%s model=%s addr=%s\n", d.Info.ID, d.Info.Model, d.Info.Address)

	query := func() {
		s, err := d.Status(ctx)
		if err != nil {
			fmt.Fprintln(os.Stderr, "status:", err)
			return
		}
		fmt.Printf("%s %s\n", time.Now().Format("15:04:05"), s)
	}
	query()
	if *interval == 0 {
		return nil
	}
	ticker := time.NewTicker(*interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			fmt.Println("\nshutting down")
			return nil
		case <-d.Done():
			reason, _ := d.Reason()
			return fmt.Errorf("device disconnected: %s (reconnect explicitly)", reason)
		case k := <-d.Keys():
			fmt.Printf("key %s long=%v\n", k.Key, k.Long)
		case <-ticker.C:
			query()
		}
	}
}

func cmdPrompts(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("prompts", flag.ExitOnError)
	device := fs.String("device", "", "device address or id")
	volume := fs.Uint("volume", 100, "temporary playback volume, 0..100")
	interval := fs.Duration("interval", time.Second, "pause between prompts")
	fs.Parse(args)
	if *device == "" {
		return errors.New("-device is required")
	}
	if *volume > 100 {
		return errors.New("-volume must be 0..100")
	}
	if *interval < 0 {
		return errors.New("-interval cannot be negative")
	}
	addr, err := resolve(ctx, *device)
	if err != nil {
		return err
	}
	d, err := zectrix.Open(ctx, addr, "Go Prompt Test")
	if err != nil {
		return err
	}
	defer func() {
		if err := d.Close(); err != nil {
			fmt.Fprintln(os.Stderr, "close:", err)
		} else {
			fmt.Println("closed")
		}
	}()
	for _, prompt := range zectrix.Prompts() {
		fmt.Printf("prompt playing id=%s name=%s duration=%s volume=%d\n",
			prompt.ID, prompt.Name, prompt.Duration.Round(time.Millisecond), *volume)
		start := time.Now()
		if err := d.PlayPrompt(ctx, prompt.ID, uint8(*volume)); err != nil {
			return fmt.Errorf("prompt id=%s: %w", prompt.ID, err)
		}
		fmt.Printf("prompt done id=%s name=%s elapsed=%s\n",
			prompt.ID, prompt.Name, time.Since(start).Round(time.Millisecond))
		select {
		case <-time.After(*interval):
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	return nil
}

func logPing(r zectrix.PingResult) {
	ts := time.Now().Format("15:04:05")
	if r.Err != nil {
		fmt.Fprintf(os.Stderr, "%s heartbeat seq=%d failed after %s: %v\n", ts, r.Seq, r.RTT.Round(time.Millisecond), r.Err)
		return
	}
	fmt.Printf("%s heartbeat seq=%d ok rtt=%s\n", ts, r.Seq, r.RTT.Round(time.Microsecond))
}
