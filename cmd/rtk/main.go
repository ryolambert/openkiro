// rtk — token compression toolkit for openkiro agent sandboxes.
//
// Estimates token counts for plain text or JSON message arrays and reports
// compression statistics. Uses the standard heuristic of 4 characters per
// token (suitable for English prose and typical agent prompts).
//
// Usage:
//
//	rtk count "some text here"
//	echo "some text"          | rtk count
//	cat messages.json         | rtk estimate
//	cat messages.json         | rtk compress --target 2000
//	rtk help
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

// charsPerToken is the average English characters-per-token heuristic used by
// Anthropic and OpenAI tokenizers.
const charsPerToken = 4

func main() {
	args := os.Args[1:]
	if len(args) == 0 {
		printUsage()
		os.Exit(1)
	}

	switch args[0] {
	case "count":
		runCount(args[1:])
	case "estimate":
		runEstimate()
	case "compress":
		runCompress(args[1:])
	case "version", "--version", "-v":
		fmt.Println("rtk v0.1.0 (openkiro token compression toolkit)")
	case "help", "--help", "-h":
		printUsage()
	default:
		fmt.Fprintf(os.Stderr, "rtk: unknown command %q\n\n", args[0])
		printUsage()
		os.Exit(1)
	}
}

// runCount prints the estimated token count for text supplied as arguments or
// via stdin. Each argument is counted separately; stdin is counted as a whole.
func runCount(args []string) {
	if len(args) > 0 {
		text := strings.Join(args, " ")
		tokens := EstimateTokens(text)
		fmt.Printf("tokens: %d  chars: %d  text: %q\n", tokens, len(text), truncate(text, 40))
		return
	}

	// Read from stdin.
	data, err := io.ReadAll(bufio.NewReader(os.Stdin))
	if err != nil {
		fatal("read stdin: %v", err)
	}
	text := string(data)
	tokens := EstimateTokens(text)
	chars := len(text)
	fmt.Printf("tokens: %d  chars: %d\n", tokens, chars)
}

// runEstimate reads a JSON message array from stdin and prints per-message and
// total token estimates.
//
// Accepted formats:
//
//	{"messages": [{"role": "user", "content": "hello"}]}
//	[{"role": "user", "content": "hello"}]
func runEstimate() {
	data, err := io.ReadAll(bufio.NewReader(os.Stdin))
	if err != nil {
		fatal("read stdin: %v", err)
	}

	msgs, err := parseMessages(data)
	if err != nil {
		fatal("parse messages: %v", err)
	}

	if len(msgs) == 0 {
		fmt.Println("tokens: 0  (no messages)")
		return
	}

	total := 0
	for i, m := range msgs {
		t := EstimateTokens(m.Content)
		total += t
		fmt.Printf("  [%d] role=%-10s tokens=%-6d chars=%d\n", i, m.Role, t, len(m.Content))
	}
	fmt.Printf("total: %d tokens across %d messages\n", total, len(msgs))
}

// runCompress reads a JSON message array from stdin and removes messages from
// the middle of the conversation to bring the token count under --target.
func runCompress(args []string) {
	target := 0
	for i := 0; i < len(args)-1; i++ {
		if args[i] == "--target" || args[i] == "-t" {
			v, err := strconv.Atoi(args[i+1])
			if err != nil || v <= 0 {
				fatal("--target must be a positive integer")
			}
			target = v
		}
	}
	if target == 0 {
		fatal("compress: --target TOKEN_LIMIT is required")
	}

	data, err := io.ReadAll(bufio.NewReader(os.Stdin))
	if err != nil {
		fatal("read stdin: %v", err)
	}

	msgs, err := parseMessages(data)
	if err != nil {
		fatal("parse messages: %v", err)
	}

	before := sumTokens(msgs)
	compressed := trimToTarget(msgs, target)
	after := sumTokens(compressed)

	ratio := float64(0)
	if before > 0 {
		ratio = 1.0 - float64(after)/float64(before)
	}
	fmt.Fprintf(os.Stderr, "compressed %d→%d tokens (%.0f%% reduction, %d→%d messages)\n",
		before, after, ratio*100, len(msgs), len(compressed))

	out, _ := json.Marshal(map[string]any{"messages": compressed})
	fmt.Println(string(out))
}

// Message is a minimal representation of a conversation turn.
type Message struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

// EstimateTokens returns a rough token count for s using the 4 chars/token
// heuristic. The result is always ≥ 1 for non-empty input.
func EstimateTokens(s string) int {
	if s == "" {
		return 0
	}
	return int(math.Ceil(float64(len(s)) / charsPerToken))
}

// parseMessages accepts either a {"messages":[...]} object or a bare [...] array.
func parseMessages(data []byte) ([]Message, error) {
	data = []byte(strings.TrimSpace(string(data)))
	if len(data) == 0 {
		return nil, nil
	}

	if data[0] == '[' {
		var msgs []Message
		return msgs, json.Unmarshal(data, &msgs)
	}

	var wrapper struct {
		Messages []Message `json:"messages"`
	}
	if err := json.Unmarshal(data, &wrapper); err != nil {
		return nil, err
	}
	return wrapper.Messages, nil
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

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}

func fatal(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "rtk: "+format+"\n", args...)
	os.Exit(1)
}

func printUsage() {
	fmt.Print(`rtk — token compression toolkit

Usage:
  rtk count [TEXT]            Estimate tokens for TEXT (or stdin if omitted).
  rtk estimate                Estimate tokens per message from JSON on stdin.
  rtk compress --target N     Trim JSON message array on stdin to ≤N tokens.
  rtk version                 Print version.
  rtk help                    Show this help.

Input formats for estimate/compress:
  {"messages": [{"role": "user", "content": "..."}]}
  [{"role": "user", "content": "..."}]

Examples:
  rtk count "Hello, world!"
  echo "My prompt text" | rtk count
  cat chat.json | rtk estimate
  cat chat.json | rtk compress --target 4000 > chat-compressed.json
`)
}
