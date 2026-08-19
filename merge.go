package main

import (
	"fmt"
	"regexp"
	"strings"
)

// ---------------------------------------------------------------------------
// DETERMINISTIC APPLY (--apply=merge)
//
// This file is the "no second model" version of the apply step. It parses the
// sketch's marker convention and splices the changed blocks back into the
// original file using ANCHOR MATCHING: the first and last line of each changed
// block are looked up in the original to decide which span the block replaces.
//
// It is deliberately ~120 lines. It works on well-behaved sketches and falls
// over on plenty of real ones. Every place it can be wrong is marked BRITTLE,
// and the README collects them. Reading these is the point of the exercise:
// this is the code a model-based apply replaces.
// ---------------------------------------------------------------------------

// keepMarkerRe matches the "unchanged span" sentinel in whatever comment syntax
// the model reached for: "// ... keep existing code ...", "# ... keep existing
// code ...", "/* ... rest of file unchanged ... */", "<!-- ... -->" and so on.
//
// BRITTLE #1: this is a regexp over prose. A model that writes
// "// (unchanged)" or "// snip" produces a line this does not recognise, and
// that line is then spliced into the output as if it were real code.
var keepMarkerRe = regexp.MustCompile(`(?i)^\s*(?://+|#+|--+|;+|/\*+|<!--|\*)?\s*\.\.\.[^\n]*?(keep|unchang|existing|rest of|remainder|omitted)[^\n]*?\.\.\.\s*(?:\*/|-->)?\s*$`)

func isKeepMarker(line string) bool { return keepMarkerRe.MatchString(line) }

// segment is one piece of a parsed sketch: either a "keep" marker standing in
// for an unknown number of original lines, or a literal block of new content.
type segment struct {
	keep  bool
	lines []string
}

// parseSketch splits a sketch into alternating keep-markers and literal blocks.
// Consecutive markers collapse into one; blank lines at the edges of a literal
// block are dropped so they never end up being used as anchors.
func parseSketch(sketch string) []segment {
	var segs []segment
	var cur []string
	flush := func() {
		block := trimBlankEdges(cur)
		if len(block) > 0 {
			segs = append(segs, segment{lines: block})
		}
		cur = nil
	}
	for _, line := range splitLines(sketch) {
		if isKeepMarker(line) {
			flush()
			if n := len(segs); n == 0 || !segs[n-1].keep {
				segs = append(segs, segment{keep: true})
			}
			continue
		}
		cur = append(cur, line)
	}
	flush()
	return segs
}

