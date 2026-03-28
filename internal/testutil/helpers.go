// Package testutil provides shared test helpers for openkiro tests.
package testutil

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

// SetupTestServer spins up a mock HTTP server for integration tests.
// It returns the server and a cleanup function that stops the server.
//
// Example:
//
//	srv, cleanup := testutil.SetupTestServer(t)
//	defer cleanup()
//	// Use srv.URL as the base URL for requests
func SetupTestServer(t *testing.T) (*httptest.Server, func()) {
	t.Helper()

	mux := http.NewServeMux()
	mux.HandleFunc("/v1/messages", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"type":"message","role":"assistant","content":[{"type":"text","text":"ok"}],"stop_reason":"end_turn"}`))
	})
	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"status":"ok"}`))
	})

	srv := httptest.NewServer(mux)
	cleanup := func() { srv.Close() }
	return srv, cleanup
}

// AssertJSONEqual performs a deep JSON comparison between expected and actual.
// It normalises both strings by unmarshalling into any and re-marshalling so
// that key order and whitespace differences are ignored.
func AssertJSONEqual(t *testing.T, expected, actual string) {
	t.Helper()

	var e, a any
	if err := json.Unmarshal([]byte(expected), &e); err != nil {
		t.Fatalf("AssertJSONEqual: expected is not valid JSON: %v", err)
	}
	if err := json.Unmarshal([]byte(actual), &a); err != nil {
		t.Fatalf("AssertJSONEqual: actual is not valid JSON: %v", err)
	}

	eb, err := json.Marshal(e)
	if err != nil {
		t.Fatalf("AssertJSONEqual: could not re-marshal expected value: %v", err)
	}
	ab, err := json.Marshal(a)
	if err != nil {
		t.Fatalf("AssertJSONEqual: could not re-marshal actual value: %v", err)
	}

	if string(eb) != string(ab) {
		t.Errorf("JSON mismatch:\nexpected: %s\n  actual: %s", eb, ab)
	}
}

// LoadTestData loads a fixture file from the testdata directory adjacent to
// the calling test file. name should be the bare filename, e.g. "request.json".
func LoadTestData(t *testing.T, name string) []byte {
	t.Helper()

	// Locate the testdata directory relative to the calling package.
	_, callerFile, _, ok := runtime.Caller(1)
	if !ok {
		t.Fatal("LoadTestData: could not determine caller path")
	}
	path := filepath.Join(filepath.Dir(callerFile), "testdata", name)

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("LoadTestData: cannot read %q: %v", path, err)
	}
	return data
}

// MustMarshal marshals v to a JSON string, failing the test on error.
func MustMarshal(t *testing.T, v any) string {
	t.Helper()

	b, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("MustMarshal: %v", err)
	}
	return string(b)
}
