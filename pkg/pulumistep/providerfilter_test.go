package pulumistep

import (
	"bytes"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// writeAndFlush feeds input through w in a single Write call, then Flushes it,
// returning what landed on the destination buffer.
func writeAndFlush(t *testing.T, input string) (string, int) {
	t.Helper()

	var dst bytes.Buffer

	w := newProviderFilterWriter(&dst)

	_, err := w.Write([]byte(input))
	require.NoError(t, err)
	require.NoError(t, w.Flush())

	return dst.String(), w.Suppressed()
}

func TestProviderFilterWriter_SuppressesVersionOnlyUpdates(t *testing.T) {
	input := strings.Join([]string{
		"Previewing update (main):",
		"  pulumi:pulumi:Stack: (same)",
		"    [urn=urn:pulumi:main::example-root::pulumi:pulumi:Stack::example-root-main]",
		"    ~ pulumi:providers:aws: (update)",
		"        [id=14f67bb3-4ef7-4ce8-8301-57645bbfac51]",
		"        [urn=urn:pulumi:main::example-root::pulumi:providers:aws::backup-eu-example-1]",
		"      ~ version: \"7.35.0\" => \"7.39.0\"",
		"    ~ pulumi:providers:aws: (update)",
		"        [id=c4856ee7-e2d7-479a-9b61-fc07eaab742b]",
		"        [urn=urn:pulumi:main::example-root::pulumi:providers:aws::secondary-eu-example-1]",
		"      ~ version: \"7.35.0\" => \"7.39.0\"",
		"Resources:",
		"    ~ 2 to update",
		"    29 unchanged",
		"",
	}, "\n")

	out, suppressed := writeAndFlush(t, input)

	assert.Equal(t, 2, suppressed)
	assert.NotContains(t, out, "pulumi:providers:aws")
	assert.NotContains(t, out, "7.35.0")
	// Unrelated lines pass through untouched.
	assert.Contains(t, out, "Previewing update (main):")
	assert.Contains(t, out, "pulumi:pulumi:Stack: (same)")
	assert.Contains(t, out, "Resources:")
	assert.Contains(t, out, "    ~ 2 to update")
	assert.Contains(t, out, "    29 unchanged")
}

func TestProviderFilterWriter_InterleavedProgressLineDoesNotBreakFiltering(t *testing.T) {
	// Pulumi's automation API interleaves standalone "@ previewing..." status
	// lines between resource blocks; make sure a block on either side of one
	// is still classified correctly.
	input := strings.Join([]string{
		"    ~ pulumi:providers:aws: (update)",
		"        [urn=urn:pulumi:main::example-root::pulumi:providers:aws::a]",
		"      ~ version: \"7.35.0\" => \"7.39.0\"",
		"@ previewing update....",
		"    ~ pulumi:providers:aws: (update)",
		"        [urn=urn:pulumi:main::example-root::pulumi:providers:aws::b]",
		"      ~ version: \"7.35.0\" => \"7.39.0\"",
		"Resources:",
		"",
	}, "\n")

	out, suppressed := writeAndFlush(t, input)

	assert.Equal(t, 2, suppressed)
	assert.Contains(t, out, "@ previewing update....")
	assert.NotContains(t, out, "pulumi:providers:aws")
}

func TestProviderFilterWriter_KeepsProviderUpdateWithNonVersionChange(t *testing.T) {
	// A provider update that touches more than just the version must never
	// be suppressed — it's real drift, not noise.
	input := strings.Join([]string{
		"    ~ pulumi:providers:aws: (update)",
		"        [urn=urn:pulumi:main::example-root::pulumi:providers:aws::a]",
		"      ~ version: \"7.35.0\" => \"7.39.0\"",
		"      ~ region: \"eu-example-1\" => \"eu-example-2\"",
		"Resources:",
		"",
	}, "\n")

	out, suppressed := writeAndFlush(t, input)

	assert.Equal(t, 0, suppressed)
	assert.Contains(t, out, "pulumi:providers:aws: (update)")
	assert.Contains(t, out, "~ region:")
}

func TestProviderFilterWriter_KeepsOtherResourceDiffsUntouched(t *testing.T) {
	// Non-provider resource diffs must always pass through unchanged,
	// regardless of how many properties they touch.
	input := strings.Join([]string{
		"    ~ aws:s3/bucket:Bucket: (update)",
		"        [urn=urn:pulumi:main::example-root::aws:s3/bucket:Bucket::my-bucket]",
		"      ~ tags: {",
		"          + Owner: \"platform\"",
		"        }",
		"Resources:",
		"",
	}, "\n")

	out, suppressed := writeAndFlush(t, input)

	assert.Equal(t, 0, suppressed)
	assert.Equal(t, input, out)
}

func TestProviderFilterWriter_TrailingBlockAtEOFIsFlushed(t *testing.T) {
	// A version-only provider block that is the very last thing written
	// (stream ends before any terminating line) must still be resolved by
	// Flush, not silently dropped or left unflushed.
	input := strings.Join([]string{
		"    ~ pulumi:providers:aws: (update)",
		"        [urn=urn:pulumi:main::example-root::pulumi:providers:aws::a]",
		"      ~ version: \"7.35.0\" => \"7.39.0\"",
		"",
	}, "\n")

	out, suppressed := writeAndFlush(t, input)

	assert.Equal(t, 1, suppressed)
	assert.Empty(t, out)
}

func TestProviderFilterWriter_HandlesFragmentedWrites(t *testing.T) {
	// Automation API may call Write with arbitrary chunk boundaries that
	// split lines mid-way; the filter must reassemble them correctly.
	full := "    ~ pulumi:providers:aws: (update)\n" +
		"        [urn=urn:pulumi:main::example-root::pulumi:providers:aws::a]\n" +
		"      ~ version: \"7.35.0\" => \"7.39.0\"\n" +
		"Resources:\n"

	var dst bytes.Buffer

	w := newProviderFilterWriter(&dst)

	for i := 0; i < len(full); i += 7 {
		end := i + 7
		if end > len(full) {
			end = len(full)
		}

		_, err := w.Write([]byte(full[i:end]))
		require.NoError(t, err)
	}

	require.NoError(t, w.Flush())

	assert.Equal(t, 1, w.Suppressed())
	assert.Equal(t, "Resources:\n", dst.String())
}
