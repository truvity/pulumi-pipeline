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
