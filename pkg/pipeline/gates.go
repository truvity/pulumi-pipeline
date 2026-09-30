package pipeline

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"sort"
	"strconv"
	"strings"
	"time"
)

const (
	// DefaultStepTimeout bounds one step when RunOptions leaves it zero. A
	// preview or an apply of a large stack takes minutes; half an hour is
	// far past any real one and short enough that a hang is found the same
	// day.
	DefaultStepTimeout = 30 * time.Minute

	// fetchTimeout bounds the fetch that proves a checkout is current.
	fetchTimeout = 60 * time.Second
)

var (
	// stepGrace is how long a step gets, after its context expires, to notice
	// and clean up (Pulumi releases its stack lock on cancel) before the run
	// stops waiting for it. A variable so tests need not wait it out.
	stepGrace = 30 * time.Second

	// quietOps are the summary keys that mean nothing changes.
	quietOps = map[string]bool{"same": true, "read": true}
)

func (o RunOptions) stepTimeout() time.Duration {
	if o.StepTimeout == 0 {
		return DefaultStepTimeout
	}

	return o.StepTimeout
}

// runStep runs one step under timeout (non-positive: unbounded).
//
// The step gets a context that expires at the deadline, which is what stops a
// well-behaved step. A step that ignores its context is abandoned after
// stepGrace and reported as timed out: the caller is about to exit non-zero
// anyway, and a runner that waits forever on a step that already finished its
// work is the incident this exists for.
func runStep(
	ctx context.Context,
	logger *slog.Logger,
	s *Step,
	repoRoot string,
	action Action,
	timeout time.Duration,
) (*StepResult, error) {
	if timeout <= 0 {
		return s.Fn(ctx, logger, repoRoot, action)
	}

	stepCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	type outcome struct {
		result *StepResult
		err    error
	}

	done := make(chan outcome, 1)

	go func() {
		result, err := s.Fn(stepCtx, logger, repoRoot, action)
		done <- outcome{result, err}
	}()

	select {
	case o := <-done:
		return o.result, o.err
	case <-stepCtx.Done():
	}

	if !errors.Is(stepCtx.Err(), context.DeadlineExceeded) {
		return nil, fmt.Errorf("cancelled: %w", stepCtx.Err())
	}

	timedOut := fmt.Errorf("timed out after %s; a stack lock may be left behind (run the scope's doctor)", timeout)

	select {
	case o := <-done:
		return o.result, fmt.Errorf("%w (step returned: %v)", timedOut, o.err)
	case <-time.After(stepGrace):
		return nil, fmt.Errorf("%w (step ignored cancellation for %s and was abandoned)", timedOut, stepGrace)
	}
}

// expectNoChanges is the --expect-no-changes gate for one step: nil when the
// step reported nothing, or only unchanged and read resources.
func expectNoChanges(step string, result *StepResult) error {
	if result == nil {
		return nil
	}

	var ops []string

	for op, n := range result.ChangeSummary {
		if n > 0 && !quietOps[op] {
			ops = append(ops, fmt.Sprintf("%s=%d", op, n))
		}
	}

	if len(ops) == 0 {
		return nil
	}

	sort.Strings(ops)

	return fmt.Errorf("expected no changes but %s reports %s", step, strings.Join(ops, " "))
}

// ciEnv reports whether the environment says this is a CI run: CI=true, the
// variable every major CI system exports. A variable so tests need not set the
// process environment.
var ciEnv = func() bool { return os.Getenv("CI") == "true" }

// requireFreshCheckout enforces the checkout gate for a mutating action, or,
// when allow is set, says loudly what it let through.
//
// Under CI=true the checks that cannot hold on a runner are skipped, each
// logged at WARN; a dirty tree is still refused. allow overrides everything,
// as before.
func requireFreshCheckout(ctx context.Context, logger *slog.Logger, root string, allow bool) error {
	ci := ciEnv() && !allow
	problems, skipped := checkCheckout(ctx, root, ci)

	for _, s := range skipped {
		logger.WarnContext(ctx, "CHECKOUT CHECK SKIPPED (CI=true)", slog.String("skipped", s),
			slog.String("reason", "CI=true: a CI runner checks out a detached HEAD, which has no upstream to compare with"),
		)
	}

	if len(problems) == 0 {
		return nil
	}

	if allow {
		for _, p := range problems {
			logger.WarnContext(ctx, "PROCEEDING FROM A CHECKOUT THAT IS NOT PROVEN CURRENT (--allow-stale-checkout)",
				slog.String("problem", p),
			)
		}

		return nil
	}

	hint := "fix the checkout, or override with --allow-stale-checkout if you mean it"
	if ci {
		hint = "CI=true relaxes only the upstream comparison; a dirty tree is still refused"
	}

	return fmt.Errorf("refusing to change infrastructure from this checkout:\n  - %s\n%s",
		strings.Join(problems, "\n  - "), hint)
}

