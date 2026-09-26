// Package pi runs read-only commands on the Raspberry Pi that hosts Seasoning.
package pi

import (
	"context"
	"fmt"
	"os/exec"
	"strings"
)

// Runner executes a shell command somewhere and returns its combined output.
type Runner interface {
	Run(ctx context.Context, command string) (string, error)
}

// SSHRunner runs commands over ssh, e.g. `ssh pi <command>`.
type SSHRunner struct {
	Host string
}

// Run executes command on the remote host via the local ssh client. The
// command is fed to `sh` on the remote side over stdin, so it does not matter
// what the remote user's login shell is.
func (r SSHRunner) Run(ctx context.Context, command string) (string, error) {
	host := r.Host
	if host == "" {
		host = "pi"
	}
	cmd := exec.CommandContext(ctx, "ssh", host, "sh", "-s")
	cmd.Stdin = strings.NewReader(command)
	var out strings.Builder
	cmd.Stdout = &out
	cmd.Stderr = &out
	if err := cmd.Run(); err != nil {
		return out.String(), fmt.Errorf("ssh %s: %w", host, err)
	}
	return out.String(), nil
}

// FakeRunner is a Runner for tests that returns canned output.
type FakeRunner struct {
	Outputs map[string]string
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
