package score

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"slices"
	"testing"
	"time"
)

var epoch = time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)

// entry returns an Entry identified by its At offset in minutes.
func entry(score float64, minute int) Entry {
	return Entry{Score: score, At: epoch.Add(time.Duration(minute) * time.Minute)}
}

// ids returns the At minute offsets of entries, which identify them in tests.
func ids(entries []Entry) []int {
	out := make([]int, len(entries))
	for i, e := range entries {
		out[i] = int(e.At.Sub(epoch) / time.Minute)
	}
	return out
}

func writeEntries(t *testing.T, path string, entries []Entry) {
	t.Helper()
	data, err := json.Marshal(entries)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestLoadMissingFile(t *testing.T) {
	s := NewStore(filepath.Join(t.TempDir(), "nope", "scores.json"))
	got, err := s.Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if got != nil {
		t.Errorf("Load() = %v, want nil", got)
	}
}

func TestCorruptFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "scores.json")
	if err := os.WriteFile(path, []byte("{not json"), 0o644); err != nil {
		t.Fatal(err)
	}
	s := NewStore(path)
	if _, err := s.Load(); err == nil {
		t.Error("Load() error = nil, want error")
	}
	if _, _, err := s.Add(entry(1, 0)); err == nil {
		t.Error("Add() error = nil, want error")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "{not json" {
		t.Errorf("corrupt file was overwritten: %q", data)
	}
}

func TestAdd(t *testing.T) {
	ten := make([]Entry, MaxEntries)
	for i := range ten {
		ten[i] = entry(float64(100-10*i), i) // scores 100, 90, ..., 10
	}
	tests := []struct {
		name     string
		existing []Entry // nil means no file
		add      Entry
		wantRank int
		wantIDs  []int
	}{
		{
			name:     "first entry",
			add:      entry(5, 0),
			wantRank: 1,
			wantIDs:  []int{0},
		},
		{
			name:     "sorted by score desc",
			existing: []Entry{entry(30, 1), entry(10, 2)},
			add:      entry(20, 3),
			wantRank: 2,
			wantIDs:  []int{1, 3, 2},
		},
		{
			name:     "unsorted file gets sorted",
			existing: []Entry{entry(10, 1), entry(30, 2)},
			add:      entry(20, 3),
			wantRank: 2,
			wantIDs:  []int{2, 3, 1},
		},
		{
			name:     "tie ranks after earlier entry",
			existing: []Entry{entry(20, 1), entry(10, 2)},
			add:      entry(20, 5),
			wantRank: 2,
			wantIDs:  []int{1, 5, 2},
		},
		{
			name:     "tie ranks before later entry",
			existing: []Entry{entry(20, 5), entry(10, 6)},
			add:      entry(20, 1),
			wantRank: 1,
			wantIDs:  []int{1, 5, 6},
		},
		{
			name:     "full tie keeps existing first",
			existing: []Entry{entry(20, 1)},
			add:      entry(20, 1),
			wantRank: 2,
			wantIDs:  []int{1, 1},
		},
		{
			name:     "placed into full list truncates last",
			existing: ten,
			add:      entry(55, 50),
			wantRank: 6,
			wantIDs:  []int{0, 1, 2, 3, 4, 50, 5, 6, 7, 8},
		},
		{
			name:     "last place in full list",
			existing: ten,
			add:      entry(15, 50),
			wantRank: 10,
			wantIDs:  []int{0, 1, 2, 3, 4, 5, 6, 7, 8, 50},
		},
		{
			name:     "not placed",
			existing: ten,
			add:      entry(5, 50),
			wantRank: 0,
			wantIDs:  []int{0, 1, 2, 3, 4, 5, 6, 7, 8, 9},
		},
		{
			name:     "tie with last of full list not placed",
			existing: ten,
			add:      entry(10, 50),
			wantRank: 0,
			wantIDs:  []int{0, 1, 2, 3, 4, 5, 6, 7, 8, 9},
		},
		{
			name:     "oversized file truncated",
			existing: append(slices.Clone(ten), entry(1, 20), entry(2, 21)),
			add:      entry(0.5, 50),
			wantRank: 0,
			wantIDs:  []int{0, 1, 2, 3, 4, 5, 6, 7, 8, 9},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "shast", "scores.json")
			if tt.existing != nil {
				if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
					t.Fatal(err)
				}
				writeEntries(t, path, tt.existing)
			}
			s := NewStore(path)
			rank, top, err := s.Add(tt.add)
			if err != nil {
				t.Fatalf("Add() error = %v", err)
			}
			if rank != tt.wantRank {
				t.Errorf("rank = %d, want %d", rank, tt.wantRank)
			}
			if got := ids(top); !slices.Equal(got, tt.wantIDs) {
				t.Errorf("top ids = %v, want %v", got, tt.wantIDs)
			}
			if rank > 0 && top[rank-1] != tt.add {
				t.Errorf("top[rank-1] = %+v, want added entry %+v", top[rank-1], tt.add)
			}
			loaded, err := s.Load()
			if err != nil {
				t.Fatalf("Load() error = %v", err)
			}
			if got := ids(loaded); !slices.Equal(got, tt.wantIDs) {
				t.Errorf("persisted ids = %v, want %v", got, tt.wantIDs)
			}
		})
	}
}

