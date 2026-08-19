package main

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// ---------------------------------------------------------------------------
// CHECK stage. Whatever produced the file (a model or the Go merge), something
// mechanical has to tell us whether it is even valid. For Go that is gofmt's
// parser plus go vet's type checker. A failure here becomes feedback text and
// the loop runs again.
// ---------------------------------------------------------------------------

type CheckStatus string

const (
	StatusOK      CheckStatus = "ok"
	StatusWarn    CheckStatus = "warn"
	StatusFail    CheckStatus = "fail"
	StatusSkipped CheckStatus = "skipped"
)

type Check struct {
	Name   string
	Status CheckStatus
	Detail string
}

type CheckReport struct {
	Checks []Check
}

// Failed reports whether the loop should retry. Only hard failures count:
// a parse error or a vet error. Formatting drift is reported as a warning
// because "gofmt would reindent line 12" is not worth another model call.
func (r CheckReport) Failed() bool {
	for _, c := range r.Checks {
		if c.Status == StatusFail {
			return true
		}
	}
	return false
}

// Feedback is the text appended to the next sketch prompt.
func (r CheckReport) Feedback() string {
	var b strings.Builder
	for _, c := range r.Checks {
		if c.Status == StatusFail {
			fmt.Fprintf(&b, "%s: %s\n", c.Name, c.Detail)
		}
	}
	return strings.TrimSpace(b.String())
}

// RunChecks writes the candidate content to a scratch directory and runs the
// checkers appropriate to its extension.
func RunChecks(path, content string) CheckReport {
	if !strings.EqualFold(filepath.Ext(path), ".go") {
		return CheckReport{Checks: []Check{{
			Name:   "check",
			Status: StatusSkipped,
			Detail: fmt.Sprintf("no checker wired up for %q files", filepath.Ext(path)),
		}}}
	}

	dir, err := os.MkdirTemp("", "applyloop-check-")
	if err != nil {
		return CheckReport{Checks: []Check{{Name: "check", Status: StatusFail, Detail: err.Error()}}}
	}
	defer os.RemoveAll(dir)

	target := filepath.Join(dir, filepath.Base(path))
	if err := os.WriteFile(target, []byte(content), 0o644); err != nil {
		return CheckReport{Checks: []Check{{Name: "check", Status: StatusFail, Detail: err.Error()}}}
	}

	return CheckReport{Checks: []Check{runGofmt(target), runGoVet(dir, target)}}
}

// runGofmt uses gofmt as a parser first and a formatter second. "-e" prints all
// syntax errors; a non-zero exit means the file does not parse, which is the
// failure mode the retry loop exists for. "-l" lists the file when it is valid
// but not canonically formatted, which we only warn about.
func runGofmt(target string) Check {
	out, stderr, err := run(filepath.Dir(target), "gofmt", "-l", "-e", target)
	if err != nil {
		detail := strings.TrimSpace(stderr)
		if detail == "" {
			detail = err.Error()
		}
		return Check{Name: "gofmt", Status: StatusFail, Detail: cleanPaths(detail, target)}
	}
	if strings.TrimSpace(out) != "" {
		return Check{Name: "gofmt", Status: StatusWarn, Detail: "file parses but is not gofmt-formatted"}
	}
	return Check{Name: "gofmt", Status: StatusOK, Detail: "parses and is formatted"}
}

// runGoVet type-checks the result, which catches the errors that actually
// matter after a bad merge: undefined identifiers, duplicated declarations, a
// function body spliced in twice.
//
// The file is vetted alone in a scratch module, so anything importing a
// third-party package cannot be resolved. That is reported as skipped, not as a
// failure; it would be dishonest to retry over a missing dependency.
func runGoVet(dir, target string) Check {
	gomod := "module applyloopcheck\n\ngo 1.21\n"
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte(gomod), 0o644); err != nil {
		return Check{Name: "go vet", Status: StatusSkipped, Detail: err.Error()}
	}
	_, stderr, err := run(dir, "go", "vet", ".")
	if err == nil {
		return Check{Name: "go vet", Status: StatusOK, Detail: "no findings"}
	}
	detail := strings.TrimSpace(stderr)
	for _, skip := range []string{
		"no required module provides package",
		"cannot find module",
		"missing go.sum",
		"updates to go.mod needed",
		"executable file not found",
	} {
		if strings.Contains(detail, skip) {
			return Check{Name: "go vet", Status: StatusSkipped, Detail: "needs dependencies this scratch module does not have"}
		}
	}
	if detail == "" {
		detail = err.Error()
	}
	return Check{Name: "go vet", Status: StatusFail, Detail: cleanPaths(detail, target)}
}

func run(dir, name string, args ...string) (stdout, stderr string, err error) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Dir = dir
	var so, se strings.Builder
	cmd.Stdout = &so
	cmd.Stderr = &se
	err = cmd.Run()
	return so.String(), se.String(), err
}

// cleanPaths strips the scratch directory out of tool output so the error the
// user (and the model) reads mentions the file, not /var/folders/...
func cleanPaths(s, target string) string {
	s = strings.ReplaceAll(s, target, filepath.Base(target))
	s = strings.ReplaceAll(s, filepath.Dir(target)+"/", "")
	return strings.TrimSpace(s)
}
