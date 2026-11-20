// Command zectrix-push sends a Note 4 notification or starts a web push server.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"net"
	"net/http"
	"os"
	"strconv"
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
	web := fs.Bool("web", false, "start the web server with these push defaults")
	listen := fs.String("listen", defaultListen, "HTTP listen address in web mode")
	fontPath := fs.String("font", "", "font file (default: embedded Zfull-GB)")
	imagePath := fs.String("image", "", "image file to push (PNG, JPEG or GIF)")
	req := defaultPushRequest()
	fs.StringVar(&req.Device, "device", req.Device, "device IPv4 address, IPv4:port or 12-hex ID")
	fs.StringVar(&req.Title, "title", req.Title, "optional title for text pushes")
	fs.StringVar(&req.Text, "text", req.Text, "body text to push")
	fs.StringVar(&req.Footer, "footer", req.Footer, "footer text (empty hides the footer)")
	fs.StringVar(&req.Sound, "sound", req.Sound, "prompt name or ID")
	fs.IntVar(&req.Volume, "volume", req.Volume, "playback volume (0..100)")
	fs.IntVar(&req.Hold, "hold", req.Hold, "seconds to display the page (0 waits for a device key)")
	fs.IntVar(&req.TitleSize, "title-size", req.TitleSize, "title font size in pixels")
	fs.IntVar(&req.BodySize, "body-size", req.BodySize, "body font size in pixels")
	fs.Parse(args)
	if fs.NArg() != 0 {
		return errors.New("unexpected positional arguments")
	}
	if *imagePath != "" {
		textSet := false
		fs.Visit(func(option *flag.Flag) {
			if option.Name == "text" {
				textSet = true
			}
		})
		if !textSet {
			req.Text = ""
		}
		data, err := os.ReadFile(*imagePath)
		if err != nil {
			return err
		}
		req.Image, err = decodeImage(data)
		if err != nil {
			return fmt.Errorf("%s: %w", *imagePath, err)
		}
	}
	req, err := normalizePushRequest(req, !*web)
	if err != nil {
		return err
	}
	if req.Device != "" || !*web {
		req.Device, err = cli.ResolveDevice(ctx, req.Device)
		if err != nil {
			return err
		}
		if _, err := zectrix.NormalizeAddress(req.Device); err != nil {
			return err
		}
	}
	font, err := zectrix.LoadFont(*fontPath)
	if err != nil {
		return err
	}
	if *web {
		return serveWeb(ctx, *listen, newWebServer(ctx, req, font).routes())
	}
	if err := pushDevice(ctx, req, font, "Go Push Demo"); err != nil {
		return err
	}
	fmt.Printf("Pushed to %s\n", req.Device)
	return nil
}

func serveWeb(ctx context.Context, listen string, handler http.Handler) error {
	listener, err := net.Listen("tcp", listen)
	if err != nil {
		return err
	}
	server := &http.Server{Handler: handler, ReadHeaderTimeout: 5 * time.Second}
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
