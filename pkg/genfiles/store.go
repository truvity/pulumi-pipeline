package genfiles

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"path"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/truvity/pulumi-pipeline/pkg/gitutils"
	"go.yaml.in/yaml/v3"
)

// A Store is a repository's generated-file tree as a process sees it: the
// files compiled into the binary (an embed.FS) overlaid with what this
// process has written since. A pipeline step that regenerates a file writes
// it to disk AND to the overlay, so a later read in the same process sees
// fresh data instead of the stale compiled-in copy.
//
// The overlay is private to the Store and guarded by a mutex. Generation
// counts the writes that changed something, for detecting a stale struct.
type Store struct {
	embedded fs.FS
	dir      string
	resolver gitutils.RootResolver
	overlay  *overlayFS
}

// NewStore returns a Store reading from embedded (nil: nothing compiled in)
// and writing under <git root>/<dir>. Paths are relative to dir, the way
// embedded is rooted.
func NewStore(embedded fs.FS, dir string) *Store {
	s := &Store{embedded: embedded, dir: dir}
	s.overlay = &overlayFS{embedded: embedded, files: map[string][]byte{}}

	return s
}

// FS is the overlay filesystem: the overlay first, then the embed. It
// implements fs.ReadFileFS and fs.ReadDirFS.
func (s *Store) FS() fs.FS { return s.overlay }

// Generation is the number of writes and deletes that changed the tree.
func (s *Store) Generation() uint64 { return atomic.LoadUint64(&s.overlay.generation) }

// ResetOverlay drops every overlay entry. For tests: production code lets
// the overlay persist.
func (s *Store) ResetOverlay() {
	s.overlay.mu.Lock()
	s.overlay.files = map[string][]byte{}
	s.overlay.mu.Unlock()
}

// DiskRoot is the git root the Store writes under, resolved once and cached.
func (s *Store) DiskRoot(ctx context.Context, logger *slog.Logger) (string, error) {
	return s.resolver.Get(ctx, logger)
}

// SetDiskRoot overrides the git-based root, for tests that use a temp dir or
// run without git.
func (s *Store) SetDiskRoot(root string) { s.resolver.Set(root) }

// ResetDiskRoot clears the cached root.
func (s *Store) ResetDiskRoot() { s.resolver.Reset() }

// Exists reports whether path is present in the Store.
func (s *Store) Exists(path string) bool {
	_, err := fs.Stat(s.overlay, path)

	return err == nil
}

// Write persists data at path on disk and in the overlay. The provenance
// stamp is ensured here, at the single funnel, so no writer can strip it.
// An unchanged file leaves the disk (and its mtime) alone, but the overlay
// is still refreshed: it may be stale from a previous process.
func (s *Store) Write(ctx context.Context, logger *slog.Logger, path string, data []byte) error {
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("genfiles: write %s: %w", path, err)
	}

	root, err := s.resolver.Get(ctx, logger)
	if err != nil {
		return fmt.Errorf("genfiles: write %s: %w", path, err)
	}

	data = EnsureStamp(path, data)

	changed, err := Write(root, s.dir, path, data)
	if err != nil {
		return fmt.Errorf("genfiles: write %s: %w", path, err)
	}

	s.overlay.mu.Lock()
	s.overlay.files[path] = append([]byte(nil), data...)
	s.overlay.mu.Unlock()

	if changed {
		atomic.AddUint64(&s.overlay.generation, 1)
	}

	return nil
}

// WriteYAML marshals val, stamps it and writes it (untyped, no validation).
// The generator named in the stamp is derived from path.
func (s *Store) WriteYAML(ctx context.Context, logger *slog.Logger, path string, val any) error {
	data, err := yaml.Marshal(val)
	if err != nil {
		return fmt.Errorf("genfiles: marshal %s: %w", path, err)
	}

	return s.Write(ctx, logger, path, data)
}

// WriteYAMLBy is WriteYAML with an explicit generator identity for the
// stamp, for a path that does not name the generator that owns the file.
func (s *Store) WriteYAMLBy(ctx context.Context, logger *slog.Logger, generator, path string, val any) error {
	data, err := yaml.Marshal(val)
	if err != nil {
		return fmt.Errorf("genfiles: marshal %s: %w", path, err)
	}

	return s.Write(ctx, logger, path, append([]byte(Stamp(generator)), data...))
}

// Delete removes path from disk and the overlay.
func (s *Store) Delete(ctx context.Context, logger *slog.Logger, path string) error {
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("genfiles: delete %s: %w", path, err)
	}

	root, err := s.resolver.Get(ctx, logger)
	if err != nil {
		return fmt.Errorf("genfiles: delete %s: %w", path, err)
	}

	if err := Remove(root, s.dir, path); err != nil {
		return fmt.Errorf("genfiles: delete %s: %w", path, err)
	}

	s.overlay.mu.Lock()
	delete(s.overlay.files, path)
	s.overlay.mu.Unlock()

	atomic.AddUint64(&s.overlay.generation, 1)

	return nil
}

// Load reads path from the Store and parses it as T. A missing file is
// (nil, nil).
func Load[T any, PT ptrValidatable[T]](ctx context.Context, s *Store, path string) (*T, error) {
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("genfiles: load %s: %w", path, err)
	}

	return LoadFrom[T, PT](s.overlay, path)
}

