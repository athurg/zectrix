// Command zectrix-discover lists Note 4 devices using mDNS.
package main

import (
	"context"
	"flag"
	"fmt"
	"time"

	"github.com/athurg/zectrix"
	"github.com/athurg/zectrix/examples/internal/cli"
)

func main() {
	cli.Run(run)
}

func run(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("zectrix-discover", flag.ExitOnError)
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
