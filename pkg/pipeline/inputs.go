package pipeline

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
	"unicode"
)

const (
	// MinInputMaxAge is the shortest max_age an input may declare. Anything
	// shorter than a publisher's own latency would refuse every deploy.
	MinInputMaxAge = time.Minute

	// inputResolveTimeout bounds one lookup of a published state.
	inputResolveTimeout = 60 * time.Second

	// maxInputBytes bounds a local copy that is hashed. An input is a small
	// configuration document; a file past this is a mistake.
	maxInputBytes = 64 << 20

	// inputClockSkew is how far in the future a publication time may be
	// before it is refused rather than read as "just now".
	inputClockSkew = time.Minute

	digestPrefix = "sha256:"
)

var (
	inputNameRE   = regexp.MustCompile(`^[a-z0-9]+(-[a-z0-9]+)*$`)
	inputSchemeRE = regexp.MustCompile(`^([a-z][a-z0-9+.-]*):\S+$`)
	digestRE      = regexp.MustCompile(`^sha256:[0-9a-f]{64}$`)

	// inputsNow is the clock; a variable so tests need not wait.
	inputsNow = time.Now
)

type (
	// ExternalInput is a file the deploy reads that is not produced from the
	// checkout: it is published elsewhere, and a copy committed here is only
	// a cache of it. Declaring one makes a deploy refuse to run on a copy that
	// is not the published one or that was published too long ago.
	ExternalInput struct {
		// Name identifies the input in messages: lower-case letters, digits
		// and single hyphens, unique among the inputs.
		Name string
		// Path is the local copy, relative to the checkout root. It must stay
		// inside the checkout, symbolic links included.
		Path string
		// Source is the opaque reference the published state is resolved
		// from, "scheme:...". The scheme picks the InputResolver. It may carry
		// a credential, so it is never printed.
		Source string
		// MaxAge is the longest a deploy may trail the publication. Required,
		// at least MinInputMaxAge.
		MaxAge time.Duration
		// Stacks narrows the input to the stacks it feeds, as "scope/stack"
		// patterns (see Ownership.Stacks). Empty: every stack.
		Stacks []string
	}

	// InputState is what a publisher says about an input.
	InputState struct {
		// Digest is "sha256:" and 64 lower-case hex digits, over the raw
		// bytes of the published file.
		Digest string
		// PublishedAt is when that digest was published.
		PublishedAt time.Time
		// Revision is an opaque label for the publication, shown in messages.
		Revision string
	}

	// InputResolver reads the published state of an input. The library does
	// no fetching of its own, so it carries no registry client; a caller
	// supplies a resolver per source scheme.
	//
	// An error should not repeat the source, which may carry a credential.
	// The gate removes the source string from what it prints, but cannot
	// know every form a client quotes it in.
	InputResolver interface {
		Resolve(ctx context.Context, source string) (InputState, error)
	}

	// InputResolverFunc adapts a function to an InputResolver.
	InputResolverFunc func(ctx context.Context, source string) (InputState, error)

	// inputGuard is what a passed gate leaves behind: the inputs it verified
	// and the digest of each local copy at that moment, for the per-step check.
	inputGuard struct {
		inputs  []ExternalInput
		digests map[string]string
	}

	// inputProblem is one reason an input is not current. ageOnly marks the
	// one reason AllowStaleInputs may waive.
	inputProblem struct {
		text    string
		ageOnly bool
	}
)

// Resolve calls f.
func (f InputResolverFunc) Resolve(ctx context.Context, source string) (InputState, error) {
	return f(ctx, source)
}

// ValidateInputs reports a malformed declaration: a name that is not
// [a-z0-9-]+ or is repeated, a path that is not a plain relative path inside
// the checkout, a source with no scheme, a max_age under MinInputMaxAge, or a
// Stacks entry that is not a valid "scope/stack" pattern. A source is never
// printed, because it may carry a credential; neither is a path, for the same
// reason.
func ValidateInputs(inputs []ExternalInput) error {
	var problems []string

	seen := make(map[string]bool, len(inputs))

	for i := range inputs {
		in := &inputs[i]
		who := fmt.Sprintf("input #%d", i+1)

		if inputNameRE.MatchString(in.Name) {
			who = fmt.Sprintf("input %q", in.Name)

			if seen[in.Name] {
				problems = append(problems, who+": the name is used twice")
			}

			seen[in.Name] = true
		} else {
			problems = append(problems, who+": name must be lower-case letters, digits and hyphens")
		}

		if reason := badInputPath(in.Path); reason != "" {
			problems = append(problems, who+": path "+reason)
		}

		if !inputSchemeRE.MatchString(in.Source) {
			problems = append(problems, who+": source must be a reference with a scheme, such as scheme:...")
		}

		if in.MaxAge < MinInputMaxAge {
			problems = append(problems, fmt.Sprintf("%s: max_age is required and must be at least %s", who, MinInputMaxAge))
		}

		for _, p := range in.Stacks {
			if err := validStackPattern(p); err != nil {
				problems = append(problems, who+": stacks: "+err.Error())
			}
		}
	}

	if len(problems) > 0 {
		return fmt.Errorf("invalid inputs declaration: %s", strings.Join(problems, "; "))
	}

	return nil
}

