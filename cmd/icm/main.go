// icm — in-context memory MCP server for openkiro agent sandboxes.
//
// icm provides key-value memory storage and retrieval for agent workloads via
// a simple HTTP JSON API. Memories are stored in memory and optionally
// persisted to a JSON file in /workspace (when the filesystem is writable).
//
// Sub-commands:
//
//	icm serve [--port PORT]        Start the HTTP server (default: 8082).
//	icm store KEY VALUE            Store a single memory (one-shot, no server).
//	icm recall KEY                 Recall a single memory (one-shot, no server).
//	icm list                       List all stored memories.
//
// HTTP API (when using icm serve):
//
//	POST /remember   {"key":"...", "value":"..."}  → stores memory
//	GET  /recall?key=…                             → retrieves memory
//	GET  /list                                     → all memories as JSON
//	DELETE /forget?key=…                           → removes a memory
//	GET  /health                                   → liveness probe
package main

import (
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"time"
)

const (
	defaultPort    = "8082"
	persistFile    = "/workspace/.icm-store.json"
	maxBodyBytes   = 1 << 20 // 1 MB
)

func main() {
	args := os.Args[1:]
	if len(args) == 0 {
		printUsage()
		os.Exit(1)
	}

	store := newMemoryStore()
	// Try to load persisted memories on startup.
	_ = store.load(persistFile)

	switch args[0] {
	case "serve":
		port := defaultPort
		for i := 0; i < len(args)-1; i++ {
			if args[i] == "--port" || args[i] == "-p" {
				port = args[i+1]
			}
		}
		runServer(store, port)

	case "store":
		if len(args) < 3 {
			fatal("store: usage: icm store KEY VALUE")
		}
		store.set(args[1], args[2])
		if err := store.save(persistFile); err != nil {
			fmt.Fprintf(os.Stderr, "warning: could not persist: %v\n", err)
		}
		fmt.Printf("stored: %s\n", args[1])

	case "recall":
		if len(args) < 2 {
			fatal("recall: usage: icm recall KEY")
		}
		v, ok := store.get(args[1])
		if !ok {
			fmt.Fprintf(os.Stderr, "icm: key %q not found\n", args[1])
			os.Exit(1)
		}
		fmt.Println(v)

	case "list":
		all := store.list()
		if len(all) == 0 {
			fmt.Println("(no memories stored)")
			return
		}
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		_ = enc.Encode(all)

	case "version", "--version", "-v":
		fmt.Println("icm v0.1.0 (openkiro in-context memory server)")

	case "help", "--help", "-h":
		printUsage()

	default:
		fmt.Fprintf(os.Stderr, "icm: unknown command %q\n\n", args[0])
		printUsage()
		os.Exit(1)
	}
}

// runServer starts the HTTP memory server on addr.
func runServer(store *memoryStore, port string) {
	mux := http.NewServeMux()
	mux.HandleFunc("/health", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{"status":"ok","tool":"icm"}`)
	})

	mux.HandleFunc("/remember", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "POST required", http.StatusMethodNotAllowed)
			return
		}
		body, err := io.ReadAll(io.LimitReader(r.Body, maxBodyBytes))
		if err != nil {
			http.Error(w, "read body: "+err.Error(), http.StatusBadRequest)
			return
		}
		var req struct {
			Key   string `json:"key"`
			Value string `json:"value"`
		}
		if err := json.Unmarshal(body, &req); err != nil || req.Key == "" {
			http.Error(w, `{"error":"key and value required"}`, http.StatusBadRequest)
			return
		}
		store.set(req.Key, req.Value)
		_ = store.save(persistFile)
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{"ok":true,"key":%q}`, req.Key)
	})

	mux.HandleFunc("/recall", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			http.Error(w, "GET required", http.StatusMethodNotAllowed)
			return
		}
		key := r.URL.Query().Get("key")
		if key == "" {
			http.Error(w, `{"error":"key query param required"}`, http.StatusBadRequest)
			return
		}
		v, ok := store.get(key)
		if !ok {
			http.Error(w, `{"error":"not found"}`, http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		enc := json.NewEncoder(w)
		_ = enc.Encode(map[string]string{"key": key, "value": v})
	})

	mux.HandleFunc("/list", func(w http.ResponseWriter, _ *http.Request) {
		all := store.list()
		w.Header().Set("Content-Type", "application/json")
		enc := json.NewEncoder(w)
		enc.SetIndent("", "  ")
		_ = enc.Encode(all)
	})

	mux.HandleFunc("/forget", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodDelete {
			http.Error(w, "DELETE required", http.StatusMethodNotAllowed)
			return
		}
		key := r.URL.Query().Get("key")
		if key == "" {
			http.Error(w, `{"error":"key query param required"}`, http.StatusBadRequest)
			return
		}
		store.delete(key)
		_ = store.save(persistFile)
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{"ok":true,"key":%q}`, key)
	})

	addr := "127.0.0.1:" + port
	srv := &http.Server{
		Addr:              addr,
		Handler:           mux,
		ReadTimeout:       10 * time.Second,
		WriteTimeout:      10 * time.Second,
		IdleTimeout:       60 * time.Second,
		ReadHeaderTimeout: 5 * time.Second,
	}
	log.Printf("icm memory server listening on %s", addr)
	if err := srv.ListenAndServe(); err != nil {
		fatal("server: %v", err)
	}
}

// memoryStore is a thread-safe in-memory key-value store with optional JSON
// file persistence.
type memoryStore struct {
	mu   sync.RWMutex
	data map[string]string
}

func newMemoryStore() *memoryStore {
	return &memoryStore{data: make(map[string]string)}
}

func (s *memoryStore) set(key, value string) {
	s.mu.Lock()
	s.data[key] = value
	s.mu.Unlock()
}

func (s *memoryStore) get(key string) (string, bool) {
	s.mu.RLock()
	v, ok := s.data[key]
	s.mu.RUnlock()
	return v, ok
}

func (s *memoryStore) delete(key string) {
	s.mu.Lock()
	delete(s.data, key)
	s.mu.Unlock()
}

func (s *memoryStore) list() map[string]string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make(map[string]string, len(s.data))
	for k, v := range s.data {
		out[k] = v
	}
	return out
}

func (s *memoryStore) save(path string) error {
	s.mu.RLock()
	data, err := json.MarshalIndent(s.data, "", "  ")
	s.mu.RUnlock()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o640)
}

func (s *memoryStore) load(path string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err // file doesn't exist — that's fine
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return json.Unmarshal(data, &s.data)
}

func fatal(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "icm: "+format+"\n", args...)
	os.Exit(1)
}

func printUsage() {
	fmt.Print(`icm — in-context memory MCP server

Usage:
  icm serve [--port PORT]   Start the HTTP memory server (default port 8082).
  icm store KEY VALUE       Store a memory entry.
  icm recall KEY            Retrieve a memory entry.
  icm list                  List all stored memories as JSON.
  icm version               Print version.
  icm help                  Show this help.

HTTP API (icm serve):
  POST   /remember   {"key":"x","value":"y"}  — store memory
  GET    /recall?key=x                        — retrieve memory
  GET    /list                                — list all memories
  DELETE /forget?key=x                        — remove memory
  GET    /health                              — liveness probe

Persistence:
  Memories are persisted to /workspace/.icm-store.json when writable.
`)
}
