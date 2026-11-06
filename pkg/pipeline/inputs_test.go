package pipeline

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	inputPath = "inputs/tenant-inputs.yaml"
	// inputSource carries a credential-shaped userinfo on purpose: it must
	// never reach a message.
	inputSource = "test://acme:s3cr3t-credential@registry.example/inputs/acme@latest"
)

var inputBytes = []byte("tenant: acme\nzones: [a, b]\n")

func digestOf(b []byte) string {
	sum := sha256.Sum256(b)

	return "sha256:" + hex.EncodeToString(sum[:])
}

// fakeResolver answers every source with one state, or one error.
type fakeResolver struct {
	state InputState
	err   error
	calls int
}

func (f *fakeResolver) Resolve(_ context.Context, _ string) (InputState, error) {
	f.calls++

	return f.state, f.err
}

// currentState is the published state of inputBytes, published an hour ago.
func currentState() InputState {
	return InputState{Digest: digestOf(inputBytes), PublishedAt: time.Now().Add(-time.Hour), Revision: "14"}
}

// inputsFixture is a clean checkout with the input committed, so the checkout
// gate passes and only the inputs gate is under test.
func inputsFixture(t *testing.T) string {
	t.Helper()

	work, _ := fixture(t)
	require.NoError(t, os.MkdirAll(filepath.Join(work, "inputs"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(work, inputPath), inputBytes, 0o644))
	require.NoError(t, runGit(work, "add", "."))
	require.NoError(t, runGit(work, "commit", "-m", "inputs"))
	require.NoError(t, runGit(work, "push", "origin", "master"))

	return work
}

func tenantInput() ExternalInput {
	return ExternalInput{Name: "tenant-inputs", Path: inputPath, Source: inputSource, MaxAge: 24 * time.Hour}
}

func inputsConfig(work string, ran *[]string, r InputResolver, ins ...ExternalInput) Config {
	cfg := gateConfig(work, ran)
	cfg.Inputs = ins
	cfg.InputResolvers = map[string]InputResolver{"test": r}

	return cfg
}

func deploy(cfg Config, opts RunOptions) error {
	opts.All = true

	return RunWith(context.Background(), quiet(), cfg, ActionExecute, nil, opts)
}

// logCapture is a logger whose output the test can read.
func logCapture() (*slog.Logger, *bytes.Buffer) {
	var buf bytes.Buffer

	return slog.New(slog.NewTextHandler(&buf, nil)), &buf
}

func TestInputsGate_DigestMismatchIsRefused(t *testing.T) {
	work := inputsFixture(t)

	stale := currentState()
	stale.Digest = digestOf([]byte("some other bytes"))

	cases := map[string]struct {
		ci   bool
		opts RunOptions
	}{
		"plain":                {false, RunOptions{}},
		"CI=true":              {true, RunOptions{}},
		"allow-stale-checkout": {false, RunOptions{AllowStaleCheckout: true}},
		"allow-stale-inputs":   {false, RunOptions{AllowStaleInputs: true}},
		"all of them":          {true, RunOptions{AllowStaleCheckout: true, AllowStaleInputs: true}},
	}

	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			withCI(t, tc.ci)

			var ran []string

			err := deploy(inputsConfig(work, &ran, &fakeResolver{state: stale}, tenantInput()), tc.opts)
			require.Error(t, err)
			assert.Contains(t, err.Error(), "external inputs are not current")
			assert.Contains(t, err.Error(), "tenant-inputs (inputs/tenant-inputs.yaml)")
			assert.Contains(t, err.Error(), "is not the published")
			assert.Contains(t, err.Error(), "revision 14")
			assert.Empty(t, ran, "no step may run on a copy that is not the published one")
		})
	}
}

func TestInputsGate_CurrentCopyProceeds(t *testing.T) {
	withCI(t, false)

	work := inputsFixture(t)

	var ran []string

	r := &fakeResolver{state: currentState()}
	require.NoError(t, deploy(inputsConfig(work, &ran, r, tenantInput()), RunOptions{}))
	assert.Equal(t, []string{"a"}, ran)
	assert.Equal(t, 1, r.calls)
}

