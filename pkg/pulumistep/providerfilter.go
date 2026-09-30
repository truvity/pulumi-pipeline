package pulumistep

import (
	"bytes"
	"fmt"
	"io"
	"regexp"
	"strings"
)

var (
	// providerUpdateHeaderRe matches the header line Pulumi's non-interactive
	// diff renderer prints for a provider resource update, e.g.:
	//   ~ pulumi:providers:aws: (update)
	providerUpdateHeaderRe = regexp.MustCompile(`^~ pulumi:providers:\S+: \(update\)$`)

	// versionOnlyDiffRe matches a property-diff line that changes only the
	// provider plugin version, e.g.:
	//   ~ version: "7.35.0" => "7.39.0"
	versionOnlyDiffRe = regexp.MustCompile(`^~ version: ".*" => ".*"$`)

	// metadataLineRe matches the [id=...] / [urn=...] lines Pulumi prints
	// under a resource header; these don't count as a property change.
	metadataLineRe = regexp.MustCompile(`^\[(id|urn)=.*\]$`)
)

// providerFilterWriter wraps an io.Writer used as a Pulumi Automation API
// ProgressStreams sink and suppresses preview diff blocks for
// pulumi:providers:* resources whose only change is the plugin version
// (e.g. aws 7.35.0 -> 7.39.0). Everything else — including provider blocks
// that touch more than just version, and every other resource's diff — is
// written through unchanged.
//
// This is display-only filtering of the text Pulumi streams back: it has no
// effect on the Preview call itself, so the actual diffing is never skipped.
//
// A block is recognized purely by indentation: any line more indented than
// the header line that started it is considered part of the block, which
// matches how Pulumi's non-interactive diff renderer nests [id=]/[urn=]
// metadata and property-diff lines under a resource header.
type (
	providerFilterWriter struct {
		dst io.Writer

		lineBuf bytes.Buffer // accumulates a partial line across Write calls

		inBlock      bool
		headerIndent int
		blockLines   []string
		versionLines int
		disqualified bool

		suppressed int
	}
)

// newProviderFilterWriter returns a providerFilterWriter that writes
// filtered output to dst.
func newProviderFilterWriter(dst io.Writer) *providerFilterWriter {
	return &providerFilterWriter{dst: dst}
}

// Write implements io.Writer.
func (w *providerFilterWriter) Write(p []byte) (int, error) {
	n := len(p)

	w.lineBuf.Write(p)

	for {
		buf := w.lineBuf.Bytes()

		idx := bytes.IndexByte(buf, '\n')
		if idx < 0 {
			break
		}

		line := string(buf[:idx])
		w.lineBuf.Next(idx + 1)

		if err := w.handleLine(line); err != nil {
			return n, err
		}
	}

	return n, nil
}

// handleLine processes a single complete line (without its trailing newline).
func (w *providerFilterWriter) handleLine(line string) error {
	trimmed := strings.TrimSpace(line)
	indent := leadingSpaces(line)

	if w.inBlock && trimmed != "" && indent > w.headerIndent {
		w.blockLines = append(w.blockLines, line)

		switch {
		case metadataLineRe.MatchString(trimmed):
			// Metadata line, doesn't affect version-only classification.
		case versionOnlyDiffRe.MatchString(trimmed):
			w.versionLines++
		default:
			w.disqualified = true
		}

		return nil
	}

	if w.inBlock {
		if err := w.flushBlock(); err != nil {
			return err
		}
	}

	if trimmed != "" && providerUpdateHeaderRe.MatchString(trimmed) {
		w.inBlock = true
		w.headerIndent = indent
		w.blockLines = []string{line}
		w.versionLines = 0
		w.disqualified = false

		return nil
	}

	_, err := fmt.Fprintln(w.dst, line)

	return err
}

// flushBlock finalizes the buffered candidate block: suppresses it if it's a
// pure version-only provider update, otherwise writes it through unchanged.
func (w *providerFilterWriter) flushBlock() error {
	w.inBlock = false

	if !w.disqualified && w.versionLines == 1 {
		w.suppressed++
		w.blockLines = nil

		return nil
	}

	for _, l := range w.blockLines {
		if _, err := fmt.Fprintln(w.dst, l); err != nil {
			return err
		}
	}

	w.blockLines = nil

	return nil
}

// Flush finalizes the writer at end-of-stream: a block still open when the
// underlying Preview call returns is resolved using the same rules as any
// other block, and a trailing partial line (no final newline) is written
// through as-is. Callers must call Flush after the Preview call completes.
func (w *providerFilterWriter) Flush() error {
	if w.inBlock {
		if err := w.flushBlock(); err != nil {
			return err
		}
	}

	if w.lineBuf.Len() > 0 {
		rest := w.lineBuf.String()
		w.lineBuf.Reset()

		if _, err := io.WriteString(w.dst, rest); err != nil {
			return err
		}
	}

	return nil
}

// Suppressed returns the number of provider-version-only update blocks
// suppressed since the writer was created.
func (w *providerFilterWriter) Suppressed() int {
	return w.suppressed
}

// leadingSpaces returns the number of leading ' ' characters in s.
func leadingSpaces(s string) int {
	n := 0

	for _, r := range s {
		if r != ' ' {
			break
		}

		n++
	}

	return n
}
