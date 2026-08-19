package main

import (
	"strings"
	"testing"
)

// Fixtures are built line by line on purpose: the deterministic merge compares
// leading whitespace exactly, so a test written with a raw string literal would
// be one stray space away from testing something else.
func lines(l ...string) string { return strings.Join(l, "\n") + "\n" }

var original = lines(
	"package demo",
	"",
	`import "fmt"`,
	"",
	"func Greet(name string) string {",
	"\treturn fmt.Sprintf(\"Hello, %s!\", name)",
	"}",
	"",
	"func Add(a, b int) int {",
	"\treturn a + b",
	"}",
)

// The happy path: the sketch changes one function body and leaves anchors
// (the func signature and the closing brace) around it.
func TestMergeSketchReplacesAnchoredBlock(t *testing.T) {
	sketch := lines(
		"// ... keep existing code ...",
		"func Greet(name string) string {",
		"\treturn fmt.Sprintf(\"Hi, %s!\", name)",
		"}",
		"// ... keep existing code ...",
	)

	got, err := MergeSketch(original, sketch)
	if err != nil {
		t.Fatalf("MergeSketch: %v", err)
	}

	want := lines(
		"package demo",
		"",
		`import "fmt"`,
		"",
		"func Greet(name string) string {",
		"\treturn fmt.Sprintf(\"Hi, %s!\", name)",
		"}",
		"",
		"func Add(a, b int) int {",
		"\treturn a + b",
		"}",
	)
	if got != want {
		t.Errorf("merged output mismatch\n--- got ---\n%s\n--- want ---\n%s", got, want)
	}
}

// BRITTLE #2 in merge.go. The sketch is perfectly sensible to a human: bump the
// return value of A. But its first line, "\treturn 1", occurs twice in the
// file, so anchor matching has no way to choose. The merge refuses instead of
// guessing, and the loop feeds the refusal back to the model.
//
// A model-based apply gets this right without trying, because it reads the
// whole file and understands that the sketch's context is function A.
func TestMergeSketchAmbiguousAnchorFails(t *testing.T) {
	dup := lines(
		"package demo",
		"",
		"func A() int {",
		"\treturn 1",
		"}",
		"",
		"func B() int {",
		"\treturn 1",
		"}",
	)
	sketch := lines(
		"// ... keep existing code ...",
		"\treturn 1",
		"\t// bumped",
		"// ... keep existing code ...",
	)

	_, err := MergeSketch(dup, sketch)
	if err == nil {
		t.Fatal("expected an error: the anchor line occurs twice")
	}
	if !strings.Contains(err.Error(), "ambiguous anchor") {
		t.Errorf("expected an ambiguity error, got: %v", err)
	}
}

// Also brittle: a block of entirely new code, sandwiched between two keep
// markers, has no anchor at all. There is no honest way to know where it goes.
func TestMergeSketchUnanchorableMiddleBlockFails(t *testing.T) {
	sketch := lines(
		"// ... keep existing code ...",
		"func Brand() string {",
		"\treturn \"acme\"",
		"}",
		"// ... keep existing code ...",
	)

	_, err := MergeSketch(original, sketch)
	if err == nil {
		t.Fatal("expected an error: the new block has no anchor in the original")
	}
	if !strings.Contains(err.Error(), "could not anchor") {
		t.Errorf("expected an anchoring error, got: %v", err)
	}
}

// BRITTLE #3: the same unanchorable block, but last in the sketch, is assumed
// to belong at the end of the file. That guess is right often enough to be
// useful and wrong often enough to be worth knowing about.
func TestMergeSketchAppendsUnanchorableFinalBlock(t *testing.T) {
	sketch := lines(
		"// ... keep existing code ...",
		"func Farewell(name string) string {",
		"\treturn \"Bye, \" + name",
		"}",
	)

	got, err := MergeSketch(original, sketch)
	if err != nil {
		t.Fatalf("MergeSketch: %v", err)
	}
	if !strings.HasPrefix(got, original) {
		t.Errorf("the original file should be preserved verbatim, got:\n%s", got)
	}
	if !strings.HasSuffix(got, lines("func Farewell(name string) string {", "\treturn \"Bye, \" + name", "}")) {
		t.Errorf("the new function should be appended at the end, got:\n%s", got)
	}
}

func TestIsKeepMarker(t *testing.T) {
	yes := []string{
		"// ... keep existing code ...",
		"    // ... keep existing code ...",
		"# ... keep existing code ...",
		"/* ... rest of file unchanged ... */",
		"<!-- ... existing markup ... -->",
		"// ... unchanged ...",
	}
	no := []string{
		"// ... TODO: rename this ...",
		`	fmt.Println("... keep existing code ...")`,
		"func Greet(name string) string {",
		"",
	}
	for _, l := range yes {
		if !isKeepMarker(l) {
			t.Errorf("expected a keep marker: %q", l)
		}
	}
	for _, l := range no {
		if isKeepMarker(l) {
			t.Errorf("did not expect a keep marker: %q", l)
		}
	}
}
