package score

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"time"
)

// MaxEntries is the number of highscores kept.
const MaxEntries = 10

// Entry is one highscore together with the filter settings of its session.
type Entry struct {
	Score      float64       `json:"score"`
	WPM        float64       `json:"wpm"`
	Accuracy   float64       `json:"accuracy"`
	Errors     int           `json:"errors"`
	Rounds     int           `json:"rounds"`
	Duration   time.Duration `json:"duration_ns"`
	Mode       string        `json:"mode"`
	Difficulty string        `json:"difficulty"`
	Category   string        `json:"category"`
	Order      string        `json:"order"`
	At         time.Time     `json:"at"`
}

// ranksBefore reports whether a is ranked above b: higher score first,
// on equal score the earlier entry first.
func ranksBefore(a, b Entry) bool {
	if a.Score != b.Score {
		return a.Score > b.Score
	}
	return a.At.Before(b.At)
}

// DefaultPath returns the default highscore file:
// $XDG_DATA_HOME/shast/scores.json, falling back to
// $HOME/.local/share/shast/scores.json. A relative XDG_DATA_HOME is
// ignored as required by the XDG Base Directory specification.
func DefaultPath() (string, error) {
	if dir := os.Getenv("XDG_DATA_HOME"); filepath.IsAbs(dir) {
		return filepath.Join(dir, "shast", "scores.json"), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("highscore path: %w", err)
	}
	return filepath.Join(home, ".local", "share", "shast", "scores.json"), nil
}

// Store reads and writes the highscore list in a JSON file.
type Store struct {
	path string
}

// NewStore returns a Store backed by the file at path.
func NewStore(path string) *Store {
	return &Store{path: path}
}

// Load returns the stored entries. A missing file yields no entries and
// no error.
func (s *Store) Load() ([]Entry, error) {
	data, err := os.ReadFile(s.path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read highscores: %w", err)
	}
	var entries []Entry
	if err := json.Unmarshal(data, &entries); err != nil {
		return nil, fmt.Errorf("parse highscores %s: %w", s.path, err)
	}
	return entries, nil
}

// Add inserts e into the highscore list, keeps the best MaxEntries and
// writes the result atomically. rank is the 1-based position of e in top,
// or 0 if e did not make the list.
func (s *Store) Add(e Entry) (rank int, top []Entry, err error) {
	entries, err := s.Load()
	if err != nil {
		return 0, nil, err
	}
	slices.SortStableFunc(entries, compareEntries)
	pos := slices.IndexFunc(entries, func(x Entry) bool { return ranksBefore(e, x) })
	if pos < 0 {
		pos = len(entries)
	}
	top = slices.Insert(entries, pos, e)
	if len(top) > MaxEntries {
		top = top[:MaxEntries]
	}
	if pos < MaxEntries {
		rank = pos + 1
	}
	if err := s.write(top); err != nil {
		return 0, nil, err
	}
	return rank, top, nil
}

func compareEntries(a, b Entry) int {
	switch {
	case ranksBefore(a, b):
		return -1
	case ranksBefore(b, a):
		return 1
	default:
		return 0
	}
}

// write stores entries atomically: it writes a temp file in the target
// directory, syncs it and renames it over the destination.
func (s *Store) write(entries []Entry) (err error) {
	data, err := json.MarshalIndent(entries, "", "  ")
	if err != nil {
		return fmt.Errorf("encode highscores: %w", err)
	}
	data = append(data, '\n')

	dir := filepath.Dir(s.path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("create highscore dir: %w", err)
	}
	f, err := os.CreateTemp(dir, ".scores-*.json.tmp")
	if err != nil {
		return fmt.Errorf("create highscore temp file: %w", err)
	}
	defer func() {
		if err != nil {
			_ = f.Close()
			_ = os.Remove(f.Name())
		}
	}()
	if err := f.Chmod(0o644); err != nil {
		return fmt.Errorf("chmod highscore temp file: %w", err)
	}
	if _, err := f.Write(data); err != nil {
		return fmt.Errorf("write highscore temp file: %w", err)
	}
	if err := f.Sync(); err != nil {
		return fmt.Errorf("sync highscore temp file: %w", err)
	}
	if err := f.Close(); err != nil {
		return fmt.Errorf("close highscore temp file: %w", err)
	}
	if err := os.Rename(f.Name(), s.path); err != nil {
		return fmt.Errorf("replace highscores: %w", err)
	}
	return nil
}
