package gateway

import (
	"context"
	"errors"
	"strings"
	"testing"
)

// fakeRunner implements DockerRunner for unit tests.
type fakeRunner struct {
	calls  [][]string
	output map[string][]byte
	errs   map[string]error
}

func newFakeRunner() *fakeRunner {
	return &fakeRunner{
		output: make(map[string][]byte),
		errs:   make(map[string]error),
	}
}

func (f *fakeRunner) setOutput(subcmd string, out []byte) { f.output[subcmd] = out }
func (f *fakeRunner) setError(subcmd string, err error)   { f.errs[subcmd] = err }

func (f *fakeRunner) Run(_ context.Context, args ...string) ([]byte, error) {
	f.calls = append(f.calls, append([]string(nil), args...))
	subcmd := ""
	if len(args) > 0 {
		subcmd = args[0]
	}
	if err, ok := f.errs[subcmd]; ok {
		return nil, err
	}
	if out, ok := f.output[subcmd]; ok {
		return out, nil
	}
	return []byte{}, nil
}

// ---- Gateway.Discover tests ----

func TestDiscover_NoContainers(t *testing.T) {
	runner := newFakeRunner()
	runner.setOutput("ps", []byte(""))
	gw := newGatewayWithRunner(runner)

	servers, err := gw.Discover(context.Background())
	if err != nil {
		t.Fatalf("Discover: %v", err)
	}
	if len(servers) != 0 {
		t.Errorf("Discover returned %d servers, want 0", len(servers))
	}
}

func TestDiscover_SingleHTTPServer(t *testing.T) {
	runner := newFakeRunner()
	runner.setOutput("ps", []byte("abc1234567890"))
	runner.setOutput("inspect", []byte(`[{
		"Id": "abc1234567890",
		"Config": {
			"Labels": {
				"mcp.enable":    "true",
				"mcp.name":      "memory-server",
				"mcp.transport": "http",
				"mcp.port":      "9090",
				"mcp.path":      "/tools"
			}
		},
		"NetworkSettings": {
			"Ports": {
				"9090/tcp": [{"HostIp": "0.0.0.0", "HostPort": "9090"}]
			}
		},
		"State": {"Running": true}
	}]`))

	gw := newGatewayWithRunner(runner)
	servers, err := gw.Discover(context.Background())
	if err != nil {
		t.Fatalf("Discover: %v", err)
	}
	if len(servers) != 1 {
		t.Fatalf("Discover returned %d servers, want 1", len(servers))
	}

	s := servers[0]
	if s.Name != "memory-server" {
		t.Errorf("Name = %q, want memory-server", s.Name)
	}
	if s.Transport != TransportHTTP {
		t.Errorf("Transport = %q, want http", s.Transport)
	}
	if s.Path != "/tools" {
		t.Errorf("Path = %q, want /tools", s.Path)
	}
	if !strings.HasSuffix(s.Address, ":9090") {
		t.Errorf("Address = %q, want suffix :9090", s.Address)
	}
}

func TestDiscover_DefaultLabels(t *testing.T) {
	runner := newFakeRunner()
	runner.setOutput("ps", []byte("ccc999"))
	runner.setOutput("inspect", []byte(`[{
		"Id": "ccc999",
		"Config": {
			"Labels": {"mcp.enable": "true"}
		},
		"NetworkSettings": {"Ports": {}},
		"State": {"Running": true}
	}]`))

	gw := newGatewayWithRunner(runner)
	servers, err := gw.Discover(context.Background())
	if err != nil {
		t.Fatalf("Discover: %v", err)
	}
	if len(servers) != 1 {
		t.Fatalf("Discover returned %d servers, want 1", len(servers))
	}

	s := servers[0]
	if s.Transport != defaultTransport {
		t.Errorf("Transport = %q, want default %q", s.Transport, defaultTransport)
	}
	if s.Path != defaultPath {
		t.Errorf("Path = %q, want default %q", s.Path, defaultPath)
	}
}

