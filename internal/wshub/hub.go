package wshub

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"regexp"
	"strings"
	"sync"

	"github.com/ampcode/wmux/internal/policy"
	"github.com/ampcode/wmux/internal/tmuxparse"
	"github.com/gorilla/websocket"
)

type TmuxSender interface {
	Send(line string) error
}

type Hub struct {
	policy  policy.Policy
	tmux    TmuxSender
	parser  *tmuxparse.StreamParser
	model   modelState
	pending []pendingCommand

	mu      sync.RWMutex
	clients map[*client]struct{}
}

type client struct {
	conn      *websocket.Conn
	send      chan serverMsg
	closeOnce sync.Once
}

type clientMsg struct {
	T    string   `json:"t"`
	Argv []string `json:"argv"`
}

type serverMsg struct {
	T            string               `json:"t"`
	Message      string               `json:"message,omitempty"`
	Command      *commandPayload      `json:"command,omitempty"`
	Notification *notificationPayload `json:"notification,omitempty"`
	PaneOutput   *paneOutputPayload   `json:"pane_output,omitempty"`
	PaneSnapshot *paneSnapshotPayload `json:"pane_snapshot,omitempty"`
	State        *statePayload        `json:"state,omitempty"`
}

type commandPayload struct {
	EpochSeconds int64    `json:"epoch_seconds"`
	CommandID    int64    `json:"command_id"`
	Flags        int64    `json:"flags"`
	Success      bool     `json:"success"`
	Output       []string `json:"output"`
}

type notificationPayload struct {
	Name  string   `json:"name"`
	Args  []string `json:"args,omitempty"`
	Text  string   `json:"text,omitempty"`
	Value string   `json:"value,omitempty"`
}

type paneOutputPayload struct {
	PaneID string `json:"pane_id"`
	Data   string `json:"data"`
}

type paneSnapshotPayload struct {
	PaneID string `json:"pane_id"`
	Data   string `json:"data"`
}

type pendingCommand struct {
	Name       string
	TargetPane string
}

var upgrader = websocket.Upgrader{
	CheckOrigin: func(_ *http.Request) bool { return true },
}

var safeBareToken = regexp.MustCompile(`^[A-Za-z0-9_@%:./+\-]+$`)

func New(p policy.Policy) *Hub {
	h := &Hub{
		policy:  p,
		clients: map[*client]struct{}{},
		model:   newModelState(),
		pending: []pendingCommand{},
	}
	h.resetParser()
	return h
}

func (h *Hub) BindTmux(tmux TmuxSender) error {
	h.tmux = tmux
	return nil
}

func (h *Hub) HandleWS(w http.ResponseWriter, r *http.Request) {
	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		log.Printf("ws upgrade failed: %v", err)
		return
	}

	c := &client{conn: conn, send: make(chan serverMsg, 256)}
	h.addClient(c)
	defer h.removeClient(c)

	go c.writeLoop()
	c.readLoop(h)
}

func (h *Hub) BroadcastTmuxStdoutLine(line string) {
	h.mu.RLock()
	parser := h.parser
	h.mu.RUnlock()
	if parser != nil {
		parser.FeedLine(line)
	}
}

func (h *Hub) BroadcastTmuxStderrLine(line string) {
	h.broadcast(serverMsg{T: "error", Message: "tmux stderr: " + line})
}

func (h *Hub) BroadcastRestart() {
	h.resetParser()
	h.mu.Lock()
	h.model.reset()
	h.pending = h.pending[:0]
	snapshot := h.model.snapshot()
	h.mu.Unlock()
	h.broadcast(serverMsg{T: "tmux_state", State: &snapshot})
	h.broadcast(serverMsg{T: "tmux_restarted"})
}

func (h *Hub) resetParser() {
	h.mu.Lock()
	old := h.parser
	h.parser = tmuxparse.NewStreamParser(512)
	newParser := h.parser
	h.mu.Unlock()

	if old != nil {
		old.Close()
	}
	go h.consumeParserEvents(newParser)
}