func TestInputsGate_AgeOverMaxAgeIsRefused(t *testing.T) {
	withCI(t, false)

	work := inputsFixture(t)

	old := currentState()
	old.PublishedAt = time.Now().Add(-49 * time.Hour)

	var ran []string

	err := deploy(inputsConfig(work, &ran, &fakeResolver{state: old}, tenantInput()), RunOptions{AllowStaleCheckout: true})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "published 49h ago, older than max_age 24h")
	assert.Contains(t, err.Error(), "--allow-stale-inputs")
	assert.Empty(t, ran)

	t.Run("a publication with no time is refused", func(t *testing.T) {
		st := currentState()
		st.PublishedAt = time.Time{}

		err := deploy(inputsConfig(work, &ran, &fakeResolver{state: st}, tenantInput()), RunOptions{})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "no publication time")
		assert.Empty(t, ran)
	})

	t.Run("a publication in the future is refused", func(t *testing.T) {
		st := currentState()
		st.PublishedAt = time.Now().Add(2 * time.Hour)

		err := deploy(inputsConfig(work, &ran, &fakeResolver{state: st}, tenantInput()), RunOptions{})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "in the future")
		assert.Empty(t, ran)
	})
}

func TestInputsGate_AllowStaleInputsCoversAgeOnly(t *testing.T) {
	withCI(t, false)

	work := inputsFixture(t)

	old := currentState()
	old.PublishedAt = time.Now().Add(-49 * time.Hour)

	var ran []string

	log, buf := logCapture()

	err := RunWith(context.Background(), log, inputsConfig(work, &ran, &fakeResolver{state: old}, tenantInput()),
		ActionExecute, nil, RunOptions{All: true, AllowStaleInputs: true})
	require.NoError(t, err)
	assert.Equal(t, []string{"a"}, ran)
	assert.Contains(t, buf.String(), "level=WARN")
	assert.Contains(t, buf.String(), "PROCEEDING ON AN EXTERNAL INPUT PAST ITS MAX AGE")
	assert.NotContains(t, buf.String(), "s3cr3t-credential")

	t.Run("an age problem next to a digest problem is still refused", func(t *testing.T) {
		both := old
		both.Digest = digestOf([]byte("other"))

		ran = nil

		err := deploy(inputsConfig(work, &ran, &fakeResolver{state: both}, tenantInput()), RunOptions{AllowStaleInputs: true})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "is not the published")
		assert.Empty(t, ran)
	})

	t.Run("a failed lookup is still refused", func(t *testing.T) {
		ran = nil

		err := deploy(inputsConfig(work, &ran, &fakeResolver{err: errors.New("unreachable")}, tenantInput()),
			RunOptions{AllowStaleInputs: true})
		require.Error(t, err)
		assert.Empty(t, ran)
	})
}

func TestInputsGate_ResolverErrorIsRefusedWithoutTheSource(t *testing.T) {
	withCI(t, false)

	work := inputsFixture(t)

	// A real client error often quotes the URL it was given.
	failing := &fakeResolver{err: fmt.Errorf("GET %s: 401 unauthorized (password s3cr3t-credential)", inputSource)}

	var ran []string

	err := deploy(inputsConfig(work, &ran, failing, tenantInput()), RunOptions{})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "could not be resolved")
	assert.NotContains(t, err.Error(), inputSource)
	assert.NotContains(t, err.Error(), "s3cr3t-credential")
	assert.NotContains(t, err.Error(), "registry.example")
	assert.Empty(t, ran)

	t.Run("a scheme without a resolver is refused", func(t *testing.T) {
		cfg := inputsConfig(work, &ran, failing, tenantInput())
		cfg.InputResolvers = nil

		err := deploy(cfg, RunOptions{})
		require.Error(t, err)
		assert.Contains(t, err.Error(), `no resolver for the scheme "test"`)
		assert.NotContains(t, err.Error(), "s3cr3t-credential")
		assert.Empty(t, ran)
	})

	t.Run("a malformed digest from the resolver is refused", func(t *testing.T) {
		st := currentState()
		st.Digest = "md5:abc"

		err := deploy(inputsConfig(work, &ran, &fakeResolver{state: st}, tenantInput()), RunOptions{})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "malformed digest")
		assert.Empty(t, ran)
	})
}