func TestDiscover_SkipsStoppedContainers(t *testing.T) {
	runner := newFakeRunner()
	runner.setOutput("ps", []byte("stopped-container"))
	runner.setOutput("inspect", []byte(`[{
		"Id": "stopped-container",
		"Config": {
			"Labels": {"mcp.enable": "true", "mcp.name": "stopped"}
		},
		"NetworkSettings": {"Ports": {}},
		"State": {"Running": false}
	}]`))

	gw := newGatewayWithRunner(runner)
	servers, err := gw.Discover(context.Background())
	if err != nil {
		t.Fatalf("Discover: %v", err)
	}
	if len(servers) != 0 {
		t.Errorf("Discover returned %d servers, want 0 (stopped container)", len(servers))
	}
}

func TestDiscover_PSError(t *testing.T) {
	runner := newFakeRunner()
	runner.setError("ps", errors.New("daemon not running"))
	gw := newGatewayWithRunner(runner)

	_, err := gw.Discover(context.Background())
	if err == nil {
		t.Fatal("expected error when docker ps fails")
	}
}

func TestDiscover_InspectError(t *testing.T) {
	runner := newFakeRunner()
	runner.setOutput("ps", []byte("container1"))
	runner.setError("inspect", errors.New("inspect failed"))
	gw := newGatewayWithRunner(runner)

	_, err := gw.Discover(context.Background())
	if err == nil {
		t.Fatal("expected error when docker inspect fails")
	}
}

func TestDiscover_InspectInvalidJSON(t *testing.T) {
	runner := newFakeRunner()
	runner.setOutput("ps", []byte("container1"))
	runner.setOutput("inspect", []byte("not json"))
	gw := newGatewayWithRunner(runner)

	_, err := gw.Discover(context.Background())
	if err == nil {
		t.Fatal("expected error when inspect returns invalid JSON")
	}
}

func TestDiscover_MultipleServers(t *testing.T) {
	runner := newFakeRunner()
	runner.setOutput("ps", []byte("aaa\nbbb"))
	runner.setOutput("inspect", []byte(`[
		{
			"Id": "aaa",
			"Config": {"Labels": {"mcp.enable": "true", "mcp.name": "server-a"}},
			"NetworkSettings": {"Ports": {}},
			"State": {"Running": true}
		},
		{
			"Id": "bbb",
			"Config": {"Labels": {"mcp.enable": "true", "mcp.name": "server-b"}},
			"NetworkSettings": {"Ports": {}},
			"State": {"Running": true}
		}
	]`))

	gw := newGatewayWithRunner(runner)
	servers, err := gw.Discover(context.Background())
	if err != nil {
		t.Fatalf("Discover: %v", err)
	}
	if len(servers) != 2 {
		t.Errorf("Discover returned %d servers, want 2", len(servers))
	}
}

// ---- Gateway.Servers / ServerByName tests ----

func TestServers_EmptyInitially(t *testing.T) {
	gw := newGatewayWithRunner(newFakeRunner())
	if got := gw.Servers(); len(got) != 0 {
		t.Errorf("Servers() = %d, want 0 before any Discover", len(got))
	}
}

func TestServerByName_Found(t *testing.T) {
	gw := newGatewayWithRunner(newFakeRunner())
	gw.mu.Lock()
	gw.servers["id1"] = &Server{ContainerID: "id1", Name: "my-tool", Transport: TransportHTTP}
	gw.mu.Unlock()

	s := gw.ServerByName("my-tool")
	if s == nil {
		t.Fatal("ServerByName returned nil for known server")
	}
	if s.Name != "my-tool" {
		t.Errorf("Name = %q, want my-tool", s.Name)
	}
}

func TestServerByName_NotFound(t *testing.T) {
	gw := newGatewayWithRunner(newFakeRunner())
	if s := gw.ServerByName("unknown"); s != nil {
		t.Error("ServerByName should return nil for unknown server")
	}
}

// ---- Gateway.ToolEndpoint tests ----

func TestToolEndpoint_Success(t *testing.T) {
	gw := newGatewayWithRunner(newFakeRunner())
	gw.mu.Lock()
	gw.servers["id2"] = &Server{
		ContainerID: "id2",
		Name:        "file-tools",
		Transport:   TransportHTTP,
		Address:     "127.0.0.1:8080",
		Path:        "/mcp",
	}
	gw.mu.Unlock()

	endpoint, err := gw.ToolEndpoint("file-tools")
	if err != nil {
		t.Fatalf("ToolEndpoint: %v", err)
	}
	want := "http://127.0.0.1:8080/mcp"
	if endpoint != want {
		t.Errorf("ToolEndpoint = %q, want %q", endpoint, want)
	}
}

