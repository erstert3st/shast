package catalog_test

import (
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
	"testing/fstest"

	"shast/internal/catalog"
	"shast/internal/outcmp"
)

const validYAML = `
- id: top-ips
  command: "awk '{print $1}' logs/access.log | sort | uniq -c"
  category: text
  difficulty: medium
  explanation: Counts requests per IP.
- id: now
  command: date
  category: system
  difficulty: easy
  explanation: Prints the date.
  deterministic: false
  compare: { ignore_order: true }
  exit_code: 3
  allow_empty: true
- id: explicit-true
  command: ls
  category: files
  difficulty: hard
  explanation: Lists files.
  deterministic: true
`

func validChallenges() []catalog.Challenge {
	return []catalog.Challenge{
		{
			ID:            "top-ips",
			Command:       "awk '{print $1}' logs/access.log | sort | uniq -c",
			Category:      "text",
			Difficulty:    catalog.Medium,
			Explanation:   "Counts requests per IP.",
			Deterministic: true,
		},
		{
			ID:            "now",
			Command:       "date",
			Category:      "system",
			Difficulty:    catalog.Easy,
			Explanation:   "Prints the date.",
			Deterministic: false,
			Compare:       outcmp.Options{IgnoreOrder: true},
			ExitCode:      3,
			AllowEmpty:    true,
		},
		{
			ID:            "explicit-true",
			Command:       "ls",
			Category:      "files",
			Difficulty:    catalog.Hard,
			Explanation:   "Lists files.",
			Deterministic: true,
		},
	}
}

func mapFS(files map[string]string) fstest.MapFS {
	fsys := fstest.MapFS{}
	for name, data := range files {
		fsys[name] = &fstest.MapFile{Data: []byte(data)}
	}
	return fsys
}

func TestLoad(t *testing.T) {
	withExpected := validChallenges()
	withExpected[0].Expected = "  42 10.0.0.1\n"

	tests := []struct {
		name  string
		files map[string]string
		want  []catalog.Challenge
	}{
		{
			name:  "valid without expected dir",
			files: map[string]string{"commands.yaml": validYAML},
			want:  validChallenges(),
		},
		{
			name: "expected output attached, other files ignored",
			files: map[string]string{
				"commands.yaml":          validYAML,
				"expected/top-ips.txt":   "  42 10.0.0.1\n",
				"expected/README.md":     "generated",
				"expected/unknown.log":   "not a .txt file",
				"expected/sub/other.txt": "subdirectories are ignored",
			},
			want: withExpected,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := catalog.Load(mapFS(tt.files))
			if err != nil {
				t.Fatalf("Load: %v", err)
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("Load =\n%+v\nwant\n%+v", got, tt.want)
			}
		})
	}
}

func TestLoadErrors(t *testing.T) {
	const entry = `
- id: x
  command: ls
  category: files
  difficulty: easy
  explanation: Lists files.
`
	tests := []struct {
		name    string
		files   map[string]string
		wantErr []string
	}{
		{
			name:    "missing commands.yaml",
			files:   map[string]string{"other.yaml": entry},
			wantErr: []string{"read catalog", "commands.yaml"},
		},
		{
			name:    "malformed yaml",
			files:   map[string]string{"commands.yaml": "- id: [unclosed\n"},
			wantErr: []string{"parse commands.yaml"},
		},
		{
			name:    "not a list",
			files:   map[string]string{"commands.yaml": "id: x\n"},
			wantErr: []string{"parse commands.yaml"},
		},
		{
			name:    "unknown field",
			files:   map[string]string{"commands.yaml": entry + "  color: red\n"},
			wantErr: []string{"parse commands.yaml", "color"},
		},
		{
			name:    "unknown compare field",
			files:   map[string]string{"commands.yaml": entry + "  compare: { ignore_case: true }\n"},
			wantErr: []string{"parse commands.yaml", "ignore_case"},
		},
		{
			name:    "wrong field type",
			files:   map[string]string{"commands.yaml": entry + "  exit_code: one\n"},
			wantErr: []string{"parse commands.yaml", "line 7"},
		},
		{
			name:    "empty file",
			files:   map[string]string{"commands.yaml": "# nothing yet\n"},
			wantErr: []string{"parse commands.yaml", "no challenges"},
		},
		{
			name:    "empty list",
			files:   map[string]string{"commands.yaml": "[]\n"},
			wantErr: []string{"parse commands.yaml", "no challenges"},
		},
		{
			name:    "multiple documents",
			files:   map[string]string{"commands.yaml": entry + "---\n" + entry},
			wantErr: []string{"parse commands.yaml", "single YAML document"},
		},
		{
			name:    "invalid challenge",
			files:   map[string]string{"commands.yaml": strings.Replace(entry, "easy", "extreme", 1)},
			wantErr: []string{"invalid commands.yaml", `challenge #1 "x"`, `unknown difficulty "extreme"`},
		},
		{
			name: "stale expected file",
			files: map[string]string{
				"commands.yaml":     entry,
				"expected/x.txt":    "ok",
				"expected/gone.txt": "stale",
			},
			wantErr: []string{"stale expected output expected/gone.txt"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := catalog.Load(mapFS(tt.files))
			if err == nil {
				t.Fatalf("Load = %+v, want error", got)
			}
			for _, want := range tt.wantErr {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("error %q does not contain %q", err, want)
				}
			}
		})
	}
}

