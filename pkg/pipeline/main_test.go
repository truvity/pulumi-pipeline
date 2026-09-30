package pipeline

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// TestMain runs the whole package's tests from a clean, current git checkout.
//
// The engine tests execute deploys and destroys, which now refuse a checkout
// that is dirty or behind; the repository these tests live in is neither
// reliably clean nor guaranteed an upstream, so each run gets a fixture that
// is. The gate's own tests build their own fixtures and never rely on this
// one.
func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "pipeline-test-")
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}

	work, _, err := newCheckout(dir)
	if err == nil {
		err = os.Chdir(work)
	}

	if err != nil {
		fmt.Fprintln(os.Stderr, "fixture:", err)
		_ = os.RemoveAll(dir)

		os.Exit(1)
	}

	code := m.Run()

	_ = os.RemoveAll(dir)

	os.Exit(code)
}

// runGit runs git in dir with a fixed identity, given through the environment
// so nothing is written to any git configuration.
func runGit(dir string, args ...string) error {
	cmd := exec.Command("git", append([]string{"-C", dir, "-c", "commit.gpgsign=false", "-c", "protocol.file.allow=always"}, args...)...)
	cmd.Env = append(os.Environ(),
		"GIT_AUTHOR_NAME=Test", "GIT_AUTHOR_EMAIL=test@example.com",
		"GIT_COMMITTER_NAME=Test", "GIT_COMMITTER_EMAIL=test@example.com",
	)

	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("git %v: %w: %s", args, err, out)
	}

	return nil
}

// newCheckout makes a bare remote and a working checkout of it under dir: one
// commit, on master, tracking origin/master, clean.
func newCheckout(dir string) (work, remote string, err error) {
	remote = filepath.Join(dir, "remote.git")
	work = filepath.Join(dir, "work")

	for _, d := range []string{remote, work} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			return "", "", err
		}
	}

	if err := runGit(remote, "init", "--bare", "-b", "master"); err != nil {
		return "", "", err
	}

	if err := runGit(work, "init", "-b", "master"); err != nil {
		return "", "", err
	}

	if err := os.WriteFile(filepath.Join(work, "go.mod"), []byte("module example.com/fixture\n"), 0o644); err != nil {
		return "", "", err
	}

	for _, args := range [][]string{
		{"add", "."},
		{"commit", "-m", "init"},
		{"remote", "add", "origin", remote},
		{"push", "-u", "origin", "master"},
	} {
		if err := runGit(work, args...); err != nil {
			return "", "", err
		}
	}

	return work, remote, nil
}