// CheckCheckout reports why the checkout at root is not safe to apply from: a
// dirty tree, a branch behind its upstream, or a state it cannot verify (no
// git, no upstream, a fetch that failed). An empty result means it is clean
// and level with, or ahead of, its upstream.
//
// It fetches the upstream's remote first, because "behind" against a stale
// remote-tracking ref is exactly the mistake being guarded: the tree looks
// current until someone asks the remote.
func CheckCheckout(ctx context.Context, root string) []string {
	problems, _ := checkCheckout(ctx, root, false)

	return problems
}

// checkCheckout is CheckCheckout with the CI relaxation. With ci set, a
// checkout that has no upstream to compare with (a detached HEAD, which is how
// a CI runner checks out; or an unpublished branch) is not a problem: the
// upstream comparison is skipped and named in skipped instead. A dirty tree,
// and anything that is not a git checkout, are refused either way. A branch
// that does have an upstream is still compared, fetch and all: that check
// holds in CI too.
func checkCheckout(ctx context.Context, root string, ci bool) (problems, skipped []string) {

	if _, err := git(ctx, root, "rev-parse", "--git-dir"); err != nil {
		return []string{"not a git checkout, so it cannot be proven current"}, nil
	}

	if out, err := git(ctx, root, "status", "--porcelain", "--", "."); err != nil {
		problems = append(problems, fmt.Sprintf("cannot read the working tree state: %v", err))
	} else if lines := nonEmptyLines(out); len(lines) > 0 {
		problems = append(problems,
			fmt.Sprintf("the working tree is dirty (%d path(s), e.g. %s)", len(lines), strings.TrimSpace(lines[0])))
	}

	upstream, err := git(ctx, root, "rev-parse", "--abbrev-ref", "--symbolic-full-name", "@{u}")
	if err != nil && ci {
		return problems, []string{"upstream comparison (detached HEAD or no upstream branch; dirty-tree check still applied)"}
	}

	if err != nil {
		return append(problems, "no upstream branch is configured (detached HEAD or an unpublished branch), so it cannot be compared"), nil
	}

	upstream = strings.TrimSpace(upstream)

	branch, err := git(ctx, root, "symbolic-ref", "--short", "HEAD")
	if err != nil {
		return append(problems, "HEAD is detached, so it cannot be compared with an upstream"), nil
	}

	remote, err := git(ctx, root, "config", "--get", "branch."+strings.TrimSpace(branch)+".remote")
	if err != nil {
		return append(problems, "the branch names no remote to fetch"), nil
	}

	remote = strings.TrimSpace(remote)

	fetchCtx, cancel := context.WithTimeout(ctx, fetchTimeout)
	defer cancel()

	if _, err := git(fetchCtx, root, "fetch", "--quiet", remote); err != nil {
		return append(problems, fmt.Sprintf("fetching %s failed, so currency cannot be proven: %v", remote, err)), nil
	}

	counts, err := git(ctx, root, "rev-list", "--left-right", "--count", "HEAD...@{u}")
	if err != nil {
		return append(problems, fmt.Sprintf("cannot compare with %s: %v", upstream, err)), nil
	}

	fields := strings.Fields(counts)
	if len(fields) != 2 {
		return append(problems, fmt.Sprintf("cannot parse the comparison with %s: %q", upstream, counts)), nil
	}

	behind, err := strconv.Atoi(fields[1])
	if err != nil {
		return append(problems, fmt.Sprintf("cannot parse the comparison with %s: %q", upstream, counts)), nil
	}

	if behind > 0 {
		problems = append(problems, fmt.Sprintf("the branch is %d commit(s) behind %s", behind, upstream))
	}

	return problems, nil
}

// git runs one git command in dir and returns its stdout. Prompts are off: a
// credential prompt on a runner is a hang, and a hang is a failure here.
func git(ctx context.Context, dir string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, "git", append([]string{"-C", dir}, args...)...)
	cmd.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0", "LC_ALL=C")

	var stdout, stderr bytes.Buffer

	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		if msg := strings.TrimSpace(stderr.String()); msg != "" {
			return "", fmt.Errorf("git %s: %s", args[0], msg)
		}

		return "", fmt.Errorf("git %s: %w", args[0], err)
	}

	return stdout.String(), nil
}

func nonEmptyLines(s string) []string {
	var out []string

	for _, l := range strings.Split(s, "\n") {
		if strings.TrimSpace(l) != "" {
			out = append(out, l)
		}
	}

	return out
}
