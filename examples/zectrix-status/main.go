// Command zectrix-status queries a Note 4 and reports its key events.
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"time"

	"github.com/athurg/zectrix"
	"github.com/athurg/zectrix/examples/internal/cli"
)

func main() {
	cli.Run(run)
}

const minInterval = 30 * time.Second

func run(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("zectrix-status", flag.ExitOnError)
	device := fs.String("device", "", "device address or id")
	interval := fs.Duration("interval", 0, "repeat status query (>= 30s); 0 = query once")
	name := fs.String("name", "Go Client", "client name shown on the device")
	fs.Parse(args)
	if *interval != 0 && *interval < minInterval {
		return fmt.Errorf("-interval must be at least %s", minInterval)
	}
	addr, err := cli.ResolveDevice(ctx, *device)
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

func logPing(r zectrix.PingResult) {
	ts := time.Now().Format("15:04:05")
	if r.Err != nil {
		fmt.Fprintf(os.Stderr, "%s heartbeat seq=%d failed after %s: %v\n", ts, r.Seq, r.RTT.Round(time.Millisecond), r.Err)
		return
	}
	fmt.Printf("%s heartbeat seq=%d ok rtt=%s\n", ts, r.Seq, r.RTT.Round(time.Microsecond))
}
