package genfiles_test

import (
	"context"
	"errors"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/truvity/pulumi-pipeline/pkg/genfiles"
)

type doc struct {
	Name string `yaml:"name"`
}

func (d *doc) Validate() error {
	if d.Name == "" {
		return errors.New("name is empty")
	}

	return nil
}

func newStore(t *testing.T) (*genfiles.Store, string) {
	t.Helper()

	root := t.TempDir()
	s := genfiles.NewStore(fstest.MapFS{
		"gen/a.yaml":     {Data: []byte("name: embedded\n")},
		"gen/sub/b.yaml": {Data: []byte("name: b\n")},
	}, "cfg")
	s.SetDiskRoot(root)

	return s, root
}

func TestStoreOverlayWinsAndIsVisible(t *testing.T) {
	ctx, log := context.Background(), slog.Default()
	s, root := newStore(t)

	got, err := genfiles.Load[doc](ctx, s, "gen/a.yaml")
	if err != nil || got.Name != "embedded" {
		t.Fatalf("embedded load = %v, %v", got, err)
	}

	gen := s.Generation()

	if err := genfiles.Save[doc](ctx, log, s, "gen/a.yaml", &doc{Name: "fresh"}); err != nil {
		t.Fatal(err)
	}

	got, err = genfiles.Load[doc](ctx, s, "gen/a.yaml")
	if err != nil || got.Name != "fresh" {
		t.Fatalf("overlay load = %v, %v", got, err)
	}

	if s.Generation() != gen+1 {
		t.Errorf("generation = %d, want %d", s.Generation(), gen+1)
	}

	disk, err := os.ReadFile(filepath.Join(root, "cfg", "gen/a.yaml"))
	if err != nil || !strings.HasPrefix(string(disk), genfiles.StampPrefix) {
		t.Fatalf("disk file = %q, %v; want a stamped file", disk, err)
	}

	// An identical write leaves the generation alone.
	if err := genfiles.Save[doc](ctx, log, s, "gen/a.yaml", &doc{Name: "fresh"}); err != nil {
		t.Fatal(err)
	}

	if s.Generation() != gen+1 {
		t.Errorf("identical write bumped generation to %d", s.Generation())
	}
}

func TestStoreReadDirMergesAndDeleteDrops(t *testing.T) {
	ctx, log := context.Background(), slog.Default()
	s, _ := newStore(t)

	if err := s.Write(ctx, log, "gen/new/c.yaml", []byte("name: c\n")); err != nil {
		t.Fatal(err)
	}

	entries, err := fs.ReadDir(s.FS(), "gen")
	if err != nil {
		t.Fatal(err)
	}

	names := map[string]bool{}
	for _, e := range entries {
		names[e.Name()] = e.IsDir()
	}

	if len(names) != 3 || !names["new"] || !names["sub"] || names["a.yaml"] {
		t.Errorf("entries = %v", names)
	}

	if !s.Exists("gen/new/c.yaml") {
		t.Fatal("written file not visible")
	}

	if err := s.Delete(ctx, log, "gen/new/c.yaml"); err != nil {
		t.Fatal(err)
	}

	if s.Exists("gen/new/c.yaml") {
		t.Error("deleted file still visible")
	}

	missing, err := genfiles.Load[doc](ctx, s, "gen/nope.yaml")
	if missing != nil || err != nil {
		t.Errorf("missing file = %v, %v; want nil, nil", missing, err)
	}
}

func TestStoreValidationFails(t *testing.T) {
	s, _ := newStore(t)

	err := genfiles.Save[doc](context.Background(), slog.Default(), s, "gen/x.yaml", &doc{})
	if err == nil {
		t.Fatal("want validation error")
	}
}
