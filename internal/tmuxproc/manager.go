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

type Config struct {
	TmuxBin       string
	TargetSession string
	BackoffBase   time.Duration
	BackoffMax    time.Duration
	OnStdoutLine  func(string)
	OnStderrLine  func(string)
	OnRestart     func()
}

type Manager struct {
	cfg Config

	mu      sync.Mutex
	stdin   io.WriteCloser
	running bool
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

func CheckTmux(tmuxBin string) error {
	cmd := exec.Command(tmuxBin, "-V")
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("tmux sanity check failed: %w (%s)", err, string(out))
	}
	return nil
}

func EnsureSession(tmuxBin, name string) error {
	check := exec.Command(tmuxBin, "has-session", "-t", name)
	if err := check.Run(); err == nil {
		return nil
	}
	create := exec.Command(tmuxBin, "new-session", "-d", "-s", name)
	if out, err := create.CombinedOutput(); err != nil {
		return fmt.Errorf("create session %q: %w (%s)", name, err, string(out))
	}
	return nil
}

func (m *Manager) Send(line string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if !m.running || m.stdin == nil {
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

		err := m.runOnce(ctx)
		if ctx.Err() != nil {
			return
		}
		log.Printf("tmux control client exited: %v", err)
		if m.cfg.OnRestart != nil {
			m.cfg.OnRestart()
		}

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

func (m *Manager) runOnce(ctx context.Context) error {
	cmd := exec.CommandContext(ctx, m.cfg.TmuxBin, "-CC", "attach-session", "-t", m.cfg.TargetSession)
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
	m.mu.Unlock()

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