func valid(id string) catalog.Challenge {
	return catalog.Challenge{
		ID:            id,
		Command:       "ls -l",
		Category:      "files",
		Difficulty:    catalog.Easy,
		Explanation:   "Lists files.",
		Deterministic: true,
	}
}

func with(id string, mutate func(*catalog.Challenge)) catalog.Challenge {
	c := valid(id)
	mutate(&c)
	return c
}

func TestValidate(t *testing.T) {
	tests := []struct {
		name    string
		cs      []catalog.Challenge
		wantErr []string // nil means valid
	}{
		{name: "empty", cs: nil},
		{name: "valid", cs: []catalog.Challenge{valid("a"), valid("b-2"), valid("3")}},
		{name: "command of max length", cs: []catalog.Challenge{
			with("a", func(c *catalog.Challenge) { c.Command = strings.Repeat("x", 120) }),
		}},
		{name: "exit code 255", cs: []catalog.Challenge{
			with("a", func(c *catalog.Challenge) { c.ExitCode = 255 }),
		}},
		{
			name:    "duplicate id",
			cs:      []catalog.Challenge{valid("a"), valid("b"), valid("a")},
			wantErr: []string{`challenge #3 "a": duplicate id, first used by challenge #1`},
		},
		{
			name:    "uppercase id",
			cs:      []catalog.Challenge{valid("Top")},
			wantErr: []string{`challenge #1 "Top": id must match`},
		},
		{
			name:    "id with path separator",
			cs:      []catalog.Challenge{valid("../x")},
			wantErr: []string{"id must match"},
		},
		{
			name:    "id with leading dash",
			cs:      []catalog.Challenge{valid("-x")},
			wantErr: []string{"id must match"},
		},
		{
			name:    "id with underscore",
			cs:      []catalog.Challenge{valid("a_b")},
			wantErr: []string{"id must match"},
		},
		{
			name:    "missing id",
			cs:      []catalog.Challenge{valid("a"), valid("")},
			wantErr: []string{"challenge #2: missing id"},
		},
		{
			name:    "missing command",
			cs:      []catalog.Challenge{with("a", func(c *catalog.Challenge) { c.Command = "" })},
			wantErr: []string{"missing command"},
		},
		{
			name:    "missing category",
			cs:      []catalog.Challenge{with("a", func(c *catalog.Challenge) { c.Category = "" })},
			wantErr: []string{"missing category"},
		},
		{
			name:    "missing difficulty",
			cs:      []catalog.Challenge{with("a", func(c *catalog.Challenge) { c.Difficulty = "" })},
			wantErr: []string{"missing difficulty"},
		},
		{
			name:    "blank explanation",
			cs:      []catalog.Challenge{with("a", func(c *catalog.Challenge) { c.Explanation = "  " })},
			wantErr: []string{"missing explanation"},
		},
		{
			name:    "unknown difficulty",
			cs:      []catalog.Challenge{with("a", func(c *catalog.Challenge) { c.Difficulty = "Easy" })},
			wantErr: []string{`unknown difficulty "Easy"`},
		},
		{
			name:    "unknown category",
			cs:      []catalog.Challenge{with("a", func(c *catalog.Challenge) { c.Category = "network" })},
			wantErr: []string{`unknown category "network"`},
		},
		{
			name:    "newline in command",
			cs:      []catalog.Challenge{with("a", func(c *catalog.Challenge) { c.Command = "ls\npwd" })},
			wantErr: []string{`command contains '\n' at byte 2`},
		},
		{
			name:    "tab in command",
			cs:      []catalog.Challenge{with("a", func(c *catalog.Challenge) { c.Command = "ls\t-l" })},
			wantErr: []string{`command contains '\t'`},
		},
		{
			name:    "non-ascii command",
			cs:      []catalog.Challenge{with("a", func(c *catalog.Challenge) { c.Command = "ls –l" })},
			wantErr: []string{`command contains '–'`, "printable ASCII"},
		},
		{
			name:    "DEL in command",
			cs:      []catalog.Challenge{with("a", func(c *catalog.Challenge) { c.Command = "ls\x7f" })},
			wantErr: []string{"printable ASCII"},
		},
		{
			name:    "command too long",
			cs:      []catalog.Challenge{with("a", func(c *catalog.Challenge) { c.Command = strings.Repeat("x", 121) })},
			wantErr: []string{"command is 121 characters long, max 120"},
		},
		{
			name:    "leading space",
			cs:      []catalog.Challenge{with("a", func(c *catalog.Challenge) { c.Command = " ls" })},
			wantErr: []string{"leading or trailing spaces"},
		},
		{
			name:    "trailing space",
			cs:      []catalog.Challenge{with("a", func(c *catalog.Challenge) { c.Command = "ls " })},
			wantErr: []string{"leading or trailing spaces"},
		},
		{
			name:    "negative exit code",
			cs:      []catalog.Challenge{with("a", func(c *catalog.Challenge) { c.ExitCode = -1 })},
			wantErr: []string{"exit_code -1 out of range 0..255"},
		},
		{
			name:    "exit code too large",
			cs:      []catalog.Challenge{with("a", func(c *catalog.Challenge) { c.ExitCode = 256 })},
			wantErr: []string{"exit_code 256 out of range"},
		},
		{
			name: "all problems reported",
			cs: []catalog.Challenge{
				with("a", func(c *catalog.Challenge) { c.Category = "" }),
				with("B", func(c *catalog.Challenge) { c.Explanation = "" }),
			},
			wantErr: []string{
				`challenge #1 "a": missing category`,
				`challenge #2 "B": id must match`,
				`challenge #2 "B": missing explanation`,
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := catalog.Validate(tt.cs)
			if tt.wantErr == nil {
				if err != nil {
					t.Fatalf("Validate: unexpected error: %v", err)
				}
				return
			}
			if err == nil {
				t.Fatal("Validate: want error, got nil")
			}
			for _, want := range tt.wantErr {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("error %q does not contain %q", err, want)
				}
			}
		})
	}
}