func TestInputsGate_FileProblemsAreRefused(t *testing.T) {
	withCI(t, false)

	work := inputsFixture(t)

	outside := filepath.Join(t.TempDir(), "elsewhere.yaml")
	require.NoError(t, os.WriteFile(outside, inputBytes, 0o644))
	require.NoError(t, os.Symlink(outside, filepath.Join(work, "inputs", "link.yaml")))
	require.NoError(t, os.Symlink("tenant-inputs.yaml", filepath.Join(work, "inputs", "inside.yaml")))
	require.NoError(t, os.MkdirAll(filepath.Join(work, "inputs", "dir.yaml"), 0o755))
	require.NoError(t, runGit(work, "add", "-f", "inputs/link.yaml", "inputs/inside.yaml"))
	require.NoError(t, runGit(work, "commit", "-m", "links"))
	require.NoError(t, runGit(work, "push", "origin", "master"))

	cases := map[string]struct {
		path string
		want string
	}{
		"missing":        {"inputs/absent.yaml", "does not exist"},
		"symlink escape": {"inputs/link.yaml", "outside the checkout"},
		"a directory":    {"inputs/dir.yaml", "not a regular file"},
	}

	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			in := tenantInput()
			in.Path = tc.path

			var ran []string

			err := deploy(inputsConfig(work, &ran, &fakeResolver{state: currentState()}, in), RunOptions{})
			require.Error(t, err)
			assert.Contains(t, err.Error(), tc.want)
			assert.Empty(t, ran)
		})
	}

	t.Run("a symlink that stays inside the checkout is read", func(t *testing.T) {
		in := tenantInput()
		in.Path = "inputs/inside.yaml"

		var ran []string

		require.NoError(t, deploy(inputsConfig(work, &ran, &fakeResolver{state: currentState()}, in), RunOptions{}))
		assert.Equal(t, []string{"a"}, ran)
	})

	t.Run("a path that is not a local path is refused for every action", func(t *testing.T) {
		bad := []string{
			"../" + inputPath,
			"inputs/../../x.yaml",
			"/etc/hostname",
			"inputs//tenant-inputs.yaml",
			"./inputs/tenant-inputs.yaml",
			"inputs/user:s3cr3t-credential@host.yaml",
			`inputs\tenant-inputs.yaml`,
			"~/inputs.yaml",
			"",
		}

		for _, p := range bad {
			for _, action := range []Action{ActionDryRun, ActionExecute, ActionSync, ActionDestroy} {
				in := tenantInput()
				in.Path = p

				var ran []string

				err := RunWith(context.Background(), quiet(), inputsConfig(work, &ran, &fakeResolver{state: currentState()}, in),
					action, nil, RunOptions{All: true})
				require.Error(t, err, "%q %s", p, action)
				assert.Contains(t, err.Error(), "invalid inputs declaration")
				assert.NotContains(t, err.Error(), "s3cr3t-credential", "a credential in a path must not be printed")
				assert.Empty(t, ran)
			}
		}
	})
}

func TestInputsGate_Scoping(t *testing.T) {
	withCI(t, false)

	work := inputsFixture(t)

	stale := currentState()
	stale.Digest = digestOf([]byte("some other bytes"))

	in := tenantInput()
	in.Stacks = []string{"net/prod"}

	t.Run("a covered stack is refused", func(t *testing.T) {
		var ran []string

		cfg := ownedConfig(work, &ran)
		cfg.Inputs = []ExternalInput{in}
		cfg.InputResolvers = map[string]InputResolver{"test": &fakeResolver{state: stale}}

		err := RunWith(context.Background(), quiet(), cfg, ActionExecute, []string{"prod"}, RunOptions{Exclusive: true})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "external inputs are not current")
		assert.Empty(t, ran)
	})

	t.Run("a stack outside stacks proceeds and nothing is resolved", func(t *testing.T) {
		var ran []string

		r := &fakeResolver{state: stale}
		cfg := ownedConfig(work, &ran)
		cfg.Inputs = []ExternalInput{in}
		cfg.InputResolvers = map[string]InputResolver{"test": r}

		require.NoError(t, RunWith(context.Background(), quiet(), cfg, ActionExecute, []string{"dev", "render"}, RunOptions{Exclusive: true}))
		assert.ElementsMatch(t, []string{"dev", "render"}, ran)
		assert.Zero(t, r.calls, "an input no stack in the run reads is not looked up")
	})

	t.Run("a pattern is matched", func(t *testing.T) {
		var ran []string

		wide := in
		wide.Stacks = []string{"net/p*"}

		cfg := ownedConfig(work, &ran)
		cfg.Inputs = []ExternalInput{wide}
		cfg.InputResolvers = map[string]InputResolver{"test": &fakeResolver{state: stale}}

		require.Error(t, RunWith(context.Background(), quiet(), cfg, ActionExecute, []string{"prod"}, RunOptions{Exclusive: true}))
		assert.Empty(t, ran)
	})

	t.Run("stacks with no scope name to match against are refused", func(t *testing.T) {
		var ran []string

		cfg := ownedConfig(work, &ran)
		cfg.Scope = ""
		cfg.Inputs = []ExternalInput{in}
		cfg.InputResolvers = map[string]InputResolver{"test": &fakeResolver{state: currentState()}}

		err := RunWith(context.Background(), quiet(), cfg, ActionExecute, nil, RunOptions{All: true})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "Config.Scope")
		assert.Empty(t, ran)
	})
}

