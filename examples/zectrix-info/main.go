// Command zectrix-info reads the identity of a Note 4.
package main

import (
	"context"
	"flag"
	"fmt"

	"github.com/athurg/zectrix"
	"github.com/athurg/zectrix/examples/internal/cli"
)

func main() {
	cli.Run(run)
}

func run(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("zectrix-info", flag.ExitOnError)
	device := fs.String("device", "", "device address or id")
	fs.Parse(args)
	addr, err := cli.ResolveDevice(ctx, *device)
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
