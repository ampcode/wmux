package tmuxproc

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"log"
	"os/exec"
	"strings"
	"sync"
	"time"

	"github.com/creack/pty"
)

type SocketTarget struct {
	Name string
	Path string
}

func (s SocketTarget) Validate() error {
	if strings.TrimSpace(s.Name) != "" && strings.TrimSpace(s.Path) != "" {
		return fmt.Errorf("tmux socket name and path are mutually exclusive")
	}
	return nil
}

func (s SocketTarget) Args() []string {
	name := strings.TrimSpace(s.Name)
	if name != "" {
		return []string{"-L", name}
	}
	path := strings.TrimSpace(s.Path)
	if path != "" {
		return []string{"-S", path}
	}
	return nil
}

type Config struct {
	TmuxBin           string
	TargetSession     string
	Socket            SocketTarget
	AutoCreateSession bool
	BackoffBase       time.Duration
	BackoffMax        time.Duration
	OnStdoutLine      func(string)
	OnStderrLine      func(string)
	OnConnected       func()
	OnDisconnect      func(error)
}

type Manager struct {
	cfg Config

	mu      sync.Mutex
	stdin   io.WriteCloser
	running bool
	lastErr error
}

func NewManager(cfg Config) *Manager {
	if cfg.BackoffBase <= 0 {
		cfg.BackoffBase = 500 * time.Millisecond
	}
	if cfg.BackoffMax < cfg.BackoffBase {
		cfg.BackoffMax = 10 * time.Second
	}
	return &Manager{cfg: cfg}
}

func buildTmuxArgs(socket SocketTarget, argv ...string) []string {
	args := socket.Args()
	if len(args) == 0 {
		return append([]string(nil), argv...)
	}
	full := make([]string, 0, len(args)+len(argv))
	full = append(full, args...)
	full = append(full, argv...)
	return full
}

func command(tmuxBin string, socket SocketTarget, argv ...string) *exec.Cmd {
	return exec.Command(tmuxBin, buildTmuxArgs(socket, argv...)...)
}

func commandContext(ctx context.Context, tmuxBin string, socket SocketTarget, argv ...string) *exec.Cmd {
	return exec.CommandContext(ctx, tmuxBin, buildTmuxArgs(socket, argv...)...)
}

func CheckTmux(tmuxBin string, socket SocketTarget) error {
	cmd := command(tmuxBin, socket, "-V")
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("tmux sanity check failed: %w (%s)", err, string(out))
	}
	return nil
}

func EnsureSession(tmuxBin string, socket SocketTarget, name string) error {
	check := command(tmuxBin, socket, "has-session", "-t", name)
	if err := check.Run(); err == nil {
		return nil
	}
	create := command(tmuxBin, socket, "new-session", "-d", "-s", name)
	if out, err := create.CombinedOutput(); err != nil {
		return fmt.Errorf("create session %q: %w (%s)", name, err, string(out))
	}
	return nil
}

func (m *Manager) Send(line string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if !m.running || m.stdin == nil {
		if m.lastErr != nil {
			return fmt.Errorf("tmux unavailable: %w", m.lastErr)
		}
		return fmt.Errorf("tmux control mode not ready")
	}
	_, err := io.WriteString(m.stdin, line+"\n")
	return err
}

func (m *Manager) Run(ctx context.Context) {
	backoff := m.cfg.BackoffBase
	for {
		if ctx.Err() != nil {
			return
		}

		if err := m.checkTargetSession(ctx); err != nil {
			if ctx.Err() != nil {
				return
			}
			log.Printf("tmux target unavailable: %v", err)
			m.markDisconnected(err)
			select {
			case <-ctx.Done():
				return
			case <-time.After(backoff):
			}
			if backoff < m.cfg.BackoffMax {
				backoff *= 2
				if backoff > m.cfg.BackoffMax {
					backoff = m.cfg.BackoffMax
				}
			}
			continue
		}

		err := m.runOnce(ctx)
		if ctx.Err() != nil {
			return
		}
		log.Printf("tmux control client exited: %v", err)
		m.markDisconnected(err)
		backoff = m.cfg.BackoffBase

		select {
		case <-ctx.Done():
			return
		case <-time.After(backoff):
		}
		if backoff < m.cfg.BackoffMax {
			backoff *= 2
			if backoff > m.cfg.BackoffMax {
				backoff = m.cfg.BackoffMax
			}
		}
	}
}

func (m *Manager) checkTargetSession(ctx context.Context) error {
	cmd := commandContext(ctx, m.cfg.TmuxBin, m.cfg.Socket, "has-session", "-t", m.cfg.TargetSession)
	out, err := cmd.CombinedOutput()
	if err == nil {
		return nil
	}
	if m.cfg.AutoCreateSession {
		if ensureErr := EnsureSession(m.cfg.TmuxBin, m.cfg.Socket, m.cfg.TargetSession); ensureErr == nil {
			return nil
		}
	}
	msg := strings.TrimSpace(string(out))
	if msg != "" {
		return fmt.Errorf("target session %q unavailable: %s", m.cfg.TargetSession, msg)
	}
	return fmt.Errorf("target session %q unavailable: %w", m.cfg.TargetSession, err)
}

func (m *Manager) runOnce(ctx context.Context) error {
	cmd := commandContext(ctx, m.cfg.TmuxBin, m.cfg.Socket, "-CC", "attach-session", "-t", m.cfg.TargetSession)
	ptmx, err := pty.Start(cmd)
	if err != nil {
		return err
	}
	defer func() {
		_ = ptmx.Close()
	}()

	log.Printf("tmux control client started")

	m.mu.Lock()
	m.stdin = ptmx
	m.running = true
	m.lastErr = nil
	m.mu.Unlock()
	if m.cfg.OnConnected != nil {
		m.cfg.OnConnected()
	}

	errCh := make(chan error, 1)
	go m.readLines(ptmx, errCh, m.cfg.OnStdoutLine)

	waitErr := make(chan error, 1)
	go func() {
		waitErr <- cmd.Wait()
	}()

	var result error
	select {
	case <-ctx.Done():
		_ = ptmx.Close()
		result = ctx.Err()
	case err := <-errCh:
		result = err
	case err := <-waitErr:
		result = err
	}

	m.mu.Lock()
	m.running = false
	m.stdin = nil
	m.mu.Unlock()
	return result
}

func (m *Manager) markDisconnected(err error) {
	m.mu.Lock()
	changed := m.running || m.stdin != nil || !sameError(m.lastErr, err)
	m.running = false
	m.stdin = nil
	m.lastErr = err
	m.mu.Unlock()
	if changed && m.cfg.OnDisconnect != nil {
		m.cfg.OnDisconnect(err)
	}
}

func sameError(a, b error) bool {
	if a == nil || b == nil {
		return a == b
	}
	return strings.TrimSpace(a.Error()) == strings.TrimSpace(b.Error())
}

func (m *Manager) readLines(r io.Reader, errCh chan<- error, onLine func(string)) {
	s := bufio.NewScanner(r)
	buf := make([]byte, 0, 64*1024)
	s.Buffer(buf, 1024*1024)
	for s.Scan() {
		if onLine != nil {
			onLine(strings.TrimSuffix(s.Text(), "\r"))
		}
	}
	if err := s.Err(); err != nil {
		errCh <- err
	}
}
