package pipeline

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"path"
	"sort"
	"strings"
)

// Ownership is what a repository declares about itself so that a deploy run
// from the wrong repository is refused. The zero value declares nothing and
// turns both of its gates off, which is how every existing caller keeps
// working until it declares.
type Ownership struct {
	// Origin is the repository the checkout must be a clone of, as its path
	// on the hosting service: "owner/name" on GitHub, "group/sub/name" where
	// groups nest. It is compared, case-insensitively, with the path of the
	// URL of the remote the checkout gate compares against. Empty: not
	// checked.
	Origin string
	// Stacks are the stacks the repository owns, as "scope/stack". Either
	// half may be a path.Match pattern ("network/*"). A deploy or destroy of
	// a stack that matches none of them is refused. Empty: not checked.
	Stacks []string
}

// Validate reports a malformed declaration: an Origin that is not a
// repository path, or a Stacks entry that is not "scope/stack" or is not a
// valid pattern.
func (o Ownership) Validate() error {
	var problems []string

	if o.Origin != "" {
		if _, ok := cleanRepoPath(o.Origin); !ok || o.Origin != strings.TrimSpace(o.Origin) {
			problems = append(problems, fmt.Sprintf("origin %q is not a repository path such as owner/name", o.Origin))
		}
	}

	for _, p := range o.Stacks {
		if err := validStackPattern(p); err != nil {
			problems = append(problems, err.Error())
		}
	}

	if len(problems) > 0 {
		return fmt.Errorf("invalid ownership declaration: %s", strings.Join(problems, "; "))
	}

	return nil
}

// Owns reports whether the declaration covers scope/stack. With no Stacks it
// covers everything.
func (o Ownership) Owns(scope, stack string) bool {
	if len(o.Stacks) == 0 {
		return true
	}

	key := scope + "/" + stack

	for _, p := range o.Stacks {
		if ok, err := path.Match(p, key); err == nil && ok {
			return true
		}
	}

	return false
}

func validStackPattern(p string) error {
	scope, stack, found := strings.Cut(p, "/")
	if !found || scope == "" || stack == "" || strings.Contains(stack, "/") || strings.TrimSpace(p) != p {
		return fmt.Errorf("stack %q is not scope/stack", p)
	}

	if _, err := path.Match(p, ""); err != nil {
		return fmt.Errorf("stack %q is not a valid pattern: %w", p, err)
	}

	return nil
}

// requireOwnership refuses a run that would apply a stack the declaration
// does not cover. Steps that are not stacks (no StackName) are not checked,
// and neither are steps that will be skipped because they do not support the
// action.
func requireOwnership(cfg Config, steps []Step, action Action) error {
	if len(cfg.Ownership.Stacks) == 0 {
		return nil
	}

	if cfg.Scope == "" {
		return errors.New("refusing to change infrastructure: this repository declares the stacks it owns, " +
			"but the scope has no name to check them against (set Config.Scope)")
	}

	var foreign []string

	for i := range steps {
		s := &steps[i]
		if s.StackName == "" || !s.Actions.Supports(action) {
			continue
		}

		if !cfg.Ownership.Owns(cfg.Scope, s.StackName) {
			foreign = append(foreign, cfg.Scope+"/"+s.StackName)
		}
	}

	if len(foreign) == 0 {
		return nil
	}

	sort.Strings(foreign)

	verb := "deploy"
	if action == ActionDestroy {
		verb = "destroy"
	}

	return fmt.Errorf("refusing to %s stacks this repository does not own:\n  - %s\n"+
		"it declares that it owns: %s\n"+
		"apply them from the repository that owns them, or add them to this one's declaration if it does",
		verb, strings.Join(foreign, "\n  - "), strings.Join(cfg.Ownership.Stacks, ", "))
}

