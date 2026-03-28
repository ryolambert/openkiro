// headroom — context budget manager for openkiro agent sandboxes.
//
// Reports how much token budget remains for a conversation and warns when the
// budget is exceeded. Can also trim a message array to fit within a budget.
//
// Usage:
//
//	headroom status --max 8000 --used 3000
//	cat messages.json | headroom check --max 8000
//	cat messages.json | headroom trim  --max 8000
//	headroom version
//	headroom help
package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"os"
	"strconv"
	"strings"
)

const charsPerToken = 4

func main() {
	args := os.Args[1:]
	if len(args) == 0 {
		printUsage()
		os.Exit(1)
	}

	switch args[0] {
	case "status":
		runStatus(args[1:])
	case "check":
		runCheck(args[1:])
	case "trim":
		runTrim(args[1:])
	case "version", "--version", "-v":
		fmt.Println("headroom v0.1.0 (openkiro context budget manager)")
	case "help", "--help", "-h":
		printUsage()
	default:
		fmt.Fprintf(os.Stderr, "headroom: unknown command %q\n\n", args[0])
		printUsage()
		os.Exit(1)
	}
}

// runStatus prints a budget summary given --max and --used flags.
func runStatus(args []string) {
	max, used := parseMaxUsed(args)
	available := max - used
	pct := 0.0
	if max > 0 {
		pct = float64(used) / float64(max) * 100
	}

	fmt.Printf("max:       %d tokens\n", max)
	fmt.Printf("used:      %d tokens (%.1f%%)\n", used, pct)
	fmt.Printf("available: %d tokens\n", available)

	if available < 0 {
		fmt.Fprintf(os.Stderr, "headroom: WARNING: over budget by %d tokens\n", -available)
		os.Exit(2) // exit 2 = over budget
	}
	if float64(used)/float64(max) > 0.9 {
		fmt.Fprintln(os.Stderr, "headroom: WARNING: budget is more than 90% full")
	}
}

// runCheck reads JSON messages from stdin, estimates their token count, and
// compares against --max.
func runCheck(args []string) {
	max := parseMax(args)
	msgs := readMessages()
	used := sumTokens(msgs)
	available := max - used
	pct := 0.0
	if max > 0 {
		pct = float64(used) / float64(max) * 100
	}

	fmt.Printf("messages:  %d\n", len(msgs))
	fmt.Printf("used:      %d tokens (%.1f%% of %d)\n", used, pct, max)
	fmt.Printf("available: %d tokens\n", available)

	if available < 0 {
		fmt.Fprintf(os.Stderr, "headroom: over budget by %d tokens\n", -available)
		os.Exit(2)
	}
}

// runTrim reads JSON messages from stdin, trims them to fit within --max
// tokens, and writes the trimmed message array to stdout.
func runTrim(args []string) {
	max := parseMax(args)
	msgs := readMessages()
	before := sumTokens(msgs)

	trimmed := trimToTarget(msgs, max)
	after := sumTokens(trimmed)

	ratio := 0.0
	if before > 0 {
		ratio = 1.0 - float64(after)/float64(before)
	}
	fmt.Fprintf(os.Stderr, "trimmed %d→%d tokens (%.0f%% reduction, %d→%d messages)\n",
		before, after, ratio*100, len(msgs), len(trimmed))

	out, _ := json.Marshal(map[string]any{"messages": trimmed})
	fmt.Println(string(out))
}

// ---- helpers ----

// Message is a single conversation turn.
type Message struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

// EstimateTokens returns a rough token count for s using 4 chars/token.
func EstimateTokens(s string) int {
	if s == "" {
		return 0
	}
	return int(math.Ceil(float64(len(s)) / charsPerToken))
}

func sumTokens(msgs []Message) int {
	total := 0
	for _, m := range msgs {
		total += EstimateTokens(m.Content)
	}
	return total
}

// trimToTarget removes messages from the middle of the conversation until the
// total token count is ≤ target. The first message (system / context) and the
// most recent messages are always preserved by dropping the oldest non-first
// turns one at a time.
func trimToTarget(msgs []Message, target int) []Message {
	for sumTokens(msgs) > target && len(msgs) > 1 {
		// Build a new slice to avoid mutating the shared backing array.
		trimmed := make([]Message, 0, len(msgs)-1)
		trimmed = append(trimmed, msgs[0])
		if len(msgs) > 2 {
			trimmed = append(trimmed, msgs[2:]...)
		}
		msgs = trimmed
	}
	return msgs
}

func readMessages() []Message {
	data, err := io.ReadAll(bufio.NewReader(os.Stdin))
	if err != nil {
		fatal("read stdin: %v", err)
	}
	data = []byte(strings.TrimSpace(string(data)))
	if len(data) == 0 {
		return nil
	}

	if data[0] == '[' {
		var msgs []Message
		if err := json.Unmarshal(data, &msgs); err != nil {
			fatal("parse JSON: %v", err)
		}
		return msgs
	}

	var wrapper struct {
		Messages []Message `json:"messages"`
	}
	if err := json.Unmarshal(data, &wrapper); err != nil {
		fatal("parse JSON: %v", err)
	}
	return wrapper.Messages
}

func parseMaxUsed(args []string) (max, used int) {
	for i := 0; i < len(args)-1; i++ {
		switch args[i] {
		case "--max", "-m":
			v, err := strconv.Atoi(args[i+1])
			if err != nil || v <= 0 {
				fatal("--max must be a positive integer")
			}
			max = v
		case "--used", "-u":
			v, err := strconv.Atoi(args[i+1])
			if err != nil || v < 0 {
				fatal("--used must be a non-negative integer")
			}
			used = v
		}
	}
	if max == 0 {
		fatal("--max TOKEN_LIMIT is required")
	}
	return
}

func parseMax(args []string) int {
	for i := 0; i < len(args)-1; i++ {
		if args[i] == "--max" || args[i] == "-m" {
			v, err := strconv.Atoi(args[i+1])
			if err != nil || v <= 0 {
				fatal("--max must be a positive integer")
			}
			return v
		}
	}
	fatal("--max TOKEN_LIMIT is required")
	return 0
}

func fatal(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "headroom: "+format+"\n", args...)
	os.Exit(1)
}

func printUsage() {
	fmt.Print(`headroom — context budget manager

Usage:
  headroom status --max N --used N   Report budget from explicit counts.
  headroom check  --max N            Estimate tokens from JSON on stdin.
  headroom trim   --max N            Trim JSON messages on stdin to ≤N tokens.
  headroom version                   Print version.
  headroom help                      Show this help.

Exit codes:
  0   Within budget
  1   Error
  2   Over budget (check/status)

Input JSON formats:
  {"messages": [{"role": "user", "content": "..."}]}
  [{"role": "user", "content": "..."}]

Examples:
  headroom status --max 8000 --used 3200
  cat chat.json | headroom check  --max 8000
  cat chat.json | headroom trim   --max 6000 > chat-trimmed.json
`)
}
