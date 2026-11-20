// Command zectrix-alert plays the built-in Note 4 prompt sounds.
package main

import (
	"context"
	"errors"
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

func run(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("zectrix-alert", flag.ExitOnError)
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
	addr, err := cli.ResolveDevice(ctx, *device)
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