func TestInputsGate_FileEditedBetweenStepsIsRefused(t *testing.T) {
	withCI(t, false)

	work := inputsFixture(t)

	var ran []string

	cfg := inputsConfig(work, &ran, &fakeResolver{state: currentState()}, tenantInput())
	cfg.Steps = []Step{
		ExecStep("first", func(_ context.Context, _ *slog.Logger, root string, _ Action) (*StepResult, error) {
			ran = append(ran, "first")

			return nil, os.WriteFile(filepath.Join(root, inputPath), []byte("tenant: globex\n"), 0o644)
		}, WithActions(AllActions())),
		ExecStep("second", func(_ context.Context, _ *slog.Logger, _ string, _ Action) (*StepResult, error) {
			ran = append(ran, "second")

			return nil, nil
		}, WithActions(AllActions()), WithDependsOn("first")),
	}

	err := deploy(cfg, RunOptions{})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "step second")
	assert.Contains(t, err.Error(), "changed during the run")
	assert.Equal(t, []string{"first"}, ran, "the step after the edit must not run")
}

func TestInputsGate_DiffWarnsAndExpectNoChangesRefuses(t *testing.T) {
	withCI(t, false)

	work := inputsFixture(t)

	stale := currentState()
	stale.Digest = digestOf([]byte("some other bytes"))

	t.Run("diff warns and still runs", func(t *testing.T) {
		var ran []string

		log, buf := logCapture()

		err := RunWith(context.Background(), log, inputsConfig(work, &ran, &fakeResolver{state: stale}, tenantInput()),
			ActionDryRun, nil, RunOptions{All: true})
		require.NoError(t, err)
		assert.Equal(t, []string{"a"}, ran)
		assert.Contains(t, buf.String(), "level=WARN")
		assert.Contains(t, buf.String(), "EXTERNAL INPUT IS NOT CURRENT")
		assert.NotContains(t, buf.String(), "s3cr3t-credential")
	})

	t.Run("diff with a current input does not warn", func(t *testing.T) {
		var ran []string

		log, buf := logCapture()

		require.NoError(t, RunWith(context.Background(), log, inputsConfig(work, &ran, &fakeResolver{state: currentState()}, tenantInput()),
			ActionDryRun, nil, RunOptions{All: true}))
		assert.NotContains(t, buf.String(), "level=WARN")
	})

	t.Run("expect-no-changes is refused", func(t *testing.T) {
		var ran []string

		err := RunWith(context.Background(), quiet(), inputsConfig(work, &ran, &fakeResolver{state: stale}, tenantInput()),
			ActionDryRun, nil, RunOptions{All: true, ExpectNoChanges: true})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "external inputs are not current")
		assert.Empty(t, ran)
	})

	t.Run("allow-stale-inputs does not relax expect-no-changes", func(t *testing.T) {
		old := currentState()
		old.PublishedAt = time.Now().Add(-49 * time.Hour)

		var ran []string

		err := RunWith(context.Background(), quiet(), inputsConfig(work, &ran, &fakeResolver{state: old}, tenantInput()),
			ActionDryRun, nil, RunOptions{All: true, ExpectNoChanges: true, AllowStaleInputs: true})
		require.Error(t, err)
		assert.Empty(t, ran)
	})
}

func TestInputsGate_DestroyAndRefreshAreNotGated(t *testing.T) {
	withCI(t, false)

	work := inputsFixture(t)

	r := &fakeResolver{err: errors.New("publisher is down")}

	var ran []string

	for _, action := range []Action{ActionDestroy, ActionSync} {
		require.NoError(t, RunWith(context.Background(), quiet(), inputsConfig(work, &ran, r, tenantInput()),
			action, nil, RunOptions{All: true}), action.String())
	}

	assert.Len(t, ran, 2)
	assert.Zero(t, r.calls, "nothing is looked up for an action that is not gated")
}

