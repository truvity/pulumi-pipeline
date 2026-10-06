// Package genfiles is the write side of a repository's generated files.
//
// Two jobs share it. A reconciler (Diff, Apply) makes a set of desired files
// true on disk and removes the stale files of the directories it owns; a
// config writer (Write, Remove, EnsureStamp, Canonical) persists one
// generated file under a config directory and guarantees its provenance
// stamp. Both write atomically (temp file in the target directory, then
// rename) and skip a write whose content is already on disk, so mtimes of
// unchanged outputs are preserved.
//
// The package knows nothing about what the files mean or how a consumer
// reads them back (an overlay, an embed): the consumer wraps these calls.
package genfiles

import (
	"bytes"
	"fmt"
	"os"
	pathpkg "path"
	"path/filepath"
	"sort"
	"strings"
)

type (
	// File is one file a generator wants on disk.
	File struct {
		// Path is relative to the repository root.
		Path    string
		Content []byte
	}

	// Result is the outcome of a Diff. Every list is sorted and holds
	// root-relative paths.
	Result struct {
		Created []string // not on disk yet
		Updated []string // on disk with other content (a hand edit is an update)
		Deleted []string // stale files an owned pattern matches
		Skipped []string // byte-identical
	}

	// Owner decides which files of a tree the generator owns: only those can
	// be deleted as stale. A zero Owner owns nothing.
	Owner struct {
		// Patterns are root-relative path.Match globs.
		Patterns []string
		// Exclude, when set, vetoes a match (a file in an owned directory
		// that another tool writes).
		Exclude func(path string) bool
	}
)

// Owns reports whether path matches an owned pattern and is not excluded.
func (o Owner) Owns(path string) bool {
	if o.Exclude != nil && o.Exclude(path) {
		return false
	}

	for _, pat := range o.Patterns {
		if ok, _ := pathpkg.Match(pat, filepath.ToSlash(path)); ok {
			return true
		}
	}

	return false
}

// Diff compares desired against the tree under root and lists the stale
// files owner owns that desired does not contain. It writes nothing.
func Diff(root string, desired []File, owner Owner) (*Result, error) {
	result := &Result{}

	desiredSet := make(map[string]bool, len(desired))

	for _, f := range desired {
		desiredSet[f.Path] = true

		disk, err := os.ReadFile(filepath.Join(root, f.Path))

		switch {
		case err == nil && bytes.Equal(disk, f.Content):
			result.Skipped = append(result.Skipped, f.Path)
		case err == nil:
			result.Updated = append(result.Updated, f.Path)
		case os.IsNotExist(err):
			result.Created = append(result.Created, f.Path)
		default:
			return nil, fmt.Errorf("read %s: %w", f.Path, err)
		}
	}

	for _, pat := range owner.Patterns {
		matches, err := filepath.Glob(filepath.Join(root, filepath.FromSlash(pat)))
		if err != nil {
			return nil, fmt.Errorf("glob %s: %w", pat, err)
		}

		for _, m := range matches {
			rel, err := filepath.Rel(root, m)
			if err != nil {
				return nil, fmt.Errorf("rel %s: %w", m, err)
			}

			if owner.Owns(rel) && !desiredSet[rel] {
				result.Deleted = append(result.Deleted, rel)
			}
		}
	}

	sort.Strings(result.Created)
	sort.Strings(result.Updated)
	sort.Strings(result.Deleted)
	sort.Strings(result.Skipped)

	return result, nil
}

// Apply writes the Created and Updated files of result and removes its
// Deleted ones. desired must be the set Diff was given.
func Apply(root string, desired []File, result *Result) error {
	byPath := make(map[string]File, len(desired))
	for _, f := range desired {
		byPath[f.Path] = f
	}

	paths := append(append([]string{}, result.Created...), result.Updated...)
	for _, p := range paths {
		f := byPath[p]
		if err := AtomicWrite(filepath.Join(root, f.Path), f.Content); err != nil {
			return fmt.Errorf("write %s: %w", f.Path, err)
		}
	}

	for _, p := range result.Deleted {
		if err := os.Remove(filepath.Join(root, p)); err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("delete %s: %w", p, err)
		}
	}

	return nil
}

// AtomicWrite creates the parent directories and replaces path with content
// in one rename, so a reader (or a crash) never sees a half-written file.
// The file is world-readable (0644), as a checked-in file is.
func AtomicWrite(path string, content []byte) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("mkdir %s: %w", dir, err)
	}

	tmp, err := os.CreateTemp(dir, ".*"+TempSuffix)
	if err != nil {
		return fmt.Errorf("create temp: %w", err)
	}

	name := tmp.Name()

	defer func() { _ = os.Remove(name) }() // a no-op after a successful rename

	if _, err := tmp.Write(content); err != nil {
		_ = tmp.Close()

		return fmt.Errorf("write temp %s: %w", name, err)
	}

	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()

		return fmt.Errorf("sync temp %s: %w", name, err)
	}

	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close temp %s: %w", name, err)
	}

	if err := os.Chmod(name, 0o644); err != nil {
		return fmt.Errorf("chmod temp %s: %w", name, err)
	}

	if err := os.Rename(name, path); err != nil {
		return fmt.Errorf("rename %s: %w", name, err)
	}

	return nil
}

// TempSuffix ends the name of every temp file AtomicWrite leaves behind if
// the process dies mid-write; consumers gitignore "*" + TempSuffix.
const TempSuffix = ".genfiles-tmp"

// Write persists data at root/dir/path, atomically, unless the file already
// holds exactly data. It reports whether the disk changed.
func Write(root, dir, path string, data []byte) (changed bool, err error) {
	disk := filepath.Join(root, dir, path)

	if existing, readErr := os.ReadFile(disk); readErr == nil && bytes.Equal(existing, data) {
		return false, nil
	}

	if err := AtomicWrite(disk, data); err != nil {
		return false, fmt.Errorf("genfiles.Write(%s): %w", path, err)
	}

	return true, nil
}

// Remove deletes root/dir/path; a file that is already gone is not an error.
func Remove(root, dir, path string) error {
	if err := os.Remove(filepath.Join(root, dir, path)); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("genfiles.Remove(%s): %w", path, err)
	}

	return nil
}

// StampPrefix is the provenance marker every machine-written file carries on
// its first line. A lint that fails when it is missing turns a hand edit to a
// generated file into a visible diff instead of drift that looks like it
// worked.
const StampPrefix = "# GENERATED by "

// Stamp builds the two-line provenance header for a file written by
// generator. What regenerates a file matters more to a reader than when.
func Stamp(generator string) string {
	return StampPrefix + generator + " — do not edit manually.\n" +
		"# Regenerate instead of editing; hand edits are lost on the next write.\n"
}

// EnsureStamp prepends the provenance header to anything under gen/, unless
// data already starts with a stamp. The generator is derived from the path
// (gen/{account}/{stack}.yaml becomes "pulumi stack {stack} ({account}
// account)"). Paths outside gen/ are returned untouched.
func EnsureStamp(path string, data []byte) []byte {
	if !strings.HasPrefix(path, "gen/") || bytes.HasPrefix(data, []byte(StampPrefix)) {
		return data
	}

	generator := "a pulumi stack"

	if parts := strings.Split(path, "/"); len(parts) == 3 {
		account := parts[1]
		stack := strings.TrimSuffix(parts[2], ".yaml")
		generator = fmt.Sprintf("pulumi stack %s (%s account)", stack, account)
	}

	return append([]byte(Stamp(generator)), data...)
}
