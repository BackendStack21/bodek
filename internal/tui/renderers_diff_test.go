package tui

import (
	"strings"
	"testing"
)

// Fenced ```diff blocks embed in tool output with prose around them (edit
// tools, agent explanations). The whole-result diff path mis-styles that
// mix — prose tints as diff lines and fence markers leak through — and a
// fence without a @@ hunk (common in model-written patches) misses the diff
// renderer entirely. The fence is authoritative: when present, only its
// content tints.

func TestHasFencedDiff(t *testing.T) {
	yes := []string{
		"Here's the patch:\n```diff\n+added\n```",
		"```diff\n--- a/f\n+++ b/f\n@@ -1 +1 @@\n+x\n```",
		"text\n```diff \n+x\n```\nmore", // trailing space on the fence opener
	}
	for _, s := range yes {
		if !hasFencedDiff(s) {
			t.Errorf("hasFencedDiff(%q) = false, want true", s)
		}
	}
	no := []string{
		"+just a plus line",
		"```go\nif x {\n\t+1\n}\n```", // a non-diff fence never counts
		"```diff\nnever closed",
		"plain prose",
	}
	for _, s := range no {
		if hasFencedDiff(s) {
			t.Errorf("hasFencedDiff(%q) = true, want false", s)
		}
	}
}

func TestFencedDiffBlocksExtracts(t *testing.T) {
	s := "before\n```diff\n+one\n-two\n```\nbetween\n```diff\n+three\n```"
	blocks := fencedDiffBlocks(s)
	if len(blocks) != 2 {
		t.Fatalf("got %d blocks (%v), want 2", len(blocks), blocks)
	}
	if blocks[0] != "+one\n-two" || blocks[1] != "+three" {
		t.Errorf("blocks = %q, want the unwrapped fence contents", blocks)
	}
}

func TestRenderMixedDiff(t *testing.T) {
	th := newTheme()
	s := "Here's the patch:\n```diff\n@@ -1 +1 @@\n-old line\n+new line\n```\nDone."
	out := renderMixedDiff(s, 100, th)
	joined := strings.Join(out, "\n")

	if strings.Contains(joined, "```") {
		t.Errorf("fence markers leaked into the render:\n%s", joined)
	}
	for _, want := range []string{"Here's the patch:", "@@ -1 +1 @@", "+new line", "-old line", "Done."} {
		if !strings.Contains(joined, want) {
			t.Errorf("render missing %q in:\n%s", want, joined)
		}
	}
	// The prose must stay verbatim-styled, not tinted as diff lines: the
	// diff tint colors carry the +/- semantics, prose keeps the plain style.
	for i, ln := range out {
		if strings.Contains(ln, "Here's the patch:") && ln != th.stepRes.Render(truncate("Here's the patch:", detailWidth(100))) {
			t.Errorf("prose line %d picked up diff styling: %q", i, ln)
		}
	}
}

func TestRenderMixedDiffFenceWithoutHunks(t *testing.T) {
	// No @@ anywhere: diffLooksLike is false, but the fence is authoritative.
	th := newTheme()
	out := renderMixedDiff("```diff\n+added\n-removed\n```", 100, th)
	joined := strings.Join(out, "\n")
	for _, want := range []string{"+added", "-removed"} {
		if !strings.Contains(joined, want) {
			t.Errorf("render missing %q in:\n%s", want, joined)
		}
	}
	if !strings.Contains(joined, th.diffAdd.Render("+added")) {
		t.Error("fenced + line did not get the add tint")
	}
	if !strings.Contains(joined, th.diffDel.Render("-removed")) {
		t.Error("fenced - line did not get the del tint")
	}
}

func TestStepDetailPrefersFences(t *testing.T) {
	th := newTheme()
	s := "Patch:\n```diff\n+new line\n```\nEnd."
	out := stepDetail("edit", s, 100, th)
	joined := strings.Join(out, "\n")
	if !strings.Contains(joined, "+new line") || strings.Contains(joined, "```") {
		t.Errorf("fenced block not rendered as a diff:\n%s", joined)
	}
	// The whole-result diff path keeps working (regression guard).
	whole := stepDetail("edit", "@@ -1 +1 @@\n-old\n+new", 100, th)
	if !strings.Contains(strings.Join(whole, "\n"), "+new") {
		t.Errorf("whole-result diff regressed:\n%s", strings.Join(whole, "\n"))
	}
}

func TestStepHeadSuffixCountsFences(t *testing.T) {
	th := newTheme()
	suffix := stepHeadSuffix("edit", "", "```diff\n+a\n-b\n-c\n```", th)
	if !strings.Contains(suffix, "+1") || !strings.Contains(suffix, "−2") {
		t.Errorf("fence diffstat chip = %q, want +1 −2", suffix)
	}
}
