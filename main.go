package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"
)

func main() {
	var (
		file       = flag.String("file", "", "path to the file to edit (required)")
		instr      = flag.String("instruction", "", "natural-language edit instruction (required)")
		mode       = flag.String("apply", "model", "apply stage: model (second LLM call) | merge (deterministic Go)")
		model      = flag.String("model", DefaultConfig().Model, "OpenAI model id used for both stages")
		maxRetries = flag.Int("max-retries", 2, "how many times to re-run the loop after a failed check")
		verbose    = flag.Bool("verbose", false, "print the sketch, the apply output and the check result")
		write      = flag.Bool("write", false, "overwrite the input file instead of writing <file>.applied")
	)
	flag.Usage = usage
	flag.Parse()

	if *file == "" || *instr == "" {
		usage()
		os.Exit(2)
	}
	if *mode != "model" && *mode != "merge" {
		fmt.Fprintf(os.Stderr, "error: --apply must be \"model\" or \"merge\", got %q\n", *mode)
		os.Exit(2)
	}

	loadDotEnv(".env")

	original, err := os.ReadFile(*file)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}

	cfg := DefaultConfig()
	cfg.Model = *model

	llm, err := NewOpenAIModel(cfg)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}

	p := &Pipeline{
		Sketcher: llm,
		Applier:  llm, // same cheap model, different job (unused in merge mode)
		Mode:     *mode,
		Cfg:      cfg,
		Verbose:  *verbose,
		Out:      os.Stdout,
	}

	result, err := p.Run(context.Background(), *file, string(original), *instr, *maxRetries)
	if result == "" && err != nil {
		fmt.Fprintf(os.Stderr, "\nerror: %v\n", err)
		os.Exit(1)
	}

	out := *file + ".applied"
	if *write {
		out = *file
	}
	if werr := os.WriteFile(out, []byte(result), 0o644); werr != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", werr)
		os.Exit(1)
	}

	fmt.Printf("\n%s %s -> %s  (mode=%s, model=%s, attempts=%d)\n",
		bold("applied:"), *file, out, *mode, cfg.Model, p.Attempts)
	if err != nil {
		fmt.Fprintf(os.Stderr, "%s %v\n", bold("warning:"), err)
		fmt.Fprintf(os.Stderr, "the last attempt was written anyway so you can inspect it.\n")
		os.Exit(1)
	}
}

// Pipeline runs SKETCH -> APPLY -> CHECK, looping on failure.
type Pipeline struct {
	Sketcher Model // stage 1: reads the file, emits a lazy edit
	Applier  Model // stage 2, --apply=model only: stitches the sketch into the file
	Mode     string
	Cfg      Config
	Verbose  bool
	Out      io.Writer

	Attempts int
}

// Run executes the loop and returns the final file content. If the checks never
// pass it returns the last candidate AND an error, so the caller can still
// write the file out for inspection.
func (p *Pipeline) Run(ctx context.Context, path, original, instruction string, maxRetries int) (string, error) {
	commentToken := commentTokenFor(path)
	feedback := ""
	last := ""

	for attempt := 1; attempt <= maxRetries+1; attempt++ {
		p.Attempts = attempt
		p.headerf("ATTEMPT %d of %d", attempt, maxRetries+1)

		// ---- STAGE 1: SKETCH ------------------------------------------------
		// One model call. Reads the whole file, writes only the changed parts.
		sketch, err := p.Sketcher.Complete(ctx,
			sketchSystemPrompt(commentToken),
			sketchUserPrompt(path, original, instruction, feedback))
		if err != nil {
			return last, fmt.Errorf("sketch call failed: %w", err)
		}
		sketch = stripFences(sketch)
		p.stagef("STAGE 1 · SKETCH", "model=%s (lazy edit, markers for unchanged spans)", p.Cfg.Model)
		p.block(sketch, 80)

		// ---- STAGE 2: APPLY -------------------------------------------------
		var applied string
		switch p.Mode {
		case "merge":
			p.stagef("STAGE 2 · APPLY", "mode=merge (deterministic splice, no model call)")
			applied, err = MergeSketch(original, sketch)
		default:
			p.stagef("STAGE 2 · APPLY", "mode=model (second %s call, cheap model does the typing)", p.Cfg.Model)
			applied, err = p.Applier.Complete(ctx,
				applySystemPrompt(commentToken),
				applyUserPrompt(path, original, sketch))
			applied = stripFences(applied)
		}
		if err != nil {
			// A merge that cannot anchor a block is a first-class failure: it
			// goes back to the sketch model as feedback, same as a lint error.
			p.linef("%s %v", red("FAILED"), err)
			feedback = fmt.Sprintf("the deterministic merge could not apply your sketch: %v", err)
			continue
		}
		if strings.TrimSpace(applied) == "" {
			feedback = "the apply stage produced an empty file"
			p.linef("%s empty output", red("FAILED"))
			continue
		}
		if !strings.HasSuffix(applied, "\n") {
			applied += "\n"
		}
		last = applied
		p.block(applied, 60)

		// ---- STAGE 3: CHECK -------------------------------------------------
		report := RunChecks(path, applied)
		p.stagef("STAGE 3 · CHECK", "gofmt + go vet on the result")
		for _, c := range report.Checks {
			p.linef("  %s %-8s %s", statusMark(c.Status), c.Name, c.Detail)
		}
		if !report.Failed() {
			return applied, nil
		}
		// The loop: the error text becomes part of the next sketch prompt.
		feedback = report.Feedback()
	}

	return last, fmt.Errorf("checks still failing after %d attempt(s)", maxRetries+1)
}

