package cli

import (
	"context"
	"errors"
	"time"

	"github.com/athurg/zectrix"
)

// ResolveDevice accepts an IPv4 address, an IPv4 address with a port, or a device ID.
func ResolveDevice(ctx context.Context, device string) (string, error) {
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
