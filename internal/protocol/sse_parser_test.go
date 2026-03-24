package protocol

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"io"
	"sync"
	"testing"
)

func encodeTestFrame(t *testing.T, payload any) []byte {
	t.Helper()

	data, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("marshal payload: %v", err)
	}

	var frame bytes.Buffer
	totalLen := uint32(len(data) + 12)
	if err := binary.Write(&frame, binary.BigEndian, totalLen); err != nil {
		t.Fatalf("write totalLen: %v", err)
	}
	if err := binary.Write(&frame, binary.BigEndian, uint32(0)); err != nil {
		t.Fatalf("write headerLen: %v", err)
	}
	frame.Write(data)
	frame.Write([]byte{0, 0, 0, 0})
	return frame.Bytes()
}

func TestParseEventsTextEndTurn(t *testing.T) {
	frames := bytes.Join([][]byte{
		encodeTestFrame(t, assistantResponseEvent{Content: "hello"}),
		encodeTestFrame(t, assistantResponseEvent{Stop: true}),
	}, nil)
	events := ParseEvents(frames)

	if len(events) != 4 {
		t.Fatalf("expected 4 events, got %d", len(events))
	}

	if events[0].Event != "content_block_start" {
		t.Fatalf("expected first event content_block_start, got %q", events[0].Event)
	}
	start := events[0].Data.(map[string]any)
	if got := start["index"]; got != 0 {
		t.Fatalf("expected text block index 0, got %#v", got)
	}
	block := start["content_block"].(map[string]any)
	if got := block["type"]; got != "text" {
		t.Fatalf("expected text block type, got %#v", got)
	}

	delta := events[1].Data.(map[string]any)["delta"].(map[string]any)
	if got := delta["type"]; got != "text_delta" {
		t.Fatalf("expected text_delta, got %#v", got)
	}
	if got := delta["text"]; got != "hello" {
		t.Fatalf("expected text %q, got %#v", "hello", got)
	}

	stop := events[2].Data.(map[string]any)
	if got := stop["index"]; got != 0 {
		t.Fatalf("expected text stop index 0, got %#v", got)
	}

	messageDelta := events[3].Data.(map[string]any)["delta"].(map[string]any)
	if got := messageDelta["stop_reason"]; got != "end_turn" {
		t.Fatalf("expected stop_reason end_turn, got %#v", got)
	}
}

func TestParseEventsToolUseSequence(t *testing.T) {
	input := `{"query":"drift"}`
	frames := bytes.Join([][]byte{
		encodeTestFrame(t, assistantResponseEvent{Content: "Need tool"}),
		encodeTestFrame(t, assistantResponseEvent{ToolUseId: "tool-1", Name: "search"}),
		encodeTestFrame(t, assistantResponseEvent{ToolUseId: "tool-1", Name: "search", Input: &input}),
		encodeTestFrame(t, assistantResponseEvent{ToolUseId: "tool-1", Stop: true}),
	}, nil)

	events := ParseEvents(frames)
	if len(events) != 7 {
		t.Fatalf("expected 7 events, got %d", len(events))
	}

	textStop := events[2].Data.(map[string]any)
	if got := textStop["index"]; got != 0 {
		t.Fatalf("expected text block stop index 0, got %#v", got)
	}

	if events[3].Event != "content_block_start" {
		t.Fatalf("expected tool start event, got %q", events[3].Event)
	}
	start := events[3].Data.(map[string]any)["content_block"].(map[string]any)
	if got := start["type"]; got != "tool_use" {
		t.Fatalf("expected tool_use block, got %#v", got)
	}
	if got := start["id"]; got != "tool-1" {
		t.Fatalf("expected tool id %q, got %#v", "tool-1", got)
	}

	delta := events[4].Data.(map[string]any)["delta"].(map[string]any)
	if got := delta["type"]; got != "input_json_delta" {
		t.Fatalf("expected input_json_delta, got %#v", got)
	}
	var partial string
	switch v := delta["partial_json"].(type) {
	case *string:
		partial = *v
	case string:
		partial = v
	default:
		t.Fatalf("expected partial_json as string or *string, got %T", delta["partial_json"])
	}
	if got := partial; got != input {
		t.Fatalf("expected partial_json %q, got %q", input, got)
	}

	stop := events[5].Data.(map[string]any)
	if got := stop["type"]; got != "content_block_stop" {
		t.Fatalf("expected content_block_stop, got %#v", got)
	}
	if got := stop["index"]; got != 1 {
		t.Fatalf("expected tool stop index 1, got %#v", got)
	}

	messageDelta := events[6].Data.(map[string]any)["delta"].(map[string]any)
	if got := messageDelta["stop_reason"]; got != "tool_use" {
		t.Fatalf("expected stop_reason tool_use, got %#v", got)
	}
}