// badInputPath says why p is not acceptable, or returns "" when it is. It is
// a plain, clean, relative path: no "..", no absolute or home-relative form,
// no backslash, and no '@' or ':' in any segment, which is where a credential
// pasted into a path would sit.
func badInputPath(p string) string {
	switch {
	case p == "":
		return "is required"
	case strings.TrimSpace(p) != p:
		return "has surrounding whitespace"
	case filepath.IsAbs(p) || strings.HasPrefix(p, "/"):
		return "must be relative to the checkout root"
	case strings.Contains(p, `\`):
		return "must use forward slashes"
	case strings.ContainsAny(p, "@:"):
		return "must not contain '@' or ':'"
	case strings.HasPrefix(p, "~"):
		return "must not be home-relative"
	}

	for _, seg := range strings.Split(p, "/") {
		if seg == "" || seg == "." || seg == ".." {
			return "must be clean and stay inside the checkout"
		}
	}

	return ""
}

// covers reports whether the input feeds a stack in the run: with no Stacks
// every run, otherwise a stack step of the run that matches one of them.
func (in *ExternalInput) covers(scope string, steps []Step, action Action) bool {
	if len(in.Stacks) == 0 {
		return true
	}

	own := Ownership{Stacks: in.Stacks}

	for i := range steps {
		s := &steps[i]
		if s.StackName != "" && s.Actions.Supports(action) && own.Owns(scope, s.StackName) {
			return true
		}
	}

	return false
}

// requireInputs is the external-inputs gate. For deploy it refuses a run
// whose inputs are not current; for diff it logs each problem, and refuses
// only with ExpectNoChanges, because a proof of no change on stale input is
// worthless; destroy and refresh are not gated. It returns the guard that
// the run uses to re-hash the inputs before each step, nil when nothing was
// verified.
//
// A digest mismatch, a missing or unreadable file and a failed lookup have no
// override. A publication past max_age is waived by AllowStaleInputs, and
// logged at WARN. CI=true and AllowStaleCheckout relax nothing here.
func requireInputs(
	ctx context.Context,
	logger *slog.Logger,
	cfg Config,
	root string,
	steps []Step,
	action Action,
	opts RunOptions,
) (*inputGuard, error) {
	if len(cfg.Inputs) == 0 || (action != ActionExecute && action != ActionDryRun) {
		return nil, nil
	}

	covered, err := coveredInputs(cfg, steps, action)
	if err != nil || len(covered) == 0 {
		return nil, err
	}

	guard := &inputGuard{digests: make(map[string]string, len(covered))}

	var problems []inputProblem

	for i := range covered {
		in := &covered[i]

		digest, ps := checkInput(ctx, root, in, cfg.InputResolvers)
		problems = append(problems, ps...)

		// An input whose only problem is its age may be waived below, and the
		// file then still has to hold until the last step: it is guarded
		// whenever the local copy hashed cleanly and nothing but age is wrong.
		if digest != "" && onlyAge(ps) {
			guard.inputs = append(guard.inputs, *in)
			guard.digests[in.Name] = digest
		}
	}

	if action == ActionDryRun {
		for _, p := range problems {
			logger.WarnContext(ctx, "EXTERNAL INPUT IS NOT CURRENT", slog.String("problem", p.text))
		}

		if len(problems) > 0 && opts.ExpectNoChanges {
			return nil, inputsError(problems, false)
		}

		return nil, nil
	}

	if opts.AllowStaleInputs {
		var rest []inputProblem

		for _, p := range problems {
			if p.ageOnly {
				logger.WarnContext(ctx, "PROCEEDING ON AN EXTERNAL INPUT PAST ITS MAX AGE (--allow-stale-inputs)",
					slog.String("problem", p.text))

				continue
			}

			rest = append(rest, p)
		}

		problems = rest
	}

	if len(problems) > 0 {
		return nil, inputsError(problems, !opts.AllowStaleInputs)
	}

	return guard, nil
}

// onlyAge reports whether every problem is the one AllowStaleInputs may waive;
// true for no problems at all.
func onlyAge(ps []inputProblem) bool {
	for _, p := range ps {
		if !p.ageOnly {
			return false
		}
	}

	return true
}

// coveredInputs returns the declared inputs that feed a stack in the run. An
// input scoped to stacks needs the scope's name to be matched against, so a
// run without one is refused rather than guessed at.
func coveredInputs(cfg Config, steps []Step, action Action) ([]ExternalInput, error) {
	var covered []ExternalInput

	for i := range cfg.Inputs {
		in := &cfg.Inputs[i]

		if len(in.Stacks) > 0 && cfg.Scope == "" {
			return nil, errors.New("refusing to change infrastructure: an external input is scoped to stacks, " +
				"but the scope has no name to match them against (set Config.Scope)")
		}

		if in.covers(cfg.Scope, steps, action) {
			covered = append(covered, *in)
		}
	}

	return covered, nil
}

// inputsError is the refusal. offerFlag says AllowStaleInputs is available,
// which is worth naming only when something it can waive is among the problems.
func inputsError(problems []inputProblem, offerFlag bool) error {
	lines := make([]string, len(problems))
	hasAge := false

	for i, p := range problems {
		lines[i] = "  - " + p.text
		hasAge = hasAge || p.ageOnly
	}

	hint := "a committed copy is never read back; there is no override"
	if offerFlag && hasAge {
		hint = "a committed copy is never read back; only max_age can be waived (--allow-stale-inputs)"
	}

	return fmt.Errorf("refusing to change infrastructure: external inputs are not current:\n%s\n%s",
		strings.Join(lines, "\n"), hint)
}

// checkInput verifies one input: its local copy against the published digest,
// and the age of the publication against max_age. It returns the digest of the
// local copy and every reason the input is not current.
func checkInput(
	ctx context.Context,
	root string,
	in *ExternalInput,
	resolvers map[string]InputResolver,
) (string, []inputProblem) {
	who := fmt.Sprintf("%s (%s)", in.Name, in.Path)

	local, fileErr := hashInput(root, in.Path)

	var problems []inputProblem

	if fileErr != nil {
		problems = append(problems, inputProblem{text: fmt.Sprintf("%s: %v", who, fileErr)})
	}

	state, err := resolveInput(ctx, in, resolvers)
	if err != nil {
		return local, append(problems, inputProblem{text: fmt.Sprintf("%s: %v", who, err)})
	}

	if fileErr == nil && local != state.Digest {
		text := fmt.Sprintf("%s: digest %s is not the published %s", who, shortDigest(local), shortDigest(state.Digest))
		if state.Revision != "" {
			text += fmt.Sprintf(" (revision %s)", printable(state.Revision, 64))
		}

		problems = append(problems, inputProblem{text: text + "; refresh the file from its source"})
	}

	switch age := inputsNow().Sub(state.PublishedAt); {
	case state.PublishedAt.IsZero():
		problems = append(problems, inputProblem{text: who + ": the publisher reports no publication time"})
	case age < -inputClockSkew:
		problems = append(problems, inputProblem{text: who + ": published in the future; the clocks disagree"})
	case age > in.MaxAge:
		problems = append(problems, inputProblem{
			text:    fmt.Sprintf("%s: published %s ago, older than max_age %s", who, shortDuration(age), shortDuration(in.MaxAge)),
			ageOnly: true,
		})
	}

	return local, problems
}

// resolveInput looks up the published state with the resolver for the scheme
// of the input's source, and checks that what comes back is well-formed.
func resolveInput(ctx context.Context, in *ExternalInput, resolvers map[string]InputResolver) (InputState, error) {
	scheme := inputSchemeRE.FindStringSubmatch(in.Source)[1]

	r := resolvers[scheme]
	if r == nil {
		return InputState{}, fmt.Errorf("no resolver for the scheme %q, so its published state cannot be read", scheme)
	}

	ctx, cancel := context.WithTimeout(ctx, inputResolveTimeout)
	defer cancel()

	state, err := r.Resolve(ctx, in.Source)
	if err != nil {
		return InputState{}, fmt.Errorf("the published state could not be resolved: %s",
			printable(redactSource(err.Error(), in.Source), 200))
	}

	if !digestRE.MatchString(state.Digest) {
		return InputState{}, errors.New("the resolver returned a malformed digest (want sha256: and 64 lower-case hex digits)")
	}

	return state, nil
}

// redactSource removes the source, and the parts of it that carry a
// credential, from a message that may quote it.
func redactSource(msg, source string) string {
	secrets := []string{source}

	if u, err := url.Parse(source); err == nil {
		if u.User != nil {
			secrets = append(secrets, u.User.String(), u.User.Username())

			if pw, ok := u.User.Password(); ok {
				secrets = append(secrets, pw)
			}
		}

		secrets = append(secrets, u.RawQuery)
	}

	// Longest first, so a part is not removed from inside the whole.
	sort.Slice(secrets, func(i, j int) bool { return len(secrets[i]) > len(secrets[j]) })

	for i, s := range secrets {
		if s == "" {
			continue
		}

		repl := "<source>"
		if i > 0 {
			repl = "<redacted>"
		}

		msg = strings.ReplaceAll(msg, s, repl)
	}

	return msg
}

// hashInput returns "sha256:" and the hex digest of the raw bytes of the file
// at rel under root. The file must be a regular file, and must stay inside
// root once symbolic links are resolved. Its path is the caller's to print:
// ValidateInputs has already refused a credential-shaped one.
func hashInput(root, rel string) (string, error) {
	realRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		return "", errors.New("the checkout root cannot be resolved")
	}

	resolved, err := filepath.EvalSymlinks(filepath.Join(root, filepath.FromSlash(rel)))
	if errors.Is(err, fs.ErrNotExist) {
		return "", errors.New("the file does not exist")
	}

	if err != nil {
		return "", errors.New("the file cannot be resolved")
	}

	if within, err := filepath.Rel(realRoot, resolved); err != nil || !filepath.IsLocal(within) {
		return "", errors.New("the file resolves outside the checkout")
	}

	f, err := os.Open(resolved)
	if err != nil {
		return "", errors.New("the file cannot be opened")
	}

	defer func() { _ = f.Close() }()

	info, err := f.Stat()
	if err != nil || !info.Mode().IsRegular() {
		return "", errors.New("the path is not a regular file")
	}

	h := sha256.New()

	n, err := io.Copy(h, io.LimitReader(f, maxInputBytes+1))
	if err != nil {
		return "", errors.New("the file cannot be read")
	}

	if n > maxInputBytes {
		return "", fmt.Errorf("the file is larger than %d MiB", maxInputBytes>>20)
	}

	return digestPrefix + hex.EncodeToString(h.Sum(nil)), nil
}

// recheck hashes every verified input again and refuses if one changed since
// the gate: a file edited between steps would otherwise be applied unchecked.
// A nil guard checks nothing.
func (g *inputGuard) recheck(root string) error {
	if g == nil {
		return nil
	}

	for i := range g.inputs {
		in := &g.inputs[i]

		got, err := hashInput(root, in.Path)
		if err != nil || got != g.digests[in.Name] {
			return fmt.Errorf("refusing to change infrastructure: external input %s (%s) changed during the run; "+
				"run again so it is verified against its source", in.Name, in.Path)
		}
	}

	return nil
}

// shortDigest abbreviates a digest for a message.
func shortDigest(d string) string {
	if n := len(digestPrefix) + 12; len(d) > n {
		return d[:n] + "…"
	}

	return d
}

// shortDuration rounds d to the minute and drops zero units: 49h, 1h30m, 5m.
func shortDuration(d time.Duration) string {
	m := int(d.Round(time.Minute) / time.Minute)

	switch h, rest := m/60, m%60; {
	case h > 0 && rest > 0:
		return fmt.Sprintf("%dh%dm", h, rest)
	case h > 0:
		return fmt.Sprintf("%dh", h)
	default:
		return fmt.Sprintf("%dm", rest)
	}
}

// printable keeps the first limit printable runes of s on one line, so a value
// a publisher controls cannot forge a log line.
func printable(s string, limit int) string {
	var b strings.Builder

	n := 0

	for _, r := range s {
		if n == limit {
			b.WriteString("…")

			break
		}

		if !unicode.IsPrint(r) {
			r = ' '
		}

		b.WriteRune(r)

		n++
	}

	return b.String()
}