func TestAddFileMode(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "a", "b")
	path := filepath.Join(dir, "scores.json")
	if _, _, err := NewStore(path).Add(entry(1, 0)); err != nil {
		t.Fatalf("Add() error = %v", err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	// Windows has no Unix permission bits.
	if got := info.Mode().Perm(); got != 0o644 && runtime.GOOS != "windows" {
		t.Errorf("file mode = %v, want 0644", got)
	}
	names, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(names) != 1 {
		t.Errorf("dir contains %d entries, want only scores.json (temp file left behind?)", len(names))
	}
}

func TestRoundTrip(t *testing.T) {
	want := Entry{
		Score:      42.5,
		WPM:        50.25,
		Accuracy:   0.845,
		Errors:     7,
		Rounds:     10,
		Duration:   3*time.Minute + 1234*time.Millisecond,
		Mode:       "speed",
		Difficulty: "medium",
		Category:   "all",
		Order:      "random",
		At:         time.Date(2026, 9, 26, 12, 30, 45, 123456789, time.FixedZone("CEST", 2*60*60)),
	}
	s := NewStore(filepath.Join(t.TempDir(), "scores.json"))
	if _, _, err := s.Add(want); err != nil {
		t.Fatalf("Add() error = %v", err)
	}
	loaded, err := s.Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if len(loaded) != 1 {
		t.Fatalf("Load() returned %d entries, want 1", len(loaded))
	}
	got := loaded[0]
	if !got.At.Equal(want.At) {
		t.Errorf("At = %v, want %v", got.At, want.At)
	}
	got.At, want.At = time.Time{}, time.Time{}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("round trip = %+v, want %+v", got, want)
	}
}

func TestDefaultPath(t *testing.T) {
	home := t.TempDir()
	xdg := t.TempDir() // absolute on every OS
	tests := []struct {
		name string
		xdg  string
		want string
	}{
		{
			name: "xdg set",
			xdg:  xdg,
			want: filepath.Join(xdg, "shast", "scores.json"),
		},
		{
			name: "xdg relative ignored",
			xdg:  "relative/data",
			want: filepath.Join(home, ".local", "share", "shast", "scores.json"),
		},
		{
			name: "xdg empty falls back to home",
			want: filepath.Join(home, ".local", "share", "shast", "scores.json"),
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("HOME", home)
			t.Setenv("USERPROFILE", home) // os.UserHomeDir on Windows
			t.Setenv("XDG_DATA_HOME", tt.xdg)
			got, err := DefaultPath()
			if err != nil {
				t.Fatalf("DefaultPath() error = %v", err)
			}
			if got != tt.want {
				t.Errorf("DefaultPath() = %q, want %q", got, tt.want)
			}
		})
	}
}
