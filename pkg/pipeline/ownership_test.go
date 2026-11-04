package pipeline

import (
	"context"
	"log/slog"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRemoteRepoPath(t *testing.T) {
	tests := []struct {
		raw  string
		want string
		ok   bool
	}{
		{"https://github.com/example-org/infra.git", "example-org/infra", true},
		{"https://github.com/example-org/infra", "example-org/infra", true},
		{"https://github.com/example-org/infra/", "example-org/infra", true},
		{"https://user:secret@github.com/example-org/infra.git\n", "example-org/infra", true},
		{"ssh://git@github.com/example-org/infra.git", "example-org/infra", true},
		{"ssh://git@github.com:22/example-org/infra.git", "example-org/infra", true},
		{"git@github.com:example-org/infra.git", "example-org/infra", true},
		{"git@work-alias:example-org/infra", "example-org/infra", true},
		{"https://gitlab.example.com/group/sub/infra.git", "group/sub/infra", true},
		{"/srv/git/infra.git", "", false},
		{"./relative/infra", "", false},
		{"file:///srv/git/example-org/infra.git", "", false},
		{"https://github.com/infra.git", "", false},
		{"git@github.com:infra", "", false},
		{"", "", false},
	}

	for _, tt := range tests {
		t.Run(tt.raw, func(t *testing.T) {
			got, ok := remoteRepoPath(tt.raw)
			assert.Equal(t, tt.ok, ok)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestOwnership_Validate(t *testing.T) {
	valid := []Ownership{
		{},
		{Origin: "example-org/infra"},
		{Origin: "group/sub/infra"},
		{Stacks: []string{"net/prod", "apps/*", "*/dev", "net/[a-c]*"}},
	}

	for _, o := range valid {
		assert.NoError(t, o.Validate(), "%+v", o)
	}

	invalid := map[string]Ownership{
		"one segment":           {Origin: "infra"},
		"padded origin":         {Origin: " example-org/infra"},
		"empty segment":         {Origin: "example-org//infra"},
		"stack without a scope": {Stacks: []string{"prod"}},
		"empty half":            {Stacks: []string{"net/"}},
		"too many slashes":      {Stacks: []string{"net/prod/x"}},
		"bad pattern":           {Stacks: []string{"net/[prod"}},
	}

	for name, o := range invalid {
		err := o.Validate()
		require.Error(t, err, name)
		assert.Contains(t, err.Error(), "invalid ownership declaration", name)
	}
}

func TestOwnership_Owns(t *testing.T) {
	o := Ownership{Stacks: []string{"net/prod", "apps/*"}}

	assert.True(t, o.Owns("net", "prod"))
	assert.True(t, o.Owns("apps", "dev"))
	assert.True(t, o.Owns("apps", "prod"))
	assert.False(t, o.Owns("net", "dev"))
	assert.False(t, o.Owns("org", "prod"))

	assert.True(t, Ownership{}.Owns("anything", "at-all"), "no declaration owns everything")
}

func setOrigin(t *testing.T, work, url string) {
	t.Helper()
	require.NoError(t, runGit(work, "remote", "set-url", "origin", url))
}

func TestCheckOrigin(t *testing.T) {
	ctx := context.Background()

	t.Run("matches, ignoring case and the host", func(t *testing.T) {
		work, _ := fixture(t)
		setOrigin(t, work, "git@work-alias:Example-Org/Infra.git")

		assert.NoError(t, CheckOrigin(ctx, work, "example-org/infra"))
	})

	t.Run("another repository", func(t *testing.T) {
		work, _ := fixture(t)
		setOrigin(t, work, "https://user:secret@github.com/example-org/fork.git")

		err := CheckOrigin(ctx, work, "example-org/infra")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "is a clone of example-org/fork")
		assert.Contains(t, err.Error(), "declares its origin as example-org/infra")
		assert.NotContains(t, err.Error(), "secret", "a credential in the URL must not be printed")
	})

	t.Run("a local remote cannot be identified", func(t *testing.T) {
		work, _ := fixture(t)

		err := CheckOrigin(ctx, work, "example-org/infra")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "not the URL of a hosted repository")
	})

	t.Run("the branch's own remote is the one checked", func(t *testing.T) {
		work, _ := fixture(t)
		setOrigin(t, work, "https://github.com/example-org/fork.git")
		require.NoError(t, runGit(work, "remote", "add", "canonical", "https://github.com/example-org/infra.git"))
		require.NoError(t, runGit(work, "config", "branch.master.remote", "canonical"))

		assert.NoError(t, CheckOrigin(ctx, work, "example-org/infra"))
	})

	t.Run("a detached HEAD falls back to origin", func(t *testing.T) {
		work, _ := fixture(t)
		setOrigin(t, work, "https://github.com/example-org/infra.git")
		detach(t, work)

		assert.NoError(t, CheckOrigin(ctx, work, "example-org/infra"))
	})

	t.Run("no such remote", func(t *testing.T) {
		work, _ := fixture(t)
		require.NoError(t, runGit(work, "remote", "rename", "origin", "elsewhere"))
		detach(t, work)

		err := CheckOrigin(ctx, work, "example-org/infra")
		require.Error(t, err)
		assert.Contains(t, err.Error(), `no remote "origin"`)
	})

	t.Run("not a git checkout", func(t *testing.T) {
		err := CheckOrigin(ctx, t.TempDir(), "example-org/infra")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "not a git checkout")
	})
}

func TestRunWith_OriginMismatchIsRefusedWithoutOverride(t *testing.T) {
	work, _ := fixture(t)
	setOrigin(t, work, "https://github.com/example-org/fork.git")

	// CI=true and --allow-stale-checkout together relax everything else the
	// checkout gate checks; the origin is still refused.
	withCI(t, true)
	detach(t, work)

	var ran []string

	cfg := gateConfig(work, &ran)
	cfg.Ownership = Ownership{Origin: "example-org/infra"}

	for _, action := range []Action{ActionExecute, ActionDestroy} {
		err := RunWith(context.Background(), quiet(), cfg, action, nil, RunOptions{All: true, AllowStaleCheckout: true})
		require.Error(t, err, action.String())
		assert.Contains(t, err.Error(), "refusing to change infrastructure")
		assert.Contains(t, err.Error(), "is a clone of example-org/fork")
		assert.Contains(t, err.Error(), "has no override")
	}

	assert.Empty(t, ran, "no step may run when the origin is refused")

	for _, action := range []Action{ActionDryRun, ActionSync} {
		require.NoError(t, RunWith(context.Background(), quiet(), cfg, action, nil, RunOptions{All: true}), action.String())
	}

	assert.Len(t, ran, 2, "diff and refresh are not gated")
}

func TestRunWith_OriginMatchProceeds(t *testing.T) {
	work, _ := fixture(t)
	setOrigin(t, work, "https://github.com/example-org/infra.git")

	// A detached HEAD under CI=true is the one way through the rest of the
	// gate without fetching from the (unreachable) URL above.
	withCI(t, true)
	detach(t, work)

	var ran []string

	cfg := gateConfig(work, &ran)
	cfg.Ownership = Ownership{Origin: "example-org/infra"}

	require.NoError(t, RunWith(context.Background(), quiet(), cfg, ActionExecute, nil, RunOptions{All: true}))
	assert.Equal(t, []string{"a"}, ran)
}

func TestRunWith_NoOriginDeclaredIsUnchanged(t *testing.T) {
	withCI(t, false)

	// The fixture's origin is a local path, which the origin check could not
	// identify; with nothing declared it is never asked.
	work, _ := fixture(t)

	var ran []string

	require.NoError(t, RunWith(context.Background(), quiet(), gateConfig(work, &ran), ActionExecute, nil, RunOptions{All: true}))
	assert.Equal(t, []string{"a"}, ran)
}

func stackStep(name string, ran *[]string) Step {
	s := ExecStep(name, func(_ context.Context, _ *slog.Logger, _ string, _ Action) (*StepResult, error) {
		*ran = append(*ran, name)

		return nil, nil
	}, WithActions(AllActions()))
	s.StackName = name

	return s
}

func ownedConfig(root string, ran *[]string, owned ...string) Config {
	return Config{
		Name:      "gate net",
		Root:      root,
		Scope:     "net",
		Ownership: Ownership{Stacks: owned},
		Steps: []Step{
			stackStep("dev", ran),
			stackStep("prod", ran),
			ExecStep("render", func(_ context.Context, _ *slog.Logger, _ string, _ Action) (*StepResult, error) {
				*ran = append(*ran, "render")

				return nil, nil
			}, WithActions(AllActions())),
		},
	}
}

func TestRunWith_StackOwnership(t *testing.T) {
	withCI(t, false)

	work, _ := fixture(t)

	t.Run("a stack outside the declaration is refused", func(t *testing.T) {
		for _, action := range []Action{ActionExecute, ActionDestroy} {
			var ran []string

			err := RunWith(context.Background(), quiet(), ownedConfig(work, &ran, "net/dev"), action, nil, RunOptions{All: true})
			require.Error(t, err, action.String())
			assert.Contains(t, err.Error(), "stacks this repository does not own")
			assert.Contains(t, err.Error(), "net/prod")
			assert.NotContains(t, err.Error(), "  - net/dev", "an owned stack is not listed as foreign")
			assert.Empty(t, ran, "nothing may run, not even the stacks it does own")
		}
	})

	t.Run("a dependency pulled in by resolution counts", func(t *testing.T) {
		var ran []string

		cfg := ownedConfig(work, &ran, "net/dev")
		cfg.Steps[0].DependsOn = []string{"prod"}

		err := RunWith(context.Background(), quiet(), cfg, ActionExecute, []string{"dev"}, RunOptions{})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "net/prod")
		assert.Empty(t, ran)
	})

	t.Run("owned stacks and non-stack steps run", func(t *testing.T) {
		var ran []string

		require.NoError(t, RunWith(context.Background(), quiet(), ownedConfig(work, &ran, "net/*"), ActionExecute, nil, RunOptions{All: true}))
		assert.ElementsMatch(t, []string{"dev", "prod", "render"}, ran)
	})

	t.Run("naming only owned stacks runs them", func(t *testing.T) {
		var ran []string

		require.NoError(t, RunWith(context.Background(), quiet(), ownedConfig(work, &ran, "net/dev"), ActionExecute,
			[]string{"dev", "render"}, RunOptions{Exclusive: true}))
		assert.ElementsMatch(t, []string{"dev", "render"}, ran)
	})

	t.Run("diff and refresh are not gated", func(t *testing.T) {
		var ran []string

		for _, action := range []Action{ActionDryRun, ActionSync} {
			require.NoError(t, RunWith(context.Background(), quiet(), ownedConfig(work, &ran, "other/*"), action, nil, RunOptions{All: true}))
		}

		assert.Len(t, ran, 6)
	})

	t.Run("a declaration without a scope name is refused", func(t *testing.T) {
		var ran []string

		cfg := ownedConfig(work, &ran, "net/*")
		cfg.Scope = ""

		err := RunWith(context.Background(), quiet(), cfg, ActionExecute, nil, RunOptions{All: true})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "Config.Scope")
		assert.Empty(t, ran)
	})

	t.Run("a malformed declaration is refused for every action", func(t *testing.T) {
		for _, action := range []Action{ActionDryRun, ActionExecute, ActionSync, ActionDestroy} {
			var ran []string

			err := RunWith(context.Background(), quiet(), ownedConfig(work, &ran, "prod"), action, nil, RunOptions{All: true})
			require.Error(t, err, action.String())
			assert.Contains(t, err.Error(), "invalid ownership declaration")
			assert.Empty(t, ran)
		}
	})

	t.Run("allow-stale-checkout does not cover it", func(t *testing.T) {
		var ran []string

		err := RunWith(context.Background(), quiet(), ownedConfig(work, &ran, "net/dev"), ActionExecute, nil,
			RunOptions{All: true, AllowStaleCheckout: true})
		require.Error(t, err)
		assert.True(t, strings.Contains(err.Error(), "does not own"))
		assert.Empty(t, ran)
	})
}

func TestBuildPipelineCommands_HandsOwnershipToEveryScope(t *testing.T) {
	work, _ := fixture(t)
	withCI(t, false)

	var ran []string

	cmds := BuildPipelineCommands(MultiConfig{
		Name:      "tool",
		Root:      work,
		Ownership: Ownership{Stacks: []string{"net/dev"}},
		Scopes:    []Scope{{Name: "net", Steps: []Step{stackStep("dev", &ran), stackStep("prod", &ran)}}},
	}, quiet(), nil, nil)
	require.Len(t, cmds, 1)

	err := cmds[0].Run(context.Background(), []string{"net", "deploy", "prod"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "net/prod")
	assert.Empty(t, ran)

	require.NoError(t, cmds[0].Run(context.Background(), []string{"net", "deploy", "dev"}))
	assert.Equal(t, []string{"dev"}, ran)
}
