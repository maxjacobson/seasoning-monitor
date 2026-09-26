// Package pi runs read-only commands on the Raspberry Pi that hosts Seasoning.
package pi

import (
	"context"
	"fmt"
	"io"
	"os/exec"
	"strings"
	"sync"
)

// Runner executes shell commands somewhere.
type Runner interface {
	// Run runs a command to completion and returns its combined output.
	Run(ctx context.Context, command string) (string, error)
	// Stream runs a long-lived command and returns a reader over its combined
	// output. Closing the reader terminates the process.
	Stream(ctx context.Context, command string) (io.ReadCloser, error)
}

// SSHRunner runs commands over ssh, e.g. `ssh pi <command>`.
type SSHRunner struct {
	Host string
}

func (r SSHRunner) host() string {
	if r.Host == "" {
		return "pi"
	}
	return r.Host
}

// Run executes command on the remote host via the local ssh client. The
// command is fed to `sh` on the remote side over stdin, so it does not matter
// what the remote user's login shell is.
func (r SSHRunner) Run(ctx context.Context, command string) (string, error) {
	cmd := exec.CommandContext(ctx, "ssh", r.host(), "sh", "-s")
	cmd.Stdin = strings.NewReader(command)
	var out strings.Builder
	cmd.Stdout = &out
	cmd.Stderr = &out
	if err := cmd.Run(); err != nil {
		return out.String(), fmt.Errorf("ssh %s: %w", r.host(), err)
	}
	return out.String(), nil
}

// Stream starts command over ssh and returns a reader that yields its output as
// it is produced. The command is fed to `sh` over stdin, as in Run.
func (r SSHRunner) Stream(ctx context.Context, command string) (io.ReadCloser, error) {
	cmd := exec.CommandContext(ctx, "ssh", r.host(), "sh", "-s")
	cmd.Stdin = strings.NewReader(command)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, fmt.Errorf("ssh %s: %w", r.host(), err)
	}
	cmd.Stderr = cmd.Stdout
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("ssh %s: %w", r.host(), err)
	}
	return &processReader{pipe: stdout, cmd: cmd}, nil
}

// processReader streams a running command's output and reaps it on Close.
type processReader struct {
	pipe io.ReadCloser
	cmd  *exec.Cmd
	once sync.Once
}

func (p *processReader) Read(b []byte) (int, error) {
	return p.pipe.Read(b)
}

func (p *processReader) Close() error {
	p.once.Do(func() {
		_ = p.pipe.Close()
		_ = p.cmd.Wait()
	})
	return nil
}

// FakeRunner is a Runner for tests that returns canned output.
type FakeRunner struct {
	Outputs map[string]string
	Streams map[string]string
	Err     error
}

// Run returns the canned output for the exact command, if any.
func (f FakeRunner) Run(_ context.Context, command string) (string, error) {
	if f.Err != nil {
		return "", f.Err
	}
	if out, ok := f.Outputs[command]; ok {
		return out, nil
	}
	return "", fmt.Errorf("no canned output for command: %s", command)
}

// Stream returns a reader over the canned stream for the exact command.
func (f FakeRunner) Stream(_ context.Context, command string) (io.ReadCloser, error) {
	if f.Err != nil {
		return nil, f.Err
	}
	if out, ok := f.Streams[command]; ok {
		return io.NopCloser(strings.NewReader(out)), nil
	}
	return io.NopCloser(strings.NewReader("")), nil
}
