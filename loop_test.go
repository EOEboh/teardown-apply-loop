package main

import (
	"context"
	"io"
	"strings"
	"testing"
)

// fakeModel replays canned answers in order and records the prompts it saw.
// Model calls sit behind the Model interface exactly so the loop can be tested
// without an API key or a network.
type fakeModel struct {
	replies []string
	calls   []string // the user prompt of each call
}

func (f *fakeModel) Complete(_ context.Context, _, user string) (string, error) {
	f.calls = append(f.calls, user)
	if len(f.replies) == 0 {
		return "", io.EOF
	}
	r := f.replies[0]
	f.replies = f.replies[1:]
	return r, nil
}

// The loop in miniature: the first sketch cannot be merged, the failure is fed
// back to the model, and the second sketch succeeds. A .txt target keeps the
// test hermetic by skipping the Go checkers.
func TestPipelineRetriesAfterMergeFailure(t *testing.T) {
	orig := lines("alpha", "beta", "gamma")

	unanchorable := lines(
		"// ... keep existing code ...",
		"delta",
		"// ... keep existing code ...",
	)
	// A sketch the merge can place: it opens on an unchanged line ("alpha")
	// and closes on an unchanged line ("gamma"), so the block in between has
	// anchors on both sides.
	good := lines(
		"// ... keep existing code ...",
		"alpha",
		"beta改",
		"gamma",
	)

	m := &fakeModel{replies: []string{unanchorable, good}}
	p := &Pipeline{Sketcher: m, Applier: m, Mode: "merge", Cfg: DefaultConfig(), Out: io.Discard}

	got, err := p.Run(context.Background(), "notes.txt", orig, "tweak beta", 2)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if want := lines("alpha", "beta改", "gamma"); got != want {
		t.Errorf("got:\n%q\nwant:\n%q", got, want)
	}
	if p.Attempts != 2 {
		t.Errorf("expected 2 attempts, got %d", p.Attempts)
	}
	if len(m.calls) != 2 {
		t.Fatalf("expected 2 model calls, got %d", len(m.calls))
	}
	if !strings.Contains(m.calls[1], "PREVIOUS ATTEMPT FAILED") ||
		!strings.Contains(m.calls[1], "could not anchor") {
		t.Errorf("the merge error should be fed back into the retry prompt, got:\n%s", m.calls[1])
	}
}

// In model mode the second call gets the original and the sketch, and its
// output is the file. Nothing parses the markers.
func TestPipelineModelApplyPassesSketchToApplier(t *testing.T) {
	orig := lines("alpha", "beta")
	sketch := lines("// ... keep existing code ...", "beta!")
	full := lines("alpha", "beta!")

	m := &fakeModel{replies: []string{sketch, full}}
	p := &Pipeline{Sketcher: m, Applier: m, Mode: "model", Cfg: DefaultConfig(), Out: io.Discard}

	got, err := p.Run(context.Background(), "notes.txt", orig, "add an exclamation mark", 2)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if got != full {
		t.Errorf("got %q, want %q", got, full)
	}
	if !strings.Contains(m.calls[1], "LAZY SKETCH") || !strings.Contains(m.calls[1], "beta!") {
		t.Errorf("the apply call should carry the sketch, got:\n%s", m.calls[1])
	}
}

func TestStripFences(t *testing.T) {
	in := "```go\npackage main\n```\n"
	if got, want := stripFences(in), "package main"; got != want {
		t.Errorf("got %q, want %q", got, want)
	}
	if got, want := stripFences("package main\n"), "package main"; got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}
