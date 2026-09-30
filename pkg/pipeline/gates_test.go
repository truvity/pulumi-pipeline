package pipeline

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func fixture(t *testing.T) (work, remote string) {
	t.Helper()

	work, remote, err := newCheckout(t.TempDir())
	require.NoError(t, err)

	return work, remote
}

func quiet() *slog.Logger { return slog.New(slog.NewTextHandler(os.Stderr, nil)) }

func TestCheckCheckout_CleanAndCurrent(t *testing.T) {
	work, _ := fixture(t)

	assert.Empty(t, CheckCheckout(context.Background(), work))
}

func TestCheckCheckout_Dirty(t *testing.T) {
	work, _ := fixture(t)
	require.NoError(t, os.WriteFile(filepath.Join(work, "untracked.txt"), []byte("x"), 0o644))

	problems := CheckCheckout(context.Background(), work)
	require.Len(t, problems, 1)
	assert.Contains(t, problems[0], "dirty")
}

func TestCheckCheckout_BehindUpstream(t *testing.T) {
	work, remote := fixture(t)

	// A second clone pushes a commit the first has not fetched: the remote
	// moved, and the checkout's own remote-tracking ref still looks current.
	other := filepath.Join(t.TempDir(), "other")
	require.NoError(t, runGit(filepath.Dir(other), "clone", remote, other))
	require.NoError(t, os.WriteFile(filepath.Join(other, "new.txt"), []byte("y"), 0o644))
	require.NoError(t, runGit(other, "add", "."))
	require.NoError(t, runGit(other, "commit", "-m", "advance"))
	require.NoError(t, runGit(other, "push", "origin", "master"))

	problems := CheckCheckout(context.Background(), work)
	require.Len(t, problems, 1)
	assert.Contains(t, problems[0], "1 commit(s) behind origin/master")
}

func TestCheckCheckout_AheadIsAllowed(t *testing.T) {
	work, _ := fixture(t)
	require.NoError(t, os.WriteFile(filepath.Join(work, "local.txt"), []byte("z"), 0o644))
	require.NoError(t, runGit(work, "add", "."))
	require.NoError(t, runGit(work, "commit", "-m", "local"))

	assert.Empty(t, CheckCheckout(context.Background(), work))
}

func TestCheckCheckout_NoUpstream(t *testing.T) {
	work, _ := fixture(t)
	require.NoError(t, runGit(work, "checkout", "-b", "unpublished"))

	problems := CheckCheckout(context.Background(), work)
	require.Len(t, problems, 1)
	assert.Contains(t, problems[0], "no upstream")
}

func TestCheckCheckout_FetchFailureIsAProblem(t *testing.T) {
	work, remote := fixture(t)
	require.NoError(t, os.RemoveAll(remote))

	problems := CheckCheckout(context.Background(), work)
	require.Len(t, problems, 1)
	assert.Contains(t, problems[0], "currency cannot be proven")
}

func TestCheckCheckout_NotGit(t *testing.T) {
	problems := CheckCheckout(context.Background(), t.TempDir())
	require.Len(t, problems, 1)
	assert.Contains(t, problems[0], "not a git checkout")
}

func gateConfig(root string, ran *[]string) Config {
	return Config{
		Name: "gate",
		Root: root,
		Steps: []Step{ExecStep("a", func(_ context.Context, _ *slog.Logger, _ string, _ Action) (*StepResult, error) {
			*ran = append(*ran, "a")

			return nil, nil
		}, WithActions(AllActions()))},
	}
}

func TestRunWith_DeployRefusesDirtyCheckout(t *testing.T) {
	withCI(t, false)

	work, _ := fixture(t)
	require.NoError(t, os.WriteFile(filepath.Join(work, "dirty.txt"), []byte("x"), 0o644))

	var ran []string

	for _, action := range []Action{ActionExecute, ActionDestroy} {
		err := RunWith(context.Background(), quiet(), gateConfig(work, &ran), action, nil, RunOptions{All: true})
		require.Error(t, err, action.String())
		assert.Contains(t, err.Error(), "refusing to change infrastructure")
		assert.Contains(t, err.Error(), "--allow-stale-checkout")
	}

	assert.Empty(t, ran, "no step may run when the gate refuses")
}

func TestRunWith_OverrideProceedsAndWarns(t *testing.T) {
	withCI(t, false)

	work, _ := fixture(t)
	require.NoError(t, os.WriteFile(filepath.Join(work, "dirty.txt"), []byte("x"), 0o644))

	var (
		ran []string
		log strings.Builder
	)

	logger := slog.New(slog.NewTextHandler(&log, nil))

	err := RunWith(context.Background(), logger, gateConfig(work, &ran), ActionExecute, nil,
		RunOptions{All: true, AllowStaleCheckout: true})
	require.NoError(t, err)
	assert.Equal(t, []string{"a"}, ran)
	assert.Contains(t, log.String(), "level=WARN")
	assert.Contains(t, log.String(), "NOT PROVEN CURRENT")
}

