package cmdhelp

import (
	"bytes"
	_ "embed"
	"errors"
	"fmt"
	"io"
	"maps"
	"slices"
	"strings"

	"go.yaml.in/yaml/v3"
)

//go:embed help.yaml
var embedded []byte

// rawCommand mirrors one entry of help.yaml.
type rawCommand struct {
	About   string            `yaml:"about"`
	Options map[string]string `yaml:"options"`
}

// Default returns the dictionary embedded in the binary.
func Default() (Dict, error) {
	d, err := Parse(embedded)
	if err != nil {
		return nil, fmt.Errorf("embedded help.yaml: %w", err)
	}
	return d, nil
}

// Parse decodes and validates a dictionary in the format of help.yaml.
// Unknown fields are errors; all problems are reported at once.
func Parse(data []byte) (Dict, error) {
	dec := yaml.NewDecoder(bytes.NewReader(data))
	dec.KnownFields(true)
	var raws map[string]rawCommand
	if err := dec.Decode(&raws); err != nil && !errors.Is(err, io.EOF) {
		return nil, err
	}
	if len(raws) == 0 {
		return nil, errors.New("no commands defined")
	}
	if err := dec.Decode(new(yaml.Node)); !errors.Is(err, io.EOF) {
		return nil, errors.New("want a single YAML document with a mapping of commands")
	}
	var errs []error
	d := make(Dict, len(raws))
	for _, name := range slices.Sorted(maps.Keys(raws)) {
		r := raws[name]
		for _, p := range problems(name, r) {
			errs = append(errs, fmt.Errorf("%q: %s", name, p))
		}
		d[name] = Command(r)
	}
	if err := errors.Join(errs...); err != nil {
		return nil, err
	}
	return d, nil
}

// problems lists what is wrong with the entry name.
func problems(name string, r rawCommand) []string {
	var ps []string
	if name == "" || strings.Join(strings.Fields(name), " ") != name {
		ps = append(ps, "name must be words separated by single spaces")
	}
	ps = append(ps, textProblems("about", r.About)...)
	for _, opt := range slices.Sorted(maps.Keys(r.Options)) {
		if len(opt) < 2 || opt[0] != '-' || strings.ContainsAny(opt, " \t\r\n=") {
			ps = append(ps, fmt.Sprintf("option %q must start with - and be a single word", opt))
			continue
		}
		ps = append(ps, textProblems(fmt.Sprintf("option %q: description", opt), r.Options[opt])...)
	}
	return ps
}

func textProblems(field, text string) []string {
	switch {
	case strings.TrimSpace(text) == "":
		return []string{field + " is empty"}
	case strings.ContainsAny(text, "\r\n"):
		return []string{field + " must be a single line"}
	}
	return nil
}
