// Command zectrix-push serves a Note 4 web demo and HTTP push API.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/athurg/zectrix"
	"github.com/athurg/zectrix/examples/internal/cli"
)

func main() {
	cli.Run(run)
}

var defaultListen = net.JoinHostPort("127.0.0.1", strconv.Itoa(zectrix.DefaultPort))

func run(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("zectrix-push", flag.ExitOnError)
	device := fs.String("device", "", "optional default device address or id")
	listen := fs.String("listen", defaultListen, "HTTP listen address")
	fontPath := fs.String("font", "", "font file (default: embedded Zfull-GB)")
	fs.Parse(args)
	addr := strings.TrimSpace(*device)
	if addr != "" {
		resolved, err := cli.ResolveDevice(ctx, addr)
		if err != nil {
			return err
		}
		addr = resolved
	}
	font, err := zectrix.LoadFont(*fontPath)
	if err != nil {
		return err
	}
	web := newWebServer(ctx, addr, font)
	listener, err := net.Listen("tcp", *listen)
	if err != nil {
		return err
	}
	server := &http.Server{Handler: web.routes(), ReadHeaderTimeout: 5 * time.Second}
	errCh := make(chan error, 1)
	go func() { errCh <- server.Serve(listener) }()
	fmt.Printf("ZECTRIX web demo: http://%s\n", listener.Addr())
	select {
	case err := <-errCh:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		return server.Shutdown(shutdownCtx)
	}
}
