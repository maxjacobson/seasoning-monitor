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

# Format all Go files.
fmt:
    gofmt -w .

# Fail if any file needs formatting.
fmt-check:
    test -z "$(gofmt -l .)"

# Everything CI would run: formatting, vet, and tests.
check: fmt-check vet test

# Remove the built binary.
clean:
    rm -f seasoning-monitor
