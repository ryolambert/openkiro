package middleware

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
)

// fakeRtkSrc is a minimal Go program that mimics the rtk binary:
// it reads stdin, applies a simple deduplication filter, and writes to stdout.
// It also responds to "--version" with "rtk 0.99.0-test".
const fakeRtkSrc = `package main

import (
	"bufio"
	"fmt"
	"os"
)

func main() {
	args := os.Args[1:]
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, "usage: rtk <command>")
		os.Exit(1)
	}

	switch args[0] {
	case "--version":
		fmt.Println("rtk 0.99.0-test")
	case "read":
		// Simple deduplication: collapse consecutive identical lines.
		scanner := bufio.NewScanner(os.Stdin)
		var prev string
		count := 0
		for scanner.Scan() {
			line := scanner.Text()
			if line == prev {
				count++
				continue
			}
			if count > 0 {
				fmt.Printf("%s (x%d)\n", prev, count+1)
			} else if prev != "" || count > 0 {
				fmt.Println(prev)
			}
			prev = line
			count = 0
		}
		// Flush last line.
		if count > 0 {
			fmt.Printf("%s (x%d)\n", prev, count+1)
		} else if prev != "" {
			fmt.Println(prev)
		}
	default:
		fmt.Fprintf(os.Stderr, "rtk: unknown command %q\n", args[0])
		os.Exit(1)
	}
}
`

func TestRtkCompressor_WithFakeBinary(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("integration test requires Unix-like PATH manipulation")
	}

	// Build a fake rtk binary that mimics deduplication.
	tmpDir := t.TempDir()
	srcFile := filepath.Join(tmpDir, "fakertk.go")
	if err := os.WriteFile(srcFile, []byte(fakeRtkSrc), 0o644); err != nil {
		t.Fatalf("write fake source: %v", err)
	}

	binPath := filepath.Join(tmpDir, "rtk")
	build := exec.Command("go", "build", "-o", binPath, srcFile)
	build.Stderr = os.Stderr
	if err := build.Run(); err != nil {
		t.Fatalf("build fake rtk: %v", err)
	}

	// Put our fake binary on PATH.
	t.Setenv("PATH", tmpDir+string(filepath.ListSeparator)+os.Getenv("PATH"))

	// Create a new RtkCompressor — it should discover our fake binary.
	c := NewRtkCompressor()
	if !c.Available() {
		t.Fatal("expected RtkCompressor to find the fake rtk binary")
	}

	t.Run("deduplication", func(t *testing.T) {
		input := "hello\nhello\nhello\nworld\nworld"
		got := c.Compress(input)
		if got == input {
			t.Errorf("expected compression to change the input, got %q", got)
		}
		if len(got) >= len(input) {
			t.Errorf("expected compressed output to be shorter: got=%d original=%d", len(got), len(input))
		}
	})

	t.Run("empty_string", func(t *testing.T) {
		got := c.Compress("")
		if got != "" {
			t.Errorf("expected empty string, got %q", got)
		}
	})

	t.Run("no_duplicates", func(t *testing.T) {
		input := "a\nb\nc"
		got := c.Compress(input)
		// Fake rtk should pass through non-duplicate content.
		if got == "" {
			t.Error("expected non-empty output for non-duplicate input")
		}
	})
}

func TestResolveRtkBinary_NotFound(t *testing.T) {
	// Set PATH to empty to ensure rtk is not found.
	t.Setenv("PATH", "")
	path := resolveRtkBinary()
	if path != "" {
		t.Errorf("expected empty path when rtk not on PATH, got %q", path)
	}
}

func TestResolveRtkBinary_GoShimRejected(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("integration test requires Unix-like PATH manipulation")
	}

	// Build a fake binary that prints "openkiro" in its --version,
	// which should be rejected by resolveRtkBinary.
	tmpDir := t.TempDir()
	goShimSrc := `package main
import "fmt"
func main() { fmt.Println("rtk v0.1.0 (openkiro token compression toolkit)") }
`
	srcFile := filepath.Join(tmpDir, "goshim.go")
	if err := os.WriteFile(srcFile, []byte(goShimSrc), 0o644); err != nil {
		t.Fatalf("write source: %v", err)
	}

	binPath := filepath.Join(tmpDir, "rtk")
	build := exec.Command("go", "build", "-o", binPath, srcFile)
	build.Stderr = os.Stderr
	if err := build.Run(); err != nil {
		t.Fatalf("build shim: %v", err)
	}

	t.Setenv("PATH", tmpDir+string(filepath.ListSeparator)+os.Getenv("PATH"))

	path := resolveRtkBinary()
	if path != "" {
		t.Errorf("expected Go shim to be rejected, got %q", path)
	}
}

func TestEstimateTokens(t *testing.T) {
	cases := []struct {
		name  string
		input string
		want  int
	}{
		{"empty", "", 0},
		{"short", "hi", 1},
		{"exact_boundary", "1234", 1},
		{"one_over", "12345", 2},
		{"typical", "Hello, world!", 4},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := estimateTokens(tc.input)
			if got != tc.want {
				t.Errorf("estimateTokens(%q) = %d, want %d", tc.input, got, tc.want)
			}
		})
	}
}
