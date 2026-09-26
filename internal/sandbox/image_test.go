package sandbox

import (
	"archive/tar"
	"context"
	"errors"
	"flag"
	"io"
	"os"
	"regexp"
	"slices"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/moby/moby/client"
)

func TestContextHash(t *testing.T) {
	base := fstest.MapFS{
		"image/Dockerfile": {Data: []byte("FROM scratch\n")},
		"image/seed.sh":    {Data: []byte("echo hi\n")},
	}
	baseSum, err := contextHash(base)
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name string
		fsys fstest.MapFS
		same bool
	}{
		{"identical", fstest.MapFS{
			"image/Dockerfile": {Data: []byte("FROM scratch\n"), Mode: 0o755},
			"image/seed.sh":    {Data: []byte("echo hi\n"), ModTime: time.Now()},
		}, true},
		{"content changed", fstest.MapFS{
			"image/Dockerfile": {Data: []byte("FROM scratch\n")},
			"image/seed.sh":    {Data: []byte("echo ho\n")},
		}, false},
		{"file renamed", fstest.MapFS{
			"image/Dockerfile": {Data: []byte("FROM scratch\n")},
			"image/seed2.sh":   {Data: []byte("echo hi\n")},
		}, false},
		{"content moved between files", fstest.MapFS{
			"image/Dockerfile": {Data: []byte("FROM scratch\necho hi\n")},
			"image/seed.sh":    {Data: []byte("")},
		}, false},
		{"file added", fstest.MapFS{
			"image/Dockerfile": {Data: []byte("FROM scratch\n")},
			"image/seed.sh":    {Data: []byte("echo hi\n")},
			"image/gitconfig":  {Data: []byte("")},
		}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			sum, err := contextHash(tt.fsys)
			if err != nil {
				t.Fatal(err)
			}
			if got := sum == baseSum; got != tt.same {
				t.Errorf("hash equal = %v, want %v", got, tt.same)
			}
		})
	}
}

func TestImageTag(t *testing.T) {
	tag := ImageTag()
	if !regexp.MustCompile(`^shast-sandbox:[0-9a-f]{12}$`).MatchString(tag) {
		t.Errorf("ImageTag() = %q, want shast-sandbox:<12 hex>", tag)
	}
	if again := ImageTag(); again != tag {
		t.Errorf("ImageTag() not stable: %q vs %q", tag, again)
	}
}

func TestContextTar(t *testing.T) {
	buf, err := contextTar(imageDir)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	tr := tar.NewReader(buf)
	for {
		hdr, err := tr.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		names = append(names, hdr.Name)
		if !hdr.ModTime.Equal(time.Unix(0, 0)) {
			t.Errorf("%s: mtime %v, want epoch", hdr.Name, hdr.ModTime)
		}
	}
	for _, want := range []string{"Dockerfile", "seed.sh", "gitconfig"} {
		if !slices.Contains(names, want) {
			t.Errorf("build context lacks %s, has %v", want, names)
		}
	}
}

func TestImageErrorMessages(t *testing.T) {
	tests := []struct {
		err  error
		want string
	}{
		{&ImageMissingError{Tag: "shast-sandbox:abc"}, "sandbox image shast-sandbox:abc not found – run `shast image build`"},
		{&DaemonError{Err: errors.New("dial unix: no such file")}, "Docker daemon not reachable: dial unix: no such file (is Docker running? are you in the docker group?)"},
	}
	for _, tt := range tests {
		if got := tt.err.Error(); got != tt.want {
			t.Errorf("Error() = %q, want %q", got, tt.want)
		}
	}
}

// dockerOrSkip connects to Docker or skips the test when it is unavailable
// or -short is set.
func dockerOrSkip(t *testing.T) *Docker {
	t.Helper()
	if testing.Short() {
		t.Skip("integration test skipped with -short")
	}
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	d, err := Connect(ctx)
	if err != nil {
		t.Skipf("docker not available: %v", err)
	}
	t.Cleanup(func() { d.Close() })
	return d
}

// TestSeedDeterministic builds the image twice without cache and checks
// that the seed manifests are identical. It takes a few minutes and needs
// network access for apt, so it only runs with SHAST_SEED_REBUILD=1
// (see `make seed-determinism`).
func TestSeedDeterministic(t *testing.T) {
	if os.Getenv("SHAST_SEED_REBUILD") != "1" {
		t.Skip("set SHAST_SEED_REBUILD=1 to rebuild the image twice")
	}
	ctx := t.Context()
	d, err := Connect(ctx) // opted in: an unreachable daemon is a failure
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()

	var manifests []string
	for _, tag := range []string{"shast-sandbox-test:a", "shast-sandbox-test:b"} {
		if err := d.BuildImage(ctx, tag, true, io.Discard); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() {
			_, _ = d.api.ImageRemove(context.Background(), tag, client.ImageRemoveOptions{Force: true})
		})
		m, err := readImageFile(ctx, d, tag, "/seed.sha256")
		if err != nil {
			t.Fatal(err)
		}
		if len(m) == 0 {
			t.Fatalf("%s: empty manifest", tag)
		}
		manifests = append(manifests, m)
	}
	if manifests[0] == manifests[1] {
		return
	}
	t.Error("seed manifests differ between builds")
	a, b := strings.Split(manifests[0], "\n"), strings.Split(manifests[1], "\n")
	for _, l := range a {
		if !slices.Contains(b, l) {
			t.Errorf("only in build a: %s", l)
		}
	}
	for _, l := range b {
		if !slices.Contains(a, l) {
			t.Errorf("only in build b: %s", l)
		}
	}
}

var update = flag.Bool("update", false, "rewrite testdata/seed.sha256 from the current image")

// TestSeedManifestGolden compares the seed manifest of the current image
// with the committed one, so any change of the seed (or of tool versions
// that shape it) is noticed. Run with -update after an intended change.
func TestSeedManifestGolden(t *testing.T) {
	d := dockerOrSkip(t)
	tag := ImageTag()
	if ok, err := d.ImageExists(t.Context(), tag); err != nil {
		t.Fatal(err)
	} else if !ok {
		t.Skipf("image %s not built; run `shast image build`", tag)
	}
	got, err := readImageFile(t.Context(), d, tag, "/seed.sha256")
	if err != nil {
		t.Fatal(err)
	}
	const golden = "testdata/seed.sha256"
	if *update {
		if err := os.WriteFile(golden, []byte(got), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	want, err := os.ReadFile(golden)
	if err != nil {
		t.Fatal(err)
	}
	if got != string(want) {
		t.Errorf("seed manifest of %s differs from %s; run go test -run TestSeedManifestGolden -update if intended", tag, golden)
	}
}

// readImageFile returns the content of a regular file inside image tag.
func readImageFile(ctx context.Context, d *Docker, tag, file string) (string, error) {
	c, err := d.api.ContainerCreate(ctx, client.ContainerCreateOptions{Image: tag})
	if err != nil {
		return "", err
	}
	defer d.api.ContainerRemove(context.Background(), c.ID, client.ContainerRemoveOptions{Force: true})
	res, err := d.api.CopyFromContainer(ctx, c.ID, client.CopyFromContainerOptions{SourcePath: file})
	if err != nil {
		return "", err
	}
	defer res.Content.Close()
	tr := tar.NewReader(res.Content)
	if _, err := tr.Next(); err != nil {
		return "", err
	}
	data, err := io.ReadAll(tr)
	return string(data), err
}
