# apply-loop

A small Go CLI — under 1,000 lines, a good half of them comments — showing how
AI coding tools actually apply an edit: not with one model writing out your
whole file, but with **two cheap calls and a lint loop**.

It is a teaching artifact for a newsletter teardown. It is small on purpose, and
the parts that do not work are documented rather than hidden.

## The idea in five lines

Rewriting a 400-line file to change 3 lines costs 400 lines of output tokens,
and output tokens are the expensive, slow ones. So the work is split:

1. **SKETCH** — a model reads the file and the instruction and emits a *lazy*
   edit: only the regions that change, with every untouched span collapsed into
   a marker comment, `// ... keep existing code ...`.
2. **APPLY** — the lazy sketch plus the original become the full file. This step
   needs no intelligence, only accuracy.
3. **CHECK** — `gofmt` and `go vet` run on the result. If they fail, the error is
   appended to the prompt and the loop runs again.

In real products the sketch is done by the big, expensive model and the apply by
a small fast one. Here both are `gpt-5-nano`, because the point is the shape of
the pipeline, not the benchmark.

## Install and run

```bash
cp .env.example .env   # then put your key in it
make build             # or: go build -o applyloop .
```

`OPENAI_API_KEY` is read from the environment, falling back to `./.env`.

```bash
make demo-merge        # run the pipeline, deterministic apply
make diff              # see what it changed
```

The result is written to `examples/greeter.go.applied`. The input file is never
touched unless you pass `--write`.

`make` on its own lists every target:

| target | what it does |
| --- | --- |
| `make build` | compile `./applyloop` |
| `make test` | unit tests — no API key, no network |
| `make check` | fmt + vet + test |
| `make demo-model` | run the pipeline with the model apply stage |
| `make demo-merge` | same edit, deterministic apply stage |
| `make diff` | diff the input against the last `.applied` output |
| `make run ARGS="..."` | pass your own flags |
| `make clean` | remove the binary and `.applied` files |

`FILE`, `INSTRUCTION`, `MODEL` and `RETRIES` are overridable:

```bash
make demo-merge FILE=internal/foo.go INSTRUCTION="return an error instead of panicking"
```

```
applyloop --file <path> --instruction "<edit>" [flags]

  --apply model|merge   how stage 2 rebuilds the file (default model)
  --model string        OpenAI model id for both stages (default gpt-5-nano)
  --max-retries int     retries after a failed check (default 2)
  --verbose             print the sketch, the apply output and the check result
  --write               overwrite the input instead of writing <file>.applied
```

## The two apply modes

This is the interesting flag. Both modes get the same sketch; they differ only
in who turns it back into a file.

```bash
# stage 2 is a second gpt-5-nano call: "cheap model does the typing"
make demo-model INSTRUCTION="add a Farewell function"

# stage 2 is pure Go: parse the markers, splice the blocks in by anchor matching
make demo-merge INSTRUCTION="add a Farewell function"
```

`--apply=merge` exists so you can feel why `--apply=model` exists. Run the same
instruction through both a few times. The merge is free, instant and
deterministic — right up until the sketch does something slightly unexpected,
at which point it either refuses or, worse, quietly puts code in the wrong
place. Every one of those cases is marked `BRITTLE #n` in [merge.go](merge.go).

The deterministic merge in one paragraph: split the sketch into marker segments
and literal blocks; for each block, look up its **first line** in the original
(it must match exactly once) and its **last line** (first match after the head).
Those two positions delimit the span the block replaces. Marker segments stand
for whatever original lines fall between two anchored blocks.

## What `--verbose` looks like

```
========================================================================
  ATTEMPT 1 of 3
========================================================================

── STAGE 1 · SKETCH ──  model=gpt-5-nano (lazy edit, markers for unchanged spans)

  │ // ... keep existing code ...
  │ func Greet(name string) string {
  │ 	if strings.TrimSpace(name) == "" {
  │ 		name = "world"
  │ 	}
  │ 	return fmt.Sprintf("Hey there, %s!", strings.ToUpper(name))
  │ }
  │ // ... keep existing code ...

── STAGE 2 · APPLY ──  mode=merge (deterministic splice, no model call)

  │ // Command greeter is the sample file to run the apply loop against.
  │ package main
  │ ... 28 more lines

── STAGE 3 · CHECK ──  gofmt + go vet on the result
  PASS gofmt    parses and is formatted
  PASS go vet   no findings
```

## Where to read

| file | what it holds |
| --- | --- |
| [prompts.go](prompts.go) | both prompts, commented. Start here. |
| [merge.go](merge.go) | the deterministic apply, with every failure mode marked |
| [main.go](main.go) | the loop itself: sketch → apply → check → feedback |
| [check.go](check.go) | what counts as a failure worth retrying |
| [config.go](config.go) | all model settings, in one struct |
| [Makefile](Makefile) | every command in this README |

## Limitations

Honest list, because the code is the argument:

- **This is not how Cursor works internally.** Production apply steps use a
  purpose-trained fast model with speculative decoding against the original
  file, which is both faster and far more reliable than a generic small model
  reading a prompt. This repo demonstrates the *shape* of that design.
- **No cost or latency numbers are claimed.** Nothing here is benchmarked. The
  "output tokens are expensive" argument is the standard one; measure it
  yourself before quoting a number.
- **The deterministic merge is brittle by construction**, in six documented ways:
  the marker regexp is prose matching; head anchors must be unique, so a block
  starting with `}` cannot be placed; an unanchorable final block is *assumed* to
  belong at the end of the file; a missing marker silently deletes lines; the
  tail anchor takes the first match, so a nested closing brace ends the span
  early; CRLF files are not normalised. It also cannot express reordering, and
  anchor matching is whitespace-exact on the left.
- **The checker only understands Go.** Other extensions report `SKIP`. `go vet`
  runs the file alone in a scratch module, so anything importing third-party
  packages reports `SKIP` too.
- **Formatting drift is a warning, not a failure.** Only parse errors and vet
  errors trigger a retry; burning a model call because `gofmt` would reindent a
  line is not worth it.
- **A retry re-runs the sketch from scratch** with the error appended. There is
  no conversation state, no diff of what went wrong, no partial reuse.
- **Single file, whole file in the prompt.** No repo context, no chunking, no
  streaming, no concurrent edits.

## Tests

```bash
make test
```

Model calls sit behind a one-method `Model` interface, so nothing in the test
suite touches the network. The merge tests cover a clean anchored edit, an
ambiguous anchor that the merge refuses rather than guesses, an unanchorable
block, and the append heuristic — read them as a tour of the brittleness.

## License

MIT. See [LICENSE](LICENSE).