func TestValidateInputs(t *testing.T) {
	valid := []ExternalInput{
		tenantInput(),
		{Name: "a", Path: "x.yaml", Source: "file:descriptor.json", MaxAge: time.Minute},
		{Name: "b1-c2", Path: "d/x.yaml", Source: "oci://registry.example/x@sha256:abc", MaxAge: time.Hour, Stacks: []string{"net/*", "*/dev"}},
	}

	require.NoError(t, ValidateInputs(nil))

	for _, in := range valid {
		require.NoError(t, ValidateInputs([]ExternalInput{in}), "%+v", in)
	}

	with := func(f func(*ExternalInput)) ExternalInput {
		in := tenantInput()
		f(&in)

		return in
	}

	invalid := map[string][]ExternalInput{
		"empty name":            {with(func(i *ExternalInput) { i.Name = "" })},
		"upper-case name":       {with(func(i *ExternalInput) { i.Name = "Tenant" })},
		"name with a slash":     {with(func(i *ExternalInput) { i.Name = "a/b" })},
		"duplicate name":        {tenantInput(), tenantInput()},
		"no max_age":            {with(func(i *ExternalInput) { i.MaxAge = 0 })},
		"max_age under 1m":      {with(func(i *ExternalInput) { i.MaxAge = 59 * time.Second })},
		"negative max_age":      {with(func(i *ExternalInput) { i.MaxAge = -time.Hour })},
		"no source":             {with(func(i *ExternalInput) { i.Source = "" })},
		"source with no scheme": {with(func(i *ExternalInput) { i.Source = "registry.example/inputs/acme" })},
		"scheme only":           {with(func(i *ExternalInput) { i.Source = "oci:" })},
		"source with a space":   {with(func(i *ExternalInput) { i.Source = "oci://a b" })},
		"bad stack pattern":     {with(func(i *ExternalInput) { i.Stacks = []string{"prod"} })},
	}

	for name, ins := range invalid {
		err := ValidateInputs(ins)
		require.Error(t, err, name)
		assert.Contains(t, err.Error(), "invalid inputs declaration", name)
		assert.NotContains(t, err.Error(), "s3cr3t-credential", name)
		assert.NotContains(t, err.Error(), "registry.example", name, "a source is never printed")
	}
}

func TestInputsGate_BadDeclarationIsRefusedForEveryAction(t *testing.T) {
	work := inputsFixture(t)

	bad := tenantInput()
	bad.MaxAge = 0

	for _, action := range []Action{ActionDryRun, ActionExecute, ActionSync, ActionDestroy} {
		var ran []string

		err := RunWith(context.Background(), quiet(), inputsConfig(work, &ran, &fakeResolver{state: currentState()}, bad),
			action, nil, RunOptions{All: true})
		require.Error(t, err, action.String())
		assert.Contains(t, err.Error(), "max_age")
		assert.Empty(t, ran)
	}
}

func TestInputsGate_NothingDeclaredIsUnchanged(t *testing.T) {
	withCI(t, false)

	work, _ := fixture(t)

	var ran []string

	cfg := gateConfig(work, &ran)
	cfg.InputResolvers = map[string]InputResolver{"test": &fakeResolver{err: errors.New("never called")}}

	require.NoError(t, deploy(cfg, RunOptions{}))
	assert.Equal(t, []string{"a"}, ran)
}

func TestRedactSource(t *testing.T) {
	msg := redactSource(fmt.Sprintf("Get %q: dial tcp: password s3cr3t-credential rejected for acme", inputSource), inputSource)
	assert.NotContains(t, msg, "s3cr3t-credential")
	assert.NotContains(t, msg, "registry.example")
	assert.Contains(t, msg, "<source>")
	assert.True(t, strings.HasPrefix(msg, "Get"))
}

func TestShortDuration(t *testing.T) {
	assert.Equal(t, "49h", shortDuration(49*time.Hour))
	assert.Equal(t, "24h", shortDuration(24*time.Hour+10*time.Second))
	assert.Equal(t, "1h30m", shortDuration(90*time.Minute))
	assert.Equal(t, "5m", shortDuration(5*time.Minute))
	assert.Equal(t, "0m", shortDuration(0))
}
