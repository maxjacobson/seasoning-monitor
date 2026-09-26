# Common tasks for seasoning-monitor.

# Run the interactive TUI (the default: just type `just`).
default: run

# List available recipes.
list:
    @just --list

# Build the binary into ./seasoning-monitor.
build:
    go build -o seasoning-monitor .

# Run the interactive TUI.
run:
    go run .

# Show a raw snapshot over ssh, then exit.
probe:
    go run . --probe

# Show a one-shot rendered summary, then exit.
once:
    go run . --once

# Follow a unit's journal in the terminal (e.g. `just logs seasoning-nightly`).
logs unit:
    go run . --logs {{unit}}

# Run an arbitrary read-only command over ssh (e.g. `just exec 'df -h'`).
exec command:
    go run . --exec '{{command}}'

# Run the unit tests.
test:
    go test ./...

# Run go vet.
vet:
    go vet ./...

# Report the warnings gopls (Zed's Go language server) would show.
lint:
    #!/usr/bin/env sh
    if ! command -v gopls >/dev/null 2>&1; then
        echo "gopls not found; install it with: go install golang.org/x/tools/gopls@latest" >&2
        exit 1
    fi
    out=$(gopls check $(find . -name '*.go'))
    if [ -n "$out" ]; then
        printf '%s\n' "$out" >&2
        exit 1
    fi

# Format all Go files.
fmt:
    gofmt -w .

# Fail if any file needs formatting.
fmt-check:
    test -z "$(gofmt -l .)"

# Everything CI would run: formatting, vet, editor warnings, and tests.
check: fmt-check vet lint test

# Remove the built binary.
clean:
    rm -f seasoning-monitor