func TestToolEndpoint_ServerNotFound(t *testing.T) {
	gw := newGatewayWithRunner(newFakeRunner())
	_, err := gw.ToolEndpoint("unknown")
	if err == nil {
		t.Fatal("expected error for unknown server")
	}
}

func TestToolEndpoint_NonHTTPTransport(t *testing.T) {
	gw := newGatewayWithRunner(newFakeRunner())
	gw.mu.Lock()
	gw.servers["stdio1"] = &Server{
		ContainerID: "stdio1",
		Name:        "stdio-server",
		Transport:   TransportStdio,
	}
	gw.mu.Unlock()

	_, err := gw.ToolEndpoint("stdio-server")
	if err == nil {
		t.Fatal("expected error for stdio transport")
	}
}

// ---- Gateway.Health tests ----

func TestHealth_DockerReachable(t *testing.T) {
	runner := newFakeRunner()
	runner.setOutput("info", []byte("24.0.0"))
	gw := newGatewayWithRunner(runner)

	if err := gw.Health(context.Background()); err != nil {
		t.Errorf("Health: unexpected error: %v", err)
	}
}

func TestHealth_DockerUnreachable(t *testing.T) {
	runner := newFakeRunner()
	runner.setError("info", errors.New("cannot connect to the Docker daemon"))
	gw := newGatewayWithRunner(runner)

	if err := gw.Health(context.Background()); err == nil {
		t.Fatal("Health should return error when docker info fails")
	}
}

// ---- parseLines tests ----

func TestParseLines(t *testing.T) {
	cases := []struct {
		input []byte
		want  []string
	}{
		{[]byte(""), nil},
		{[]byte("abc"), []string{"abc"}},
		{[]byte("abc\ndef"), []string{"abc", "def"}},
		{[]byte("abc\n\ndef\n"), []string{"abc", "def"}},
		{[]byte("  abc  \n  def  "), []string{"abc", "def"}},
	}
	for _, tc := range cases {
		got := parseLines(tc.input)
		if len(got) != len(tc.want) {
			t.Errorf("parseLines(%q) = %v, want %v", tc.input, got, tc.want)
			continue
		}
		for i := range got {
			if got[i] != tc.want[i] {
				t.Errorf("parseLines(%q)[%d] = %q, want %q", tc.input, i, got[i], tc.want[i])
			}
		}
	}
}

// ---- serverFromInspect tests ----

func TestServerFromInspect_MissingEnableLabel(t *testing.T) {
	c := &containerInspect{}
	c.ID = "nolabel"
	c.Config.Labels = map[string]string{"mcp.name": "test"}
	c.State.Running = true

	if s := serverFromInspect(c); s != nil {
		t.Error("serverFromInspect should return nil when mcp.enable!=true")
	}
}

func TestServerFromInspect_DefaultNameFromID(t *testing.T) {
	c := &containerInspect{}
	c.ID = "abcdef123456789"
	c.Config.Labels = map[string]string{"mcp.enable": "true"}
	c.State.Running = true

	s := serverFromInspect(c)
	if s == nil {
		t.Fatal("serverFromInspect returned nil")
	}
	wantName := "abcdef123456"
	if s.Name != wantName {
		t.Errorf("Name = %q, want first 12 chars of ID %q", s.Name, wantName)
	}
}

func TestResolveHostPort_WithMapping(t *testing.T) {
	c := &containerInspect{}
	c.NetworkSettings.Ports = map[string][]struct {
		HostIP   string `json:"HostIp"`
		HostPort string `json:"HostPort"`
	}{
		"8080/tcp": {{"0.0.0.0", "32768"}},
	}
	got := resolveHostPort(c, "8080")
	if got != "32768" {
		t.Errorf("resolveHostPort = %q, want 32768", got)
	}
}

func TestResolveHostPort_NoMapping(t *testing.T) {
	c := &containerInspect{}
	c.NetworkSettings.Ports = map[string][]struct {
		HostIP   string `json:"HostIp"`
		HostPort string `json:"HostPort"`
	}{}
	got := resolveHostPort(c, "9090")
	if got != "9090" {
		t.Errorf("resolveHostPort = %q, want 9090 (fallback to container port)", got)
	}
}