func TestParseEventsMetadataPing(t *testing.T) {
	frames := encodeTestFrame(t, map[string]any{"contextUsagePercentage": 73})
	events := ParseEvents(frames)

	if len(events) != 1 {
		t.Fatalf("expected 1 event, got %d", len(events))
	}
	if events[0].Event != "ping" {
		t.Fatalf("expected ping event, got %q", events[0].Event)
	}
	data := events[0].Data.(map[string]any)
	if got := data["type"]; got != "ping" {
		t.Fatalf("expected ping data type, got %#v", got)
	}
}

func TestParseEventStreamIncremental(t *testing.T) {
	pr, pw := io.Pipe()

	var mu sync.Mutex
	var received []SSEEvent

	done := make(chan error, 1)
	go func() {
		done <- ParseEventStream(pr, func(evt SSEEvent) error {
			mu.Lock()
			received = append(received, evt)
			mu.Unlock()
			return nil
		})
	}()

	// Write first frame — should emit content_block_start + content_block_delta
	pw.Write(encodeTestFrame(t, assistantResponseEvent{Content: "hi"}))

	// Write stop frame — should emit content_block_stop + message_delta
	pw.Write(encodeTestFrame(t, assistantResponseEvent{Stop: true}))

	pw.Close()

	if err := <-done; err != nil {
		t.Fatalf("ParseEventStream error: %v", err)
	}

	mu.Lock()
	defer mu.Unlock()
	if len(received) != 4 {
		t.Fatalf("expected 4 events, got %d", len(received))
	}
	wantOrder := []string{"content_block_start", "content_block_delta", "content_block_stop", "message_delta"}
	for i, want := range wantOrder {
		if received[i].Event != want {
			t.Fatalf("event[%d] = %q, want %q", i, received[i].Event, want)
		}
	}
}

func TestParseEventStreamEmitError(t *testing.T) {
	frame := encodeTestFrame(t, assistantResponseEvent{Content: "x"})
	errSentinel := io.ErrClosedPipe

	err := ParseEventStream(bytes.NewReader(frame), func(evt SSEEvent) error {
		return errSentinel
	})
	if err != errSentinel {
		t.Fatalf("expected sentinel error, got %v", err)
	}
}

func TestParseEventStreamFinalizesWithoutStop(t *testing.T) {
	// Stream with content but no stop frame — finalization should close block + emit message_delta
	frame := encodeTestFrame(t, assistantResponseEvent{Content: "partial"})
	var events []SSEEvent
	err := ParseEventStream(bytes.NewReader(frame), func(evt SSEEvent) error {
		events = append(events, evt)
		return nil
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(events) != 4 {
		t.Fatalf("expected 4 events (start+delta+stop+message_delta), got %d", len(events))
	}
	if events[2].Event != "content_block_stop" {
		t.Fatalf("expected finalization content_block_stop, got %q", events[2].Event)
	}
	if events[3].Event != "message_delta" {
		t.Fatalf("expected finalization message_delta, got %q", events[3].Event)
	}
}
