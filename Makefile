# apply-loop â common commands. Run `make` for the list.
#
# Override any variable on the command line, e.g.
#   make demo-merge INSTRUCTION="add a Farewell function"

BINARY      := applyloop
FILE        ?= examples/greeter.go
INSTRUCTION ?= make Greet shout the name in upper case
MODEL       ?= gpt-5-nano
RETRIES     ?= 2
ARGS        ?=

.DEFAULT_GOAL := help

help: ## show this help
	@grep -E '^[a-zA-Z_-]+:.*?## .*$$' $(MAKEFILE_LIST) \
		| awk 'BEGIN{FS=":.*?## "}{printf "  \033[1m%-12s\033[0m %s\n", $$1, $$2}'

build: ## compile ./applyloop
	go build -o $(BINARY) .

test: ## run the unit tests (no API key, no network)
	go test ./...

fmt: ## gofmt the tree in place
	gofmt -l -w .

vet: ## go vet the tree
	go vet ./...

check: fmt vet test ## fmt + vet + test

demo-model: build ## run the pipeline with the model apply stage (needs OPENAI_API_KEY)
	./$(BINARY) --file $(FILE) --instruction "$(INSTRUCTION)" \
		--apply=model --model $(MODEL) --max-retries $(RETRIES) --verbose

demo-merge: build ## same edit, deterministic Go apply stage
	./$(BINARY) --file $(FILE) --instruction "$(INSTRUCTION)" \
		--apply=merge --model $(MODEL) --max-retries $(RETRIES) --verbose

diff: ## show what the last run changed
	@diff -u $(FILE) $(FILE).applied || true

run: build ## pass your own flags: make run ARGS="--file x.go --instruction 'y'"
	./$(BINARY) $(ARGS)

install: ## go install applyloop into GOBIN
	go install .

clean: ## remove the binary and any *.applied output
	rm -f $(BINARY)
	rm -f *.applied examples/*.applied

.PHONY: help build test fmt vet check demo-model demo-merge diff run install clean