// MergeSketch splices a lazy sketch into the original file and returns the full
// file. It returns an error rather than guessing whenever a block cannot be
// located; a wrong guess would silently corrupt the file, and in the loop an
// error is cheap (it is fed back to the sketch model as feedback).
func MergeSketch(original, sketch string) (string, error) {
	orig := splitLines(original)
	segs := parseSketch(sketch)
	if len(segs) == 0 {
		return "", fmt.Errorf("sketch is empty")
	}

	var out []string
	pos := 0             // cursor into orig: everything before it is already emitted
	pendingKeep := false // the previous segment was a "keep existing code" marker

	for i, seg := range segs {
		if seg.keep {
			// How much to keep is not known yet: the NEXT block's anchor
			// decides. If no block follows, it means "the rest of the file".
			pendingKeep = true
			continue
		}

		head := seg.lines[0]
		tail := seg.lines[len(seg.lines)-1]

		// Anchor the top of the block. The head must occur exactly once in the
		// not-yet-consumed part of the original.
		//
		// BRITTLE #2: uniqueness is required, so a block starting with a line
		// as common as "}" or "return nil" cannot be anchored at all.
		headIdx, err := uniqueIndexFrom(orig, pos, head)
		if err != nil {
			// The block's first line is not (uniquely) in the original, so this
			// block is new content and we have to decide where it goes.
			if !pendingKeep {
				// No marker before it: the sketch says it belongs right here.
				out = append(out, seg.lines...)
				continue
			}
			if i == len(segs)-1 {
				// BRITTLE #3: last block, preceded by "keep existing code",
				// and unanchorable. We assume the model meant "append to the
				// end of the file". That is a guess. If it actually meant
				// "insert this new function in the middle", the code still
				// compiles and the merge still reports success, and the result
				// is simply in the wrong place.
				out = append(out, orig[pos:]...)
				out = append(out, seg.lines...)
				pos = len(orig)
				pendingKeep = false
				continue
			}
			return "", fmt.Errorf("block %d: %w\n  first line: %q", blockNum(segs, i), err, head)
		}

		if pendingKeep {
			// The marker stood for orig[pos:headIdx].
			out = append(out, orig[pos:headIdx]...)
		}
		// BRITTLE #4: if there was NO marker and headIdx > pos, the lines in
		// between are dropped. That is the convention's own logic ("no marker
		// means nothing is kept"), but it means a forgotten marker deletes code
		// silently rather than erroring.

		// Anchor the bottom of the block: the FIRST occurrence of the last line
		// at or after the head. Uniqueness is deliberately not required here,
		// because closing lines like "}" are never unique.
		//
		// BRITTLE #5: first-match wins. If the block's last line also appears
		// earlier than the model intended (a nested "}", a repeated "})"), the
		// merge ends the replaced span too early and duplicates the tail of the
		// original region.
		tailIdx := indexFrom(orig, headIdx, tail)
		if tailIdx < 0 {
			// The block's last line is new content; it replaces only the head.
			tailIdx = headIdx
		}

		out = append(out, seg.lines...)
		pos = tailIdx + 1
		pendingKeep = false
	}

	// Trailing "keep existing code", or content after the last anchor.
	if pos < len(orig) {
		out = append(out, orig[pos:]...)
	}

	result := strings.Join(out, "\n")
	if strings.HasSuffix(original, "\n") && !strings.HasSuffix(result, "\n") {
		result += "\n"
	}
	return result, nil
}

// uniqueIndexFrom finds the single line in lines[from:] equal to want. Trailing
// whitespace is ignored; leading whitespace is NOT, so a model that reindents
// an anchor line breaks the match.
func uniqueIndexFrom(lines []string, from int, want string) (int, error) {
	w := strings.TrimRight(want, " \t")
	if strings.TrimSpace(w) == "" {
		return 0, fmt.Errorf("anchor line is blank")
	}
	found, count := -1, 0
	for i := from; i < len(lines); i++ {
		if strings.TrimRight(lines[i], " \t") == w {
			count++
			if found < 0 {
				found = i
			}
		}
	}
	switch {
	case count == 0:
		return 0, fmt.Errorf("could not anchor: first line not found in the original file")
	case count > 1:
		return 0, fmt.Errorf("ambiguous anchor: first line occurs %d times in the original file", count)
	}
	return found, nil
}

func indexFrom(lines []string, from int, want string) int {
	w := strings.TrimRight(want, " \t")
	if strings.TrimSpace(w) == "" {
		return -1
	}
	for i := from; i < len(lines); i++ {
		if strings.TrimRight(lines[i], " \t") == w {
			return i
		}
	}
	return -1
}

// blockNum reports the 1-based index of a literal block among literal blocks
// only, so error messages match how a reader counts them in the sketch.
func blockNum(segs []segment, i int) int {
	n := 0
	for j := 0; j <= i; j++ {
		if !segs[j].keep {
			n++
		}
	}
	return n
}

func trimBlankEdges(lines []string) []string {
	for len(lines) > 0 && strings.TrimSpace(lines[0]) == "" {
		lines = lines[1:]
	}
	for len(lines) > 0 && strings.TrimSpace(lines[len(lines)-1]) == "" {
		lines = lines[:len(lines)-1]
	}
	return lines
}

// splitLines splits on "\n" and drops a single trailing empty element so that a
// file ending in a newline does not gain a phantom last line.
// BRITTLE #6: CRLF files are not normalised.
func splitLines(s string) []string {
	lines := strings.Split(s, "\n")
	if n := len(lines); n > 0 && lines[n-1] == "" {
		lines = lines[:n-1]
	}
	return lines
}