func (h *Hub) consumeParserEvents(parser *tmuxparse.StreamParser) {
	for ev := range parser.Events() {
		switch e := ev.(type) {
		case tmuxparse.Command:
			pending := h.shiftPending()
			var state *statePayload
			h.mu.Lock()
			if h.model.applyOutputLines(e.Output) {
				snapshot := h.model.snapshot()
				state = &snapshot
			}
			h.mu.Unlock()

			h.broadcast(serverMsg{T: "tmux_command", Command: &commandPayload{
				EpochSeconds: e.Header.EpochSeconds,
				CommandID:    e.Header.CommandID,
				Flags:        e.Header.Flags,
				Success:      e.Success,
				Output:       append([]string(nil), e.Output...),
			}})
			if state != nil {
				h.broadcast(serverMsg{T: "tmux_state", State: state})
			}
			if pending.Name == "capture-pane" && pending.TargetPane != "" {
				h.broadcast(serverMsg{T: "pane_snapshot", PaneSnapshot: &paneSnapshotPayload{
					PaneID: pending.TargetPane,
					Data:   strings.Join(e.Output, "\n"),
				}})
			}
		case tmuxparse.Notification:
			if (e.Name == "output" || e.Name == "extended-output") && len(e.Args) >= 1 {
				h.broadcast(serverMsg{T: "pane_output", PaneOutput: &paneOutputPayload{
					PaneID: e.Args[0],
					Data:   tmuxparse.DecodeEscapedValue(e.Value),
				}})
				continue
			}
			h.broadcast(serverMsg{T: "tmux_notification", Notification: &notificationPayload{
				Name:  e.Name,
				Args:  append([]string(nil), e.Args...),
				Text:  e.Text,
				Value: e.Value,
			}})
		case tmuxparse.ParseError:
			h.broadcast(serverMsg{T: "error", Message: "tmux parse error: " + e.Error()})
		}
	}
}

func (h *Hub) addClient(c *client) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.clients[c] = struct{}{}
}

func (h *Hub) removeClient(c *client) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if _, ok := h.clients[c]; !ok {
		return
	}
	delete(h.clients, c)
	c.close()
}

func (h *Hub) broadcast(m serverMsg) {
	h.mu.RLock()
	defer h.mu.RUnlock()
	for c := range h.clients {
		select {
		case c.send <- m:
		default:
			go h.removeClient(c)
		}
	}
}

func (c *client) readLoop(h *Hub) {
	for {
		_, data, err := c.conn.ReadMessage()
		if err != nil {
			return
		}
		var msg clientMsg
		if err := json.Unmarshal(data, &msg); err != nil {
			c.enqueue(serverMsg{T: "error", Message: "invalid JSON"})
			continue
		}
		if msg.T != "cmd" {
			c.enqueue(serverMsg{T: "error", Message: "unsupported message type"})
			continue
		}
		line, err := encodeArgvCommand(msg.Argv)
		if err != nil {
			c.enqueue(serverMsg{T: "error", Message: err.Error()})
			continue
		}
		if err := h.policy.Validate(line); err != nil {
			c.enqueue(serverMsg{T: "error", Message: err.Error()})
			continue
		}
		if h.tmux == nil {
			c.enqueue(serverMsg{T: "error", Message: "tmux backend unavailable"})
			continue
		}
		if err := h.tmux.Send(line); err != nil {
			c.enqueue(serverMsg{T: "error", Message: err.Error()})
			continue
		}
		h.registerPending(msg.Argv)
	}
}

func (h *Hub) registerPending(argv []string) {
	if len(argv) == 0 {
		return
	}
	p := pendingCommand{Name: strings.ToLower(strings.TrimSpace(argv[0]))}
	for i := 1; i < len(argv)-1; i++ {
		if argv[i] == "-t" {
			p.TargetPane = argv[i+1]
			break
		}
	}
	h.mu.Lock()
	h.pending = append(h.pending, p)
	h.mu.Unlock()
}

func (h *Hub) shiftPending() pendingCommand {
	h.mu.Lock()
	defer h.mu.Unlock()
	if len(h.pending) == 0 {
		return pendingCommand{}
	}
	p := h.pending[0]
	h.pending = h.pending[1:]
	return p
}

func encodeArgvCommand(argv []string) (string, error) {
	if len(argv) == 0 {
		return "", fmt.Errorf("argv cannot be empty")
	}
	cmd := strings.ToLower(strings.TrimSpace(argv[0]))
	if !safeBareToken.MatchString(cmd) {
		return "", fmt.Errorf("invalid command name")
	}

	parts := make([]string, 0, len(argv))
	parts = append(parts, cmd)
	for _, arg := range argv[1:] {
		parts = append(parts, quoteArg(arg))
	}
	return strings.Join(parts, " "), nil
}

func quoteArg(arg string) string {
	if arg == "" {
		return "''"
	}
	if safeBareToken.MatchString(arg) {
		return arg
	}
	// Use single-quoted strings and escape a single quote as '\''.
	return "'" + strings.ReplaceAll(arg, "'", "'\\''") + "'"
}

func (c *client) writeLoop() {
	for msg := range c.send {
		if err := c.conn.WriteJSON(msg); err != nil {
			return
		}
	}
}

func (c *client) enqueue(msg serverMsg) {
	defer func() {
		_ = recover()
	}()
	select {
	case c.send <- msg:
	default:
	}
}

func (c *client) close() {
	c.closeOnce.Do(func() {
		close(c.send)
		_ = c.conn.Close()
	})
}
