package main

import (
	"bytes"
	"io"
	"os"
	"strings"
	"testing"
)

func TestPrintUsageIncludesBanner(t *testing.T) {
	output := captureStdout(t, printUsage)

	checks := []string{
		openkiroBanner,
		"openkiro - Anthropic API proxy for Kiro/AWS CodeWhisperer",
		"openkiro server [port]",
		"openkiro sandbox <sub-cmd>",
	}
	for _, check := range checks {
		if !strings.Contains(output, check) {
			t.Errorf("printUsage() output missing %q", check)
		}
	}
}

func TestPrintSandboxUsageIncludesBanner(t *testing.T) {
	output := captureStdout(t, printSandboxUsage)

	checks := []string{
		openkiroBanner,
		"openkiro sandbox — manage ephemeral agent sandbox containers",
		"create --id ID [flags]",
		"openkiro sandbox destroy dev-session",
	}
	for _, check := range checks {
		if !strings.Contains(output, check) {
			t.Errorf("printSandboxUsage() output missing %q", check)
		}
	}
}

func captureStdout(t *testing.T, fn func()) string {
	t.Helper()

	origStdout := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("os.Pipe: %v", err)
	}
	os.Stdout = w
	t.Cleanup(func() {
		os.Stdout = origStdout
	})

	fn()

	if err := w.Close(); err != nil {
		t.Fatalf("close writer: %v", err)
	}

	var buf bytes.Buffer
	if _, err := io.Copy(&buf, r); err != nil {
		t.Fatalf("read captured stdout: %v", err)
	}
	if err := r.Close(); err != nil {
		t.Fatalf("close reader: %v", err)
	}

	return buf.String()
}