func TestRunWith_DryRunAndRefreshAreNotGated(t *testing.T) {
	withCI(t, false)

	work, _ := fixture(t)
	require.NoError(t, os.WriteFile(filepath.Join(work, "dirty.txt"), []byte("x"), 0o644))

	var ran []string

	for _, action := range []Action{ActionDryRun, ActionSync} {
		require.NoError(t, RunWith(context.Background(), quiet(), gateConfig(work, &ran), action, nil, RunOptions{All: true}))
	}

	assert.Len(t, ran, 2)
}

func summaryStep(name string, summary map[string]int) Step {
	return ExecStep(name, func(_ context.Context, _ *slog.Logger, _ string, _ Action) (*StepResult, error) {
		return &StepResult{StepName: name, ChangeSummary: summary}, nil
	})
}

func TestRunWith_ExpectNoChanges(t *testing.T) {
	work, _ := fixture(t)

	tests := []struct {
		name    string
		summary map[string]int
		wantErr string
	}{
		{"nothing reported", nil, ""},
		{"only same", map[string]int{"same": 12}, ""},
		{"same and read", map[string]int{"same": 3, "read": 1}, ""},
		{"an update", map[string]int{"same": 3, "update": 1}, "update=1"},
		{"a create and a delete", map[string]int{"create": 2, "delete": 1}, "create=2 delete=1"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := Config{Name: "x", Root: work, Steps: []Step{summaryStep("s", tt.summary)}}

			err := RunWith(context.Background(), quiet(), cfg, ActionDryRun, nil, RunOptions{All: true, ExpectNoChanges: true})
			if tt.wantErr == "" {
				require.NoError(t, err)

				return
			}

			require.Error(t, err)
			assert.Contains(t, err.Error(), "1 of 1 step(s) failed")
		})
	}

	// Without the flag the same change is not a failure.
	cfg := Config{Name: "x", Root: work, Steps: []Step{summaryStep("s", map[string]int{"update": 1})}}
	require.NoError(t, RunWith(context.Background(), quiet(), cfg, ActionDryRun, nil, RunOptions{All: true}))
}

func TestExpectNoChanges_MessageNamesTheOps(t *testing.T) {
	err := expectNoChanges("stack-a", &StepResult{ChangeSummary: map[string]int{"replace": 1, "same": 9, "update": 2}})
	require.Error(t, err)
	assert.Equal(t, "expected no changes but stack-a reports replace=1 update=2", err.Error())
}

func TestRunWith_ExpectNoChangesOnlyForDiff(t *testing.T) {
	work, _ := fixture(t)

	var ran []string

	err := RunWith(context.Background(), quiet(), gateConfig(work, &ran), ActionExecute, nil,
		RunOptions{All: true, ExpectNoChanges: true})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "only applies to diff")
	assert.Empty(t, ran)
}

func blockingStep(name string, honourContext bool) Step {
	return ExecStep(name, func(ctx context.Context, _ *slog.Logger, _ string, _ Action) (*StepResult, error) {
		if honourContext {
			<-ctx.Done()

			return nil, ctx.Err()
		}

		select {} // a step that finished its work and then never returns
	}, WithActions(AllActions()))
}

func TestRunWith_StepTimeoutStopsAStepThatHonoursItsContext(t *testing.T) {
	work, _ := fixture(t)
	cfg := Config{Name: "x", Root: work, Steps: []Step{blockingStep("slow", true)}}

	start := time.Now()
	err := RunWith(context.Background(), quiet(), cfg, ActionSync, nil, RunOptions{All: true, StepTimeout: 50 * time.Millisecond})

	require.Error(t, err)
	assert.Contains(t, err.Error(), "step slow: timed out after 50ms")
	assert.Less(t, time.Since(start), 5*time.Second)
}

func TestRunWith_StepTimeoutAbandonsAStepThatIgnoresItsContext(t *testing.T) {
	work, _ := fixture(t)
	cfg := Config{Name: "x", Root: work, Steps: []Step{blockingStep("hung", false)}}

	old := stepGrace
	stepGrace = 50 * time.Millisecond

	t.Cleanup(func() { stepGrace = old })

	err := RunWith(context.Background(), quiet(), cfg, ActionSync, nil, RunOptions{All: true, StepTimeout: 50 * time.Millisecond})

	require.Error(t, err)
	assert.Contains(t, err.Error(), "abandoned")
}

func TestRunWith_DiffTimeoutIsContinueOnError(t *testing.T) {
	work, _ := fixture(t)

	var ran []string

	cfg := Config{Name: "x", Root: work, Steps: []Step{blockingStep("slow", true), gateConfig(work, &ran).Steps[0]}}

	err := RunWith(context.Background(), quiet(), cfg, ActionDryRun, nil, RunOptions{All: true, StepTimeout: 50 * time.Millisecond})

	require.Error(t, err)
	assert.Equal(t, []string{"a"}, ran, "the sweep continues past the step that timed out")
}

