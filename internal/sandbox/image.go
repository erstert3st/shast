package sandbox

import (
	"archive/tar"
	"bytes"
	"context"
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"strings"
	"sync"
	"time"

	cerrdefs "github.com/containerd/errdefs"
	"github.com/moby/moby/api/types/jsonstream"
	"github.com/moby/moby/client"
)

// imageRepo is the repository name of the sandbox image.
const imageRepo = "shast-sandbox"

// imageDir holds the Docker build context: Dockerfile, seed.sh, gitconfig.
//
//go:embed image
var imageDir embed.FS

// ImageMissingError reports that the sandbox image is not present locally.
type ImageMissingError struct {
	Tag string
}

func (e *ImageMissingError) Error() string {
	return fmt.Sprintf("sandbox image %s not found – run `shast image build`", e.Tag)
}

// ImageTag returns the tag of the sandbox image that matches the embedded
// build context. Any change to the context yields a new tag, so a stale
// image is detected as missing.
func ImageTag() string { return imageTag() }

var imageTag = sync.OnceValue(func() string {
	sum, err := contextHash(imageDir)
	if err != nil {
		// The context is embedded at compile time; failing to read it is a
		// programming error.
		panic(fmt.Sprintf("sandbox: hash image context: %v", err))
	}
	return imageRepo + ":" + sum[:12]
})

// contextHash returns the hex sha256 over all file paths and contents in
// fsys below "image", in lexical order.
func contextHash(fsys fs.FS) (string, error) {
	h := sha256.New()
	err := fs.WalkDir(fsys, "image", func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		data, err := fs.ReadFile(fsys, p)
		if err != nil {
			return err
		}
		fmt.Fprintf(h, "%s\x00%d\x00", p, len(data))
		h.Write(data)
		return nil
	})
	if err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// contextTar returns the build context as a tar archive with fixed
// metadata, so the archive depends only on file paths and contents.
func contextTar(fsys fs.FS) (*bytes.Buffer, error) {
	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)
	err := fs.WalkDir(fsys, "image", func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		data, err := fs.ReadFile(fsys, p)
		if err != nil {
			return err
		}
		name, _ := strings.CutPrefix(p, "image/")
		hdr := &tar.Header{
			Name:    name,
			Mode:    0o644,
			Size:    int64(len(data)),
			ModTime: time.Unix(0, 0),
			Format:  tar.FormatPAX,
		}
		if err := tw.WriteHeader(hdr); err != nil {
			return err
		}
		_, err = tw.Write(data)
		return err
	})
	if err != nil {
		return nil, fmt.Errorf("build context: %w", err)
	}
	if err := tw.Close(); err != nil {
		return nil, fmt.Errorf("build context: %w", err)
	}
	return &buf, nil
}

// BuildImage builds the sandbox image from the embedded context and tags it
// with tag. Build output is written to out. With noCache, no build cache is
// used and the base image is pulled again.
func (d *Docker) BuildImage(ctx context.Context, tag string, noCache bool, out io.Writer) error {
	buildCtx, err := contextTar(imageDir)
	if err != nil {
		return err
	}
	res, err := d.api.ImageBuild(ctx, buildCtx, client.ImageBuildOptions{
		Tags:        []string{tag},
		NoCache:     noCache,
		PullParent:  noCache,
		Remove:      true,
		ForceRemove: true,
	})
	if err != nil {
		return fmt.Errorf("build image %s: %w", tag, err)
	}
	defer res.Body.Close()

	dec := json.NewDecoder(res.Body)
	for {
		var msg jsonstream.Message
		if err := dec.Decode(&msg); errors.Is(err, io.EOF) {
			return nil
		} else if err != nil {
			return fmt.Errorf("build image %s: read progress: %w", tag, err)
		}
		if msg.Error != nil {
			return fmt.Errorf("build image %s: %w", tag, msg.Error)
		}
		text := msg.Stream
		if msg.Status != "" { // e.g. progress of pulling the base image
			text = strings.TrimSpace(msg.ID+" "+msg.Status) + "\n"
		}
		if _, err := io.WriteString(out, text); err != nil {
			return fmt.Errorf("build image %s: write output: %w", tag, err)
		}
	}
}

// ImageExists reports whether the image tag is present locally.
func (d *Docker) ImageExists(ctx context.Context, tag string) (bool, error) {
	_, err := d.api.ImageInspect(ctx, tag)
	if cerrdefs.IsNotFound(err) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("inspect image %s: %w", tag, err)
	}
	return true, nil
}
