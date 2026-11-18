package main

import (
	"bytes"
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/wangzw/agent-peer/test/e2e/ctl"
)

// TestServeShutdown starts the command, asks it to stop over the control
// socket, and checks that it exits with zero and removes the socket.
func TestServeShutdown(t *testing.T) {
	dir := t.TempDir()
	binary := filepath.Join(dir, "e2e-node")

	build := exec.Command("go", "build", "-o", binary, ".")
	var buildStderr bytes.Buffer
	build.Stderr = &buildStderr
	if err := build.Run(); err != nil {
		t.Fatalf("building e2e-node: %v\n%s", err, buildStderr.String())
	}

	home := filepath.Join(dir, "home")
	if err := os.MkdirAll(home, 0o700); err != nil {
		t.Fatalf("creating the account home: %v", err)
	}
	socket := filepath.Join(dir, "ctl.sock")

	var stderr bytes.Buffer
	cmd := exec.Command(binary, "serve", "--ctl", socket, "--home", home, "--bin", binary)
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		t.Fatalf("starting e2e-node: %v", err)
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()

	client := ctl.Dial(socket)
	defer client.Close()

	deadline := time.Now().Add(10 * time.Second)
	for {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		err := client.Shutdown(ctx)
		cancel()
		if err == nil {
			break
		}
		if time.Now().After(deadline) {
			_ = cmd.Process.Kill()
			t.Fatalf("the control socket did not answer within 10s: %v\n%s", err, stderr.String())
		}
		time.Sleep(20 * time.Millisecond)
	}

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("e2e-node exited with %v\n%s", err, stderr.String())
		}
	case <-time.After(10 * time.Second):
		_ = cmd.Process.Kill()
		t.Fatalf("e2e-node did not exit after shutdown\n%s", stderr.String())
	}

	if _, err := os.Stat(socket); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("control socket %s still present after shutdown: %v", socket, err)
	}
}