func TestRunWith_NegativeTimeoutDisablesTheBound(t *testing.T) {
	work, _ := fixture(t)

	var ran []string

	require.NoError(t, RunWith(context.Background(), quiet(), gateConfig(work, &ran), ActionSync, nil,
		RunOptions{All: true, StepTimeout: -1}))
	assert.Equal(t, []string{"a"}, ran)
}

func TestRunStep_ParentCancellationIsNotATimeout(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	step := blockingStep("s", true)

	go func() {
		time.Sleep(20 * time.Millisecond)
		cancel()
	}()

	_, err := runStep(ctx, quiet(), &step, ".", ActionSync, time.Minute)
	require.Error(t, err)
	assert.True(t, errors.Is(err, context.Canceled))
	assert.NotContains(t, err.Error(), "timed out")
}

func TestRunOptions_DefaultTimeout(t *testing.T) {
	assert.Equal(t, DefaultStepTimeout, RunOptions{}.stepTimeout())
	assert.Equal(t, time.Second, RunOptions{StepTimeout: time.Second}.stepTimeout())
	assert.Negative(t, RunOptions{StepTimeout: -1}.stepTimeout())
}

func withCI(t *testing.T, on bool) {
	t.Helper()

	prev := ciEnv
	ciEnv = func() bool { return on }

	t.Cleanup(func() { ciEnv = prev })
}

// detach leaves the checkout on a detached HEAD, as a CI runner does.
func detach(t *testing.T, work string) {
	t.Helper()
	require.NoError(t, runGit(work, "checkout", "--detach", "HEAD"))
}

func TestRunWith_DetachedHeadRefusedOutsideCI(t *testing.T) {
	withCI(t, false)

	work, _ := fixture(t)
	detach(t, work)

	var ran []string

	err := RunWith(context.Background(), quiet(), gateConfig(work, &ran), ActionExecute, nil, RunOptions{All: true})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "no upstream")
	assert.Empty(t, ran)
}

func TestRunWith_CIProceedsFromDetachedHeadAndWarns(t *testing.T) {
	withCI(t, true)

	work, _ := fixture(t)
	detach(t, work)

	var (
		ran []string
		log strings.Builder
	)

	logger := slog.New(slog.NewTextHandler(&log, nil))

	for _, action := range []Action{ActionExecute, ActionDestroy} {
		require.NoError(t, RunWith(context.Background(), logger, gateConfig(work, &ran), action, nil, RunOptions{All: true}))
	}

	assert.Len(t, ran, 2)
	assert.Contains(t, log.String(), "level=WARN")
	assert.Contains(t, log.String(), "CHECKOUT CHECK SKIPPED (CI=true)")
	assert.Contains(t, log.String(), "upstream comparison")
}

func TestRunWith_CIStillRefusesDirtyTree(t *testing.T) {
	withCI(t, true)

	work, _ := fixture(t)
	detach(t, work)
	require.NoError(t, os.WriteFile(filepath.Join(work, "dirty.txt"), []byte("x"), 0o644))

	var ran []string

	err := RunWith(context.Background(), quiet(), gateConfig(work, &ran), ActionExecute, nil, RunOptions{All: true})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "dirty")
	assert.NotContains(t, err.Error(), "no upstream")
	assert.Empty(t, ran)
}

func TestRunWith_CIStillComparesABranchWithAnUpstream(t *testing.T) {
	withCI(t, true)

	work, remote := fixture(t)

	other := filepath.Join(t.TempDir(), "other")
	require.NoError(t, runGit(filepath.Dir(other), "clone", remote, other))
	require.NoError(t, os.WriteFile(filepath.Join(other, "new.txt"), []byte("y"), 0o644))
	require.NoError(t, runGit(other, "add", "."))
	require.NoError(t, runGit(other, "commit", "-m", "advance"))
	require.NoError(t, runGit(other, "push", "origin", "master"))

	var ran []string

	err := RunWith(context.Background(), quiet(), gateConfig(work, &ran), ActionExecute, nil, RunOptions{All: true})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "behind origin/master")
	assert.Empty(t, ran)
}

func TestRunWith_CINotAGitCheckoutStillRefused(t *testing.T) {
	withCI(t, true)

	var ran []string

	err := RunWith(context.Background(), quiet(), gateConfig(t.TempDir(), &ran), ActionExecute, nil, RunOptions{All: true})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not a git checkout")
}

func TestCIEnv_OnlyLiteralTrue(t *testing.T) {
	for val, want := range map[string]bool{"true": true, "1": false, "false": false, "": false, "TRUE": false} {
		t.Setenv("CI", val)
		assert.Equal(t, want, ciEnv(), "CI=%q", val)
	}
}
