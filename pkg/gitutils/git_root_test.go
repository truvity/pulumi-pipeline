package gitutils_test

import (
	"errors"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/truvity/pulumi-pipeline/pkg/gitutils"
)

func TestGitRoot(t *testing.T) {
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}

	t.Chdir(dir)
	t.Setenv("GIT_CEILING_DIRECTORIES", filepath.Dir(dir))

	_, err = gitutils.GitRoot(t.Context(), slog.New(slog.DiscardHandler))
	if !errors.Is(err, gitutils.ErrNotInRepo) {
		t.Fatalf("outside a repository: got %v, want ErrNotInRepo", err)
	}

	if out, initErr := exec.CommandContext(t.Context(), "git", "init", "-q", dir).CombinedOutput(); initErr != nil {
		t.Fatalf("git init: %v: %s", initErr, out)
	}

	sub := filepath.Join(dir, "a", "b")
	if err := os.MkdirAll(sub, 0o750); err != nil {
		t.Fatal(err)
	}

	t.Chdir(sub)
	t.Setenv("GIT_DIR", filepath.Join(t.TempDir(), "elsewhere"))

	got, err := gitutils.GitRoot(t.Context(), slog.New(slog.DiscardHandler))
	if err != nil {
		t.Fatal(err)
	}

	if got != dir {
		t.Fatalf("GitRoot = %q, want %q", got, dir)
	}
}

func TestGitRoot_IgnoresHookEnvironment(t *testing.T) {
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}

	if out, initErr := exec.CommandContext(t.Context(), "git", "init", "-q", dir).CombinedOutput(); initErr != nil {
		t.Fatalf("git init: %v: %s", initErr, out)
	}

	t.Chdir(dir)
	t.Setenv("GIT_DIR", filepath.Join(t.TempDir(), "elsewhere"))
	t.Setenv("GIT_WORK_TREE", t.TempDir())
	t.Setenv("GIT_INDEX_FILE", filepath.Join(t.TempDir(), "index"))

	got, err := gitutils.GitRoot(t.Context(), slog.New(slog.DiscardHandler))
	if err != nil {
		t.Fatal(err)
	}

	if got != dir {
		t.Fatalf("GitRoot = %q, want %q", got, dir)
	}
}

func TestRootResolver(t *testing.T) {
	var r gitutils.RootResolver

	r.Set("/somewhere")

	got, err := r.Get(t.Context(), slog.New(slog.DiscardHandler))
	if err != nil || got != "/somewhere" {
		t.Fatalf("Get after Set = %q, %v", got, err)
	}

	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}

	if out, initErr := exec.CommandContext(t.Context(), "git", "init", "-q", dir).CombinedOutput(); initErr != nil {
		t.Fatalf("git init: %v: %s", initErr, out)
	}

	t.Chdir(dir)
	r.Reset()

	got, err = r.Get(t.Context(), slog.New(slog.DiscardHandler))
	if err != nil || got != dir {
		t.Fatalf("Get after Reset = %q, %v; want %q", got, err, dir)
	}
}
