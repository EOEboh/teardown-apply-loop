package main

import (
	"fmt"
	"path/filepath"
	"strings"
)

// ---------------------------------------------------------------------------
// The two prompts are the heart of the demo. Read them before the code.
//
// STAGE 1 (sketch) asks a model for a LAZY edit: only the regions that change,
// with every untouched span collapsed into one marker line. That is cheap,
// because output tokens dominate the cost of an edit, and a 400-line file
// rewritten in full is 400 lines of output for a 3-line change.
//
// STAGE 2 (apply) turns the lazy sketch back into a whole file. Either a second
// cheap model call does it (--apply=model) or pure Go does it (--apply=merge).
// ---------------------------------------------------------------------------

// KeepMarkerText is the sentinel phrase the sketch uses for "unchanged span".
// Cursor and friends use almost exactly this string; the deterministic merge in
// merge.go keys off it.
const KeepMarkerText = "... keep existing code ..."

// sketchSystemPrompt tells the model to emit a lazy edit.
//
// The single most important rule is #3: the model must copy a couple of
// UNCHANGED lines around each edited region. Those lines are the anchors the
// deterministic merge uses to find its way back into the original file. A model
// apply can usually cope without them; the Go merge cannot.
func sketchSystemPrompt(commentToken string) string {
	marker := commentToken + " " + KeepMarkerText
	return fmt.Sprintf(`You are the SKETCH stage of a two-stage code editing pipeline.

You are given a file and a natural-language edit instruction. You do NOT rewrite
the file. You emit a lazy, partial edit that a later APPLY stage stitches back
into the original.

Rules:
1. Output ONLY the regions of the file that change.
2. Collapse every unchanged span into exactly one marker line:
     %s
   Use that marker verbatim, on its own line, as many times as needed.
3. Around each changed region, copy 1-3 UNCHANGED lines directly above it and
   1-3 UNCHANGED lines directly below it, character for character, including
   indentation. The apply stage uses those lines as anchors to locate the edit.
   Without them the edit cannot be placed.
4. Never invent or reformat code you were not asked to change. Preserve the
   file's existing indentation style (tabs vs spaces) exactly.
5. Output raw file content only: no markdown fences, no commentary, no
   explanation before or after.

Example shape of a good answer:

%s
func Add(a, b int) int {
	return a + b
}

func Sub(a, b int) int {
	return a - b
}
%s`, marker, marker, marker)
}

// sketchUserPrompt carries the actual work item, plus any error from a previous
// failed attempt (that feedback is what makes this a loop rather than a call).
func sketchUserPrompt(path, original, instruction, feedback string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "FILE: %s\n\nORIGINAL FILE CONTENT:\n%s\n\n", path, original)
	fmt.Fprintf(&b, "EDIT INSTRUCTION:\n%s\n", instruction)
	if feedback != "" {
		fmt.Fprintf(&b, `
YOUR PREVIOUS ATTEMPT FAILED. The error was:
%s

Produce a corrected lazy edit. Pay extra attention to copying unchanged anchor
lines exactly, and to leaving the rest of the file collapsed behind markers.
`, feedback)
	}
	return b.String()
}

// applySystemPrompt is the "cheap model does the typing" stage. Its job is
// deliberately narrow: expand markers, apply the sketched changes, touch
// nothing else. No reasoning about the task, no improvements, no opinions.
func applySystemPrompt(commentToken string) string {
	return fmt.Sprintf(`You are the APPLY stage of a two-stage code editing pipeline.

You are given an ORIGINAL FILE and a LAZY SKETCH of an edit to it. The sketch
contains only changed regions; every unchanged span was collapsed into a marker
line that looks like:
    %s %s

Your only job is to produce the full file with the sketch's changes applied.

Rules:
1. Expand every marker back into the exact original lines it stands for.
2. Apply every change the sketch makes, exactly as the sketch makes it.
3. Change NOTHING the sketch did not change: no reformatting, no renaming, no
   added comments, no "improvements".
4. Output the complete resulting file, raw. No markdown fences, no commentary.`,
		commentToken, KeepMarkerText)
}

func applyUserPrompt(path, original, sketch string) string {
	return fmt.Sprintf("FILE: %s\n\nORIGINAL FILE:\n%s\n\nLAZY SKETCH:\n%s\n\nFULL FILE WITH THE EDIT APPLIED:\n", path, original, sketch)
}

// commentTokenFor picks the line-comment syntax for the marker so the sketch
// stays syntactically plausible for the language being edited.
func commentTokenFor(path string) string {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".py", ".rb", ".sh", ".bash", ".zsh", ".yaml", ".yml", ".toml", ".tf", ".pl", ".r":
		return "#"
	case ".sql", ".lua", ".hs", ".elm":
		return "--"
	case ".lisp", ".clj", ".el":
		return ";;"
	default:
		// .go, .js, .ts, .tsx, .java, .c, .cpp, .rs, .swift, .kt, .php, .cs ...
		return "//"
	}
}
