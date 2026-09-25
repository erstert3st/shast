package catalog

import (
	"bytes"
	"embed"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"strings"

	"go.yaml.in/yaml/v3"

	"shast/internal/outcmp"
)

const (
	catalogFile = "commands.yaml"
	expectedDir = "expected"
	expectedExt = ".txt"
)

//go:embed commands.yaml expected
var embedded embed.FS

// rawChallenge mirrors one entry of commands.yaml.
type rawChallenge struct {
	ID            string     `yaml:"id"`
	Command       string     `yaml:"command"`
	Category      string     `yaml:"category"`
	Difficulty    string     `yaml:"difficulty"`
	Explanation   string     `yaml:"explanation"`
	Deterministic *bool      `yaml:"deterministic"`
	Compare       rawCompare `yaml:"compare"`
	ExitCode      int        `yaml:"exit_code"`
	AllowEmpty    bool       `yaml:"allow_empty"`
}

type rawCompare struct {
	IgnoreOrder bool `yaml:"ignore_order"`
}

func (r rawChallenge) challenge() Challenge {
	deterministic := r.Deterministic == nil || *r.Deterministic
	return Challenge{
		ID:            r.ID,
		Command:       r.Command,
		Category:      r.Category,
		Difficulty:    Difficulty(r.Difficulty),
		Explanation:   r.Explanation,
		Deterministic: deterministic,
		Compare:       outcmp.Options{IgnoreOrder: r.Compare.IgnoreOrder},
		ExitCode:      r.ExitCode,
		AllowEmpty:    r.AllowEmpty,
	}
}

// Default loads the catalog embedded in the binary.
func Default() ([]Challenge, error) {
	cs, err := Load(embedded)
	if err != nil {
		return nil, fmt.Errorf("embedded catalog: %w", err)
	}
	return cs, nil
}

// LoadWithOverlay loads the embedded catalog and, if dir is not empty,
// merges the catalog in dir on top of it (see Merge).
func LoadWithOverlay(dir string) ([]Challenge, error) {
	base, err := Default()
	if err != nil {
		return nil, err
	}
	if dir == "" {
		return base, nil
	}
	overlay, err := Load(os.DirFS(dir))
	if err != nil {
		return nil, fmt.Errorf("catalog %s: %w", dir, err)
	}
	merged := Merge(base, overlay)
	if err := Validate(merged); err != nil {
		return nil, fmt.Errorf("merged catalog: %w", err)
	}
	return merged, nil
}

// Load reads the catalog from fsys: the required file commands.yaml and an
// optional expected/<id>.txt per challenge. The challenges are validated and
// returned in file order.
func Load(fsys fs.FS) ([]Challenge, error) {
	data, err := fs.ReadFile(fsys, catalogFile)
	if err != nil {
		return nil, fmt.Errorf("read catalog: %w", err)
	}
	cs, err := parse(data)
	if err != nil {
		return nil, fmt.Errorf("parse %s: %w", catalogFile, err)
	}
	if err := Validate(cs); err != nil {
		return nil, fmt.Errorf("invalid %s: %w", catalogFile, err)
	}
	if err := attachExpected(fsys, cs); err != nil {
		return nil, err
	}
	return cs, nil
}

// parse decodes the YAML catalog strictly: unknown fields and additional
// documents are errors.
func parse(data []byte) ([]Challenge, error) {
	dec := yaml.NewDecoder(bytes.NewReader(data))
	dec.KnownFields(true)
	var raws []rawChallenge
	if err := dec.Decode(&raws); err != nil && !errors.Is(err, io.EOF) {
		return nil, err
	}
	if len(raws) == 0 {
		return nil, errors.New("no challenges defined")
	}
	if err := dec.Decode(new(yaml.Node)); !errors.Is(err, io.EOF) {
		return nil, errors.New("want a single YAML document with a list of challenges")
	}
	cs := make([]Challenge, len(raws))
	for i, r := range raws {
		cs[i] = r.challenge()
	}
	return cs, nil
}

// attachExpected sets Expected from expected/<id>.txt. Files for unknown IDs
// are rejected as stale; files without the .txt extension are ignored.
func attachExpected(fsys fs.FS, cs []Challenge) error {
	entries, err := fs.ReadDir(fsys, expectedDir)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("read expected outputs: %w", err)
	}
	index := make(map[string]int, len(cs))
	for i, c := range cs {
		index[c.ID] = i
	}
	var errs []error
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || path.Ext(name) != expectedExt {
			continue
		}
		file := path.Join(expectedDir, name)
		i, ok := index[strings.TrimSuffix(name, expectedExt)]
		if !ok {
			errs = append(errs, fmt.Errorf("stale expected output %s: no challenge with that id", file))
			continue
		}
		data, err := fs.ReadFile(fsys, file)
		if err != nil {
			errs = append(errs, fmt.Errorf("read expected output: %w", err))
			continue
		}
		cs[i].Expected = string(data)
	}
	return errors.Join(errs...)
}