// LoadFrom is Load from a specific filesystem.
func LoadFrom[T any, PT ptrValidatable[T]](fsys fs.FS, path string) (*T, error) {
	data, err := fs.ReadFile(fsys, path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, nil
		}

		return nil, fmt.Errorf("genfiles: read %s: %w", path, err)
	}

	return Parse[T, PT](path, data)
}

// Save validates val, marshals it and writes it to the Store.
func Save[T any, PT ptrValidatable[T]](ctx context.Context, logger *slog.Logger, s *Store, path string, val *T) error {
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("genfiles: save %s: %w", path, err)
	}

	data, err := Marshal[T, PT](path, val)
	if err != nil {
		return err
	}

	return s.Write(ctx, logger, path, data)
}

// SaveFromMap converts a map to T by a YAML round trip, validates it and
// writes it to the Store; for raw key-value outputs.
func SaveFromMap[T any, PT ptrValidatable[T]](ctx context.Context, logger *slog.Logger, s *Store, path string, data map[string]any) error {
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("genfiles: save-from-map %s: %w", path, err)
	}

	canonical, err := Canonical[T, PT](path, data)
	if err != nil {
		return err
	}

	return s.Write(ctx, logger, path, canonical)
}

// ── overlay filesystem ──────────────────────────────────────────────────────

type overlayFS struct {
	embedded   fs.FS
	mu         sync.RWMutex
	files      map[string][]byte
	generation uint64
}

func (o *overlayFS) lookup(name string) ([]byte, bool) {
	o.mu.RLock()
	defer o.mu.RUnlock()

	data, ok := o.files[name]

	return data, ok
}

func (o *overlayFS) Open(name string) (fs.File, error) {
	if data, ok := o.lookup(name); ok {
		return &memFile{name: name, data: data}, nil
	}

	if o.embedded == nil {
		return nil, &fs.PathError{Op: "open", Path: name, Err: fs.ErrNotExist}
	}

	return o.embedded.Open(name)
}

// ReadFile returns a copy, so a caller cannot mutate overlay data.
func (o *overlayFS) ReadFile(name string) ([]byte, error) {
	if data, ok := o.lookup(name); ok {
		return append([]byte(nil), data...), nil
	}

	if o.embedded == nil {
		return nil, &fs.PathError{Op: "open", Path: name, Err: fs.ErrNotExist}
	}

	return fs.ReadFile(o.embedded, name)
}

// ReadDir merges the embed's entries with the overlay's.
func (o *overlayFS) ReadDir(name string) ([]fs.DirEntry, error) {
	var (
		entries []fs.DirEntry
		err     error
	)

	if o.embedded != nil {
		entries, err = fs.ReadDir(o.embedded, name)
	} else {
		err = &fs.PathError{Op: "readdir", Path: name, Err: fs.ErrNotExist}
	}

	o.mu.RLock()
	defer o.mu.RUnlock()

	prefix := name + "/"
	if name == "." {
		prefix = ""
	}

	seen := make(map[string]bool, len(entries))
	for _, e := range entries {
		seen[e.Name()] = true
	}

	for p, data := range o.files {
		if !strings.HasPrefix(p, prefix) {
			continue
		}

		rest := p[len(prefix):]
		child, _, nested := strings.Cut(rest, "/")

		if child == "" || seen[child] {
			continue
		}

		seen[child] = true

		entries = append(entries, &memDirEntry{name: child, isDir: nested, size: int64(len(data))})
	}

	if err != nil && len(entries) == 0 {
		return nil, err
	}

	return entries, nil
}

type (
	memFile struct {
		name   string
		data   []byte
		offset int
	}

	memFileInfo struct {
		name string
		size int64
	}

	memDirEntry struct {
		name  string
		isDir bool
		size  int64
	}
)

func (f *memFile) Stat() (fs.FileInfo, error) {
	return &memFileInfo{name: path.Base(f.name), size: int64(len(f.data))}, nil
}

func (f *memFile) Read(b []byte) (int, error) {
	if f.offset >= len(f.data) {
		return 0, io.EOF
	}

	n := copy(b, f.data[f.offset:])
	f.offset += n

	if f.offset >= len(f.data) {
		return n, io.EOF
	}

	return n, nil
}

func (f *memFile) Close() error { return nil }

func (fi *memFileInfo) Name() string       { return fi.name }
func (fi *memFileInfo) Size() int64        { return fi.size }
func (fi *memFileInfo) Mode() fs.FileMode  { return 0o444 }
func (fi *memFileInfo) ModTime() time.Time { return time.Time{} }
func (fi *memFileInfo) IsDir() bool        { return false }
func (fi *memFileInfo) Sys() any           { return nil }

func (de *memDirEntry) Name() string { return de.name }
func (de *memDirEntry) IsDir() bool  { return de.isDir }
func (de *memDirEntry) Type() fs.FileMode {
	if de.isDir {
		return fs.ModeDir
	}

	return 0
}
func (de *memDirEntry) Info() (fs.FileInfo, error) {
	return &memFileInfo{name: de.name, size: de.size}, nil
}
