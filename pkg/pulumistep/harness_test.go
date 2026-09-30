package pulumistep

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sync/atomic"
	"testing"

	"github.com/pulumi/pulumi/sdk/v3/go/auto"
	"github.com/stretchr/testify/require"
)

const testStack = "dev"

var (
	// programBinary is the compiled testdata program, built once for the run.
	programBinary string

	projectSeq atomic.Int64
)

// TestMain prepares what every integration test shares: one file backend, one
// Pulumi home and one compiled program. Each test gets its own project name, so
// they share the backend without sharing state, and can run in parallel.
//
// It needs the pulumi CLI and the Go language plugin on PATH -- the devbox
// shell provides both -- and a shell without them fails here rather than
// skipping, so a green run means the Automation API path really ran.
func TestMain(m *testing.M) {
	os.Exit(runTests(m))
}

func runTests(m *testing.M) int {
	for _, tool := range []string{"pulumi", "pulumi-language-go", "go"} {
		if _, err := exec.LookPath(tool); err != nil {
			fmt.Fprintf(os.Stderr, "%s is not on PATH; run the tests inside the devbox shell (just test)\n", tool)

			return 1
		}
	}

	dir, err := os.MkdirTemp("", "pulumistep-test-")
	if err != nil {
		fmt.Fprintln(os.Stderr, err)

		return 1
	}

	defer func() { _ = os.RemoveAll(dir) }()

	for k, v := range map[string]string{
		"PULUMI_BACKEND_URL":          "file://" + filepath.Join(dir, "state"),
		"PULUMI_CONFIG_PASSPHRASE":    "test-passphrase-not-a-secret",
		"PULUMI_HOME":                 filepath.Join(dir, "home"),
		"PULUMI_SKIP_UPDATE_CHECK":    "true",
		"PULUMI_DISABLE_CI_DETECTION": "true",
	} {
		if err := os.Setenv(k, v); err != nil {
			fmt.Fprintln(os.Stderr, err)

			return 1
		}
	}

	if err := os.MkdirAll(filepath.Join(dir, "state"), 0o755); err != nil {
		fmt.Fprintln(os.Stderr, err)

		return 1
	}

	programBinary = filepath.Join(dir, "program-bin")

	if out, err := exec.Command("go", "build", "-o", programBinary, "./internal/testprogram").CombinedOutput(); err != nil {
		fmt.Fprintf(os.Stderr, "building the test program: %v\n%s", err, out)

		return 1
	}

	return m.Run()
}

// harness is a Pulumi project in a temp directory, running the compiled test
// program against the shared file backend.
type harness struct {
	root    string // checkout root the steps run against
	workDir string // relative to root
	project string
}

func newHarness(t *testing.T) *harness {
	t.Helper()
	t.Parallel()

	root := t.TempDir()
	project := fmt.Sprintf("fixture%d", projectSeq.Add(1))
	workDir := filepath.Join("scopes", "net")
	abs := filepath.Join(root, workDir)

	require.NoError(t, os.MkdirAll(abs, 0o755))

	yaml := fmt.Sprintf("name: %s\nruntime:\n  name: go\n  options:\n    binary: %s\n", project, programBinary)
	require.NoError(t, os.WriteFile(filepath.Join(abs, "Pulumi.yaml"), []byte(yaml), 0o644))

	h := &harness{root: root, workDir: workDir, project: project}

	stack, err := auto.UpsertStackLocalSource(context.Background(), testStack, abs)
	require.NoError(t, err)
	require.NoError(t, stack.SetConfig(context.Background(), project+":count", auto.ConfigValue{Value: "2"}))

	return h
}

// setCount rewrites the program's config, which is how a test changes what the
// next preview plans.
func (h *harness) setCount(t *testing.T, n int) {
	t.Helper()

	stack := h.stack(t)
	require.NoError(t, stack.SetConfig(context.Background(), h.project+":count", auto.ConfigValue{Value: fmt.Sprint(n)}))
}

func (h *harness) stack(t *testing.T) auto.Stack {
	t.Helper()

	stack, err := auto.SelectStackLocalSource(context.Background(), testStack, filepath.Join(h.root, h.workDir))
	require.NoError(t, err)

	return stack
}
