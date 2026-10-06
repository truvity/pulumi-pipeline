package gensync_test

import (
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"testing"

	"github.com/truvity/pulumi-pipeline/pkg/gensync"
)

func recorder(got *map[string]map[string]any) gensync.Saver {
	return func(_ context.Context, _ *slog.Logger, path string, outputs map[string]any) error {
		(*got)[path] = outputs

		return nil
	}
}

func TestPathAndNames(t *testing.T) {
	if got := gensync.Path("a", "b-1"); got != "gen/a/b-1.yaml" {
		t.Fatalf("Path = %q", got)
	}

	for _, bad := range []string{"", "../x", "A", "a/b", "a b"} {
		if err := gensync.ValidateNames("ok", bad); err == nil {
			t.Errorf("%q accepted", bad)
		}
	}
}

func TestWrite(t *testing.T) {
	got := map[string]map[string]any{}
	log := slog.New(slog.DiscardHandler)

	if err := gensync.Write(t.Context(), log, recorder(&got), "s", "t", map[string]any{"k": "v"}); err != nil {
		t.Fatal(err)
	}

	if got["gen/s/t.yaml"]["k"] != "v" {
		t.Fatalf("saved = %v", got)
	}

	if err := gensync.Write(t.Context(), log, recorder(&got), "s", "t", nil); err == nil {
		t.Error("empty outputs accepted")
	}

	if err := gensync.Write(t.Context(), log, recorder(&got), "S", "t", map[string]any{"k": 1}); err == nil {
		t.Error("bad scope accepted")
	}
}

func TestHooksAreOptIn(t *testing.T) {
	root := t.TempDir()
	got := map[string]map[string]any{}
	hooks := gensync.Hooks{ConfigDir: "conf", Save: recorder(&got)}

	if hooks.OutputsFor(root, "s", "t") != nil {
		t.Fatal("a stack without a gen file must not be synced")
	}

	file := filepath.Join(root, "conf", gensync.Path("s", "t"))
	if err := os.MkdirAll(filepath.Dir(file), 0o750); err != nil {
		t.Fatal(err)
	}

	if err := os.WriteFile(file, []byte("{}\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	fn := hooks.OutputsFor(root, "s", "t")
	if fn == nil {
		t.Fatal("an enrolled stack must be synced")
	}

	if err := fn(t.Context(), slog.New(slog.DiscardHandler), map[string]any{"a": 1}); err != nil {
		t.Fatal(err)
	}

	if _, ok := got["gen/s/t.yaml"]; !ok {
		t.Fatalf("not saved: %v", got)
	}
}

func TestSyncReadsStackOutput(t *testing.T) {
	root := t.TempDir()
	bin := t.TempDir()
	script := "#!/bin/sh\necho '{\"url\":\"x\"}'\n"

	if err := os.WriteFile(filepath.Join(bin, "pulumi"), []byte(script), 0o700); err != nil { //nolint:gosec // a test stub must be executable
		t.Fatal(err)
	}

	if err := os.MkdirAll(filepath.Join(root, "deploy", "s"), 0o750); err != nil {
		t.Fatal(err)
	}

	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))

	got := map[string]map[string]any{}
	opts := gensync.SyncOptions{Root: root, ScopesDir: "deploy", Save: recorder(&got)}

	if err := gensync.Sync(t.Context(), slog.New(slog.DiscardHandler), opts, "s", "t"); err != nil {
		t.Fatal(err)
	}

	if got["gen/s/t.yaml"]["url"] != "x" {
		t.Fatalf("saved = %v", got)
	}
}