func ids(cs []catalog.Challenge) []string {
	out := make([]string, len(cs))
	for i, c := range cs {
		out[i] = c.ID
	}
	return out
}

func TestMerge(t *testing.T) {
	replaced := with("b", func(c *catalog.Challenge) { c.Command = "ls -la" })
	tests := []struct {
		name        string
		base        []catalog.Challenge
		overlay     []catalog.Challenge
		wantIDs     []string
		wantCommand map[string]string
	}{
		{
			name:    "empty overlay",
			base:    []catalog.Challenge{valid("a"), valid("b")},
			wantIDs: []string{"a", "b"},
		},
		{
			name:    "empty base",
			overlay: []catalog.Challenge{valid("x")},
			wantIDs: []string{"x"},
		},
		{
			name:        "replace in place and append",
			base:        []catalog.Challenge{valid("a"), valid("b"), valid("c")},
			overlay:     []catalog.Challenge{valid("e"), replaced, valid("d")},
			wantIDs:     []string{"a", "b", "c", "e", "d"},
			wantCommand: map[string]string{"a": "ls -l", "b": "ls -la"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			baseBefore := slices.Clone(tt.base)
			got := catalog.Merge(tt.base, tt.overlay)
			if g := ids(got); !slices.Equal(g, tt.wantIDs) {
				t.Errorf("Merge IDs = %v, want %v", g, tt.wantIDs)
			}
			for _, c := range got {
				if want, ok := tt.wantCommand[c.ID]; ok && c.Command != want {
					t.Errorf("%s: Command = %q, want %q", c.ID, c.Command, want)
				}
			}
			if !reflect.DeepEqual(tt.base, baseBefore) {
				t.Error("Merge modified base")
			}
		})
	}
}

func TestSelect(t *testing.T) {
	cs := []catalog.Challenge{
		with("e-files", func(c *catalog.Challenge) { c.Difficulty, c.Category = catalog.Easy, "files" }),
		with("h-git", func(c *catalog.Challenge) { c.Difficulty, c.Category = catalog.Hard, "git" }),
		with("e-git", func(c *catalog.Challenge) { c.Difficulty, c.Category = catalog.Easy, "git" }),
		with("m-files", func(c *catalog.Challenge) { c.Difficulty, c.Category = catalog.Medium, "files" }),
	}
	tests := []struct {
		name   string
		filter catalog.Filter
		want   []string
	}{
		{"zero filter matches all", catalog.Filter{}, []string{"e-files", "h-git", "e-git", "m-files"}},
		{"by difficulty", catalog.Filter{Difficulty: catalog.Easy}, []string{"e-files", "e-git"}},
		{"by category", catalog.Filter{Category: "files"}, []string{"e-files", "m-files"}},
		{"both", catalog.Filter{Difficulty: catalog.Easy, Category: "git"}, []string{"e-git"}},
		{"no match", catalog.Filter{Difficulty: catalog.Medium, Category: "git"}, []string{}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := catalog.Select(cs, tt.filter)
			if g := ids(got); !slices.Equal(g, tt.want) {
				t.Errorf("Select = %v, want %v", g, tt.want)
			}
			for _, c := range cs {
				if tt.filter.Match(c) != slices.Contains(tt.want, c.ID) {
					t.Errorf("Match(%s) = %v, inconsistent with Select", c.ID, tt.filter.Match(c))
				}
			}
		})
	}
}

