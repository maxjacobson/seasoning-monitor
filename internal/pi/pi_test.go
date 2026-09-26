package pi

import (
	"context"
	"errors"
	"io"
	"os/exec"
	"strings"
	"testing"
)

func TestProcessReaderStreamsAndReaps(t *testing.T) {
	cmd := exec.Command("sh", "-c", "printf 'one\\ntwo\\n'")
	pipe, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatalf("StdoutPipe: %v", err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}

	pr := &processReader{pipe: pipe, cmd: cmd}
	data, err := io.ReadAll(pr)
	if err != nil {
		t.Fatalf("ReadAll: %v", err)
	}
	if string(data) != "one\ntwo\n" {
		t.Errorf("stream = %q", string(data))
	}
	if err := pr.Close(); err != nil {
		t.Errorf("Close: %v", err)
	}
	if err := pr.Close(); err != nil {
		t.Errorf("second Close should be a no-op: %v", err)
	}
}

func TestProcessReaderReportsExitStatus(t *testing.T) {
	cmd := exec.Command("sh", "-c", "printf 'oops\\n'; exit 3")
	pipe, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatalf("StdoutPipe: %v", err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}

	pr := &processReader{pipe: pipe, cmd: cmd}
	if _, err := io.ReadAll(pr); err != nil {
		t.Fatalf("ReadAll: %v", err)
	}
	err = pr.Close()
	var exitErr *exec.ExitError
	if !errors.As(err, &exitErr) {
		t.Fatalf("Close error = %v, want an *exec.ExitError", err)
	}
	if exitErr.ExitCode() != 3 {
		t.Errorf("exit code = %d, want 3", exitErr.ExitCode())
	}
}

func TestFakeRunnerStream(t *testing.T) {
	runner := FakeRunner{Streams: map[string]string{"tail": "hi\n"}}
	rc, err := runner.Stream(context.Background(), "tail")
	if err != nil {
		t.Fatalf("Stream: %v", err)
	}
	defer rc.Close()

	data, err := io.ReadAll(rc)
	if err != nil {
		t.Fatalf("ReadAll: %v", err)
	}
	if strings.TrimSpace(string(data)) != "hi" {
		t.Errorf("stream = %q", string(data))
	}
}
