//go:build windows

package service

import (
	"context"
	"fmt"
	"time"

	"golang.org/x/sys/windows/svc"
	"golang.org/x/sys/windows/svc/mgr"

	"github.com/ryolambert/openkiro/internal/proxy"
)

// Name is the Windows Service name.
const Name = "openkiro"

type handler struct{ port string }

func (h *handler) Execute(_ []string, r <-chan svc.ChangeRequest, s chan<- svc.Status) (bool, uint32) {
	s <- svc.Status{State: svc.StartPending}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		proxy.StartServer(ctx, proxy.DefaultListenAddress, h.port)
	}()

	s <- svc.Status{State: svc.Running, Accepts: svc.AcceptStop | svc.AcceptShutdown}
	for c := range r {
		switch c.Cmd {
		case svc.Stop, svc.Shutdown:
			s <- svc.Status{State: svc.StopPending}
			cancel()
			select {
			case <-done:
			case <-time.After(10 * time.Second):
			}
			return false, 0
		}
	}
	cancel()
	return false, 0
}

// RunService runs the binary as a Windows Service (called by SCM).
func RunService(port string) error {
	return svc.Run(Name, &handler{port: port})
}

// IsWindowsService reports whether the process is running as a Windows Service.
func IsWindowsService() (bool, error) {
	return svc.IsWindowsService()
}

// Install installs and starts the service via SCM.
func Install(binPath, port string) error {
	m, err := mgr.Connect()
	if err != nil {
		return fmt.Errorf("connect SCM: %w", err)
	}
	defer m.Disconnect()

	if s, err := m.OpenService(Name); err == nil {
		s.Close()
		return fmt.Errorf("service %s already exists", Name)
	}

	s, err := m.CreateService(Name, binPath, mgr.Config{
		DisplayName: "openkiro proxy",
		StartType:   mgr.StartAutomatic,
	}, "server", "--port", port)
	if err != nil {
		return fmt.Errorf("create service: %w", err)
	}
	defer s.Close()
	return s.Start()
}

// Uninstall stops and removes the service from SCM.
func Uninstall() error {
	m, err := mgr.Connect()
	if err != nil {
		return fmt.Errorf("connect SCM: %w", err)
	}
	defer m.Disconnect()

	s, err := m.OpenService(Name)
	if err != nil {
		return fmt.Errorf("open service: %w", err)
	}
	defer s.Close()

	_, _ = s.Control(svc.Stop)
	time.Sleep(500 * time.Millisecond)
	return s.Delete()
}

// QueryStatus returns a human-readable service status string.
func QueryStatus() (string, error) {
	m, err := mgr.Connect()
	if err != nil {
		return "", fmt.Errorf("connect SCM: %w", err)
	}
	defer m.Disconnect()

	s, err := m.OpenService(Name)
	if err != nil {
		return "not installed", nil
	}
	defer s.Close()

	q, err := s.Query()
	if err != nil {
		return "", fmt.Errorf("query service: %w", err)
	}
	switch q.State {
	case svc.Running:
		return "running", nil
	case svc.Stopped:
		return "stopped", nil
	case svc.StartPending:
		return "starting", nil
	case svc.StopPending:
		return "stopping", nil
	default:
		return fmt.Sprintf("state=%d", q.State), nil
	}
}