// CheckOrigin reports, as an error, why the checkout at root is not a clone of
// want ("owner/name"): no git checkout, no remote to identify it by, a remote
// URL that names no hosted repository, or one that names another. The remote
// is the one the checkout gate compares against: the current branch's, or
// "origin" when HEAD is detached or the branch has none.
//
// Only the repository path is compared, not the host: an SSH host alias
// (git@work-github:owner/name) is as good as the real host name. The URL
// itself is never printed, because it may carry a credential.
func CheckOrigin(ctx context.Context, root, want string) error {
	wantPath, ok := cleanRepoPath(want)
	if !ok {
		return fmt.Errorf("the declared origin %q is not a repository path such as owner/name", want)
	}

	if _, err := git(ctx, root, "rev-parse", "--git-dir"); err != nil {
		return errors.New("not a git checkout, so its origin cannot be identified")
	}

	remote := "origin"

	if branch, err := git(ctx, root, "symbolic-ref", "--short", "HEAD"); err == nil {
		r, err := git(ctx, root, "config", "--get", "branch."+strings.TrimSpace(branch)+".remote")
		if r = strings.TrimSpace(r); err == nil && r != "" && r != "." {
			remote = r
		}
	}

	raw, err := git(ctx, root, "remote", "get-url", remote)
	if err != nil {
		return fmt.Errorf("the checkout has no remote %q, so its origin cannot be identified", remote)
	}

	got, ok := remoteRepoPath(raw)
	if !ok {
		return fmt.Errorf("remote %q is not the URL of a hosted repository, so its origin cannot be identified", remote)
	}

	if !strings.EqualFold(got, wantPath) {
		return fmt.Errorf("remote %q is a clone of %s, but this repository declares its origin as %s", remote, got, wantPath)
	}

	return nil
}

// requireOrigin is the origin half of the checkout gate. Unlike the rest of
// the gate it has no override and no CI relaxation: a runner checks out the
// repository it was triggered from, so the check holds there, and a wrong
// repository is not a state that a person can mean to apply from.
func requireOrigin(ctx context.Context, root, want string) error {
	if want == "" {
		return nil
	}

	if err := CheckOrigin(ctx, root, want); err != nil {
		return fmt.Errorf("refusing to change infrastructure from this checkout:\n  - %w\n"+
			"the origin check has no override (--allow-stale-checkout does not cover it): "+
			"run from a clone of %s, point the remote at it (git remote set-url), "+
			"or correct the declaration", err, want)
	}

	return nil
}

// remoteRepoPath extracts the repository path from a git remote URL: the
// scheme form (https://host/owner/name.git, ssh://git@host/owner/name) and
// the scp-like form (git@host:owner/name.git). A local path or a file:// URL
// names no hosted repository.
func remoteRepoPath(raw string) (string, bool) {
	raw = strings.TrimSpace(raw)

	var p string

	switch {
	case strings.Contains(raw, "://"):
		u, err := url.Parse(raw)
		if err != nil || u.Host == "" || u.Scheme == "file" {
			return "", false
		}

		p = u.Path
	default:
		// scp-like: [user@]host:path, where the host holds no slash. A
		// path with a slash before its first colon is a local path.
		i := strings.Index(raw, ":")
		if i <= 0 || strings.Contains(raw[:i], "/") {
			return "", false
		}

		p = raw[i+1:]
	}

	return cleanRepoPath(p)
}

// cleanRepoPath normalises a repository path: surrounding slashes and a .git
// suffix dropped. It must have at least two non-empty segments, none holding
// a character that a hosted repository path never does. Refusing '@' and ':'
// matters beyond tidiness: a scp-like remote with userinfo before the host
// (user:token@host:owner/name) would otherwise yield a "path" that carries
// the credential, and the mismatch error prints the path. A leading '~' is a
// home-relative path (ssh://host/~/name), not a hosted repository.
func cleanRepoPath(p string) (string, bool) {
	p = strings.Trim(strings.TrimSpace(p), "/")
	p = strings.Trim(strings.TrimSuffix(p, ".git"), "/")

	segments := strings.Split(p, "/")
	if len(segments) < 2 {
		return "", false
	}

	for _, s := range segments {
		if s == "" || s == "." || s == ".." || strings.HasPrefix(s, "~") || strings.ContainsAny(s, " \t\\@:[]") {
			return "", false
		}
	}

	return p, true
}