// ---------------------------------------------------------------------------
// verbose output
// ---------------------------------------------------------------------------

func (p *Pipeline) headerf(format string, args ...any) {
	if !p.Verbose {
		return
	}
	title := fmt.Sprintf(format, args...)
	fmt.Fprintf(p.Out, "\n%s\n", bold(strings.Repeat("=", 72)))
	fmt.Fprintf(p.Out, "%s\n", bold("  "+title))
	fmt.Fprintf(p.Out, "%s\n", bold(strings.Repeat("=", 72)))
}

func (p *Pipeline) stagef(name, format string, args ...any) {
	if !p.Verbose {
		return
	}
	fmt.Fprintf(p.Out, "\n%s  %s\n", bold("── "+name+" ──"), dim(fmt.Sprintf(format, args...)))
}

func (p *Pipeline) linef(format string, args ...any) {
	if !p.Verbose {
		return
	}
	fmt.Fprintf(p.Out, format+"\n", args...)
}

// block prints file content indented, truncated so a large file does not bury
// the stage headers.
func (p *Pipeline) block(s string, maxLines int) {
	if !p.Verbose {
		return
	}
	lines := strings.Split(strings.TrimRight(s, "\n"), "\n")
	shown := lines
	if len(lines) > maxLines {
		shown = lines[:maxLines]
	}
	fmt.Fprintln(p.Out)
	for _, l := range shown {
		fmt.Fprintf(p.Out, "  %s %s\n", dim("│"), l)
	}
	if len(lines) > maxLines {
		fmt.Fprintf(p.Out, "  %s\n", dim(fmt.Sprintf("│ ... %d more lines", len(lines)-maxLines)))
	}
}

func statusMark(s CheckStatus) string {
	switch s {
	case StatusOK:
		return green("PASS")
	case StatusWarn:
		return "WARN"
	case StatusFail:
		return red("FAIL")
	default:
		return dim("SKIP")
	}
}

// ---------------------------------------------------------------------------
// small helpers
// ---------------------------------------------------------------------------

var useColor = func() bool {
	if os.Getenv("NO_COLOR") != "" {
		return false
	}
	fi, err := os.Stdout.Stat()
	return err == nil && fi.Mode()&os.ModeCharDevice != 0
}()

func paint(code, s string) string {
	if !useColor {
		return s
	}
	return "\x1b[" + code + "m" + s + "\x1b[0m"
}

func bold(s string) string  { return paint("1", s) }
func dim(s string) string   { return paint("2", s) }
func red(s string) string   { return paint("31", s) }
func green(s string) string { return paint("32", s) }

// loadDotEnv reads a very small subset of dotenv (KEY=VALUE, # comments) so the
// shipped .env.example is actually useful. Existing environment variables win.
func loadDotEnv(path string) {
	data, err := os.ReadFile(path)
	if err != nil {
		return
	}
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		line = strings.TrimPrefix(line, "export ")
		k, v, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		k = strings.TrimSpace(k)
		v = strings.Trim(strings.TrimSpace(v), `"'`)
		if _, exists := os.LookupEnv(k); !exists {
			os.Setenv(k, v)
		}
	}
}

func usage() {
	fmt.Fprint(os.Stderr, `applyloop - a two-model edit pipeline in miniature

  SKETCH  a model emits only the changed regions, with marker comments
          standing in for everything it left alone
  APPLY   the sketch plus the original become the full rewritten file,
          either via a second cheap model call or a deterministic Go merge
  CHECK   gofmt and go vet run on the result; failures loop back as feedback

usage:
  applyloop --file <path> --instruction "<edit>" [flags]

flags:
`)
	flag.PrintDefaults()
	fmt.Fprint(os.Stderr, `
examples:
  applyloop --file examples/greeter.go --instruction "add a Farewell function" --verbose
  applyloop --file examples/greeter.go --instruction "add a Farewell function" --apply=merge --verbose

the result is written to <file>.applied unless --write is given.
OPENAI_API_KEY is read from the environment or from ./.env
`)
}