func TestDifficultyRank(t *testing.T) {
	tests := []struct {
		d    catalog.Difficulty
		want int
	}{
		{catalog.Easy, 0},
		{catalog.Medium, 1},
		{catalog.Hard, 2},
		{"", -1},
		{"extreme", -1},
	}
	for _, tt := range tests {
		if got := tt.d.Rank(); got != tt.want {
			t.Errorf("Difficulty(%q).Rank() = %d, want %d", tt.d, got, tt.want)
		}
	}
	for i, d := range catalog.Difficulties() {
		if d.Rank() != i {
			t.Errorf("Difficulties()[%d] = %q has rank %d", i, d, d.Rank())
		}
	}
}

func TestDefault(t *testing.T) {
	cs, err := catalog.Default()
	if err != nil {
		t.Fatalf("Default: %v", err)
	}
	if len(cs) < 60 {
		t.Errorf("Default has %d challenges, want at least 60", len(cs))
	}
	perCategory := map[string]int{}
	perDifficulty := map[catalog.Difficulty]int{}
	seen := map[string]bool{}
	nonDeterministic := 0
	for _, c := range cs {
		if seen[c.ID] {
			t.Errorf("duplicate ID %q", c.ID)
		}
		seen[c.ID] = true
		perCategory[c.Category]++
		perDifficulty[c.Difficulty]++
		if !c.Deterministic {
			nonDeterministic++
		}
	}
	for _, cat := range catalog.Categories() {
		if perCategory[cat] < 3 {
			t.Errorf("category %q has %d challenges, want at least 3", cat, perCategory[cat])
		}
	}
	for _, d := range catalog.Difficulties() {
		if perDifficulty[d] == 0 {
			t.Errorf("no challenges with difficulty %q", d)
		}
	}
	if nonDeterministic < 3 {
		t.Errorf("%d non-deterministic challenges, want at least 3", nonDeterministic)
	}
}

func TestLoadWithOverlay(t *testing.T) {
	base, err := catalog.Default()
	if err != nil {
		t.Fatalf("Default: %v", err)
	}
	dir := t.TempDir()
	overlay := `
- id: ` + base[0].ID + `
  command: echo replaced
  category: system
  difficulty: easy
  explanation: Replaced.
- id: brand-new
  command: echo new
  category: system
  difficulty: easy
  explanation: New.
`
	if err := os.WriteFile(filepath.Join(dir, "commands.yaml"), []byte(overlay), 0o644); err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name      string
		dir       string
		wantLen   int
		wantFirst string
		wantLast  string
	}{
		{"no overlay", "", len(base), base[0].Command, base[len(base)-1].Command},
		{"overlay", dir, len(base) + 1, "echo replaced", "echo new"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := catalog.LoadWithOverlay(tt.dir)
			if err != nil {
				t.Fatalf("LoadWithOverlay: %v", err)
			}
			if len(got) != tt.wantLen {
				t.Fatalf("len = %d, want %d", len(got), tt.wantLen)
			}
			if got[0].Command != tt.wantFirst || got[len(got)-1].Command != tt.wantLast {
				t.Errorf("first/last command = %q/%q, want %q/%q",
					got[0].Command, got[len(got)-1].Command, tt.wantFirst, tt.wantLast)
			}
		})
	}

	t.Run("missing dir", func(t *testing.T) {
		missing := filepath.Join(dir, "nope")
		if _, err := catalog.LoadWithOverlay(missing); err == nil || !strings.Contains(err.Error(), missing) {
			t.Errorf("LoadWithOverlay(%q) error = %v, want error mentioning the dir", missing, err)
		}
	})
}

// TestDefaultExpectedOutputs checks that the committed expected outputs
// match the catalog: exactly the deterministic challenges have one
// (regenerate with `shast expected`).
func TestDefaultExpectedOutputs(t *testing.T) {
	cs, err := catalog.Default()
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range cs {
		if has := c.Expected != ""; has != c.Deterministic {
			t.Errorf("%s: deterministic=%v but has expected output=%v", c.ID, c.Deterministic, has)
		}
	}
}
