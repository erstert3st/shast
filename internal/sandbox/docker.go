// Package sandbox runs catalog commands inside an isolated Docker container.
//
// The sandbox image is built from an embedded context (see image/). A
// session owns one long-lived container whose writable workspace /work is
// reset from the read-only seed /seed before every command.
package sandbox

import (
	"context"
	"fmt"
	"time"

	"github.com/moby/moby/client"
)

// pingTimeout bounds the initial connection check, so an unreachable
// daemon is reported quickly.
const pingTimeout = 5 * time.Second

// DaemonError reports that the Docker daemon cannot be reached.
type DaemonError struct {
	Err error
}

func (e *DaemonError) Error() string {
	return fmt.Sprintf("Docker daemon not reachable: %v (is Docker running? are you in the docker group?)", e.Err)
}

func (e *DaemonError) Unwrap() error { return e.Err }

// Docker is a connection to the Docker daemon.
type Docker struct {
	api *client.Client
}

// Connect connects to the Docker daemon configured by the environment
// (DOCKER_HOST etc.) and checks that it responds. Failures are reported as
// *DaemonError.
func Connect(ctx context.Context) (*Docker, error) {
	api, err := client.New(client.FromEnv)
	if err != nil {
		return nil, &DaemonError{Err: err}
	}
	ctx, cancel := context.WithTimeout(ctx, pingTimeout)
	defer cancel()
	if _, err := api.Ping(ctx, client.PingOptions{NegotiateAPIVersion: true}); err != nil {
		api.Close()
		return nil, &DaemonError{Err: err}
	}
	return &Docker{api: api}, nil
}

// Close releases the client connection.
func (d *Docker) Close() error {
	return d.api.Close()
}

// Preflight checks that the sandbox image for this binary is present.
// A missing or stale image is reported as *ImageMissingError.
func (d *Docker) Preflight(ctx context.Context) error {
	tag := ImageTag()
	ok, err := d.ImageExists(ctx, tag)
	if err != nil {
		return err
	}
	if !ok {
		return &ImageMissingError{Tag: tag}
	}
	return nil
}
