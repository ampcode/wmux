package wshub

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/ampcode/wmux/internal/policy"
	"github.com/ampcode/wmux/internal/tmuxparse"
	"github.com/gorilla/websocket"
)

type TmuxSender interface {
	Send(line string) error
}

type Hub struct {
	policy        policy.Policy
	tmux          TmuxSender
	parser        *tmuxparse.StreamParser
	model         modelState
	pending       []pendingCommand
	targetSession string

	mu      sync.RWMutex
	clients map[*client]struct{}
}

type PaneInfo struct {
	PaneID      string `json:"pane_id"`
	PaneIndex   int    `json:"pane_index"`
	Name        string `json:"name"`
	SessionName string `json:"session_name"`
	Width       int    `json:"width"`
	Height      int    `json:"height"`
	TmuxPaneID  string `json:"-"`
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
	PaneCursor   *paneCursorPayload   `json:"pane_cursor,omitempty"`
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

type paneCursorPayload struct {
	PaneID string `json:"pane_id"`
	X      int    `json:"x"`
	Y      int    `json:"y"`
}

type pendingCommand struct {
	Name             string
	TargetPane       string
	EmitPaneSnapshot bool
	Wait             chan commandResult
}

type commandResult struct {
	Success bool
	Output  []string
}

var upgrader = websocket.Upgrader{
	CheckOrigin: func(_ *http.Request) bool { return true },
}

var safeBareToken = regexp.MustCompile(`^[A-Za-z0-9_@%:./+\-]+$`)

func New(p policy.Policy, targetSession string) *Hub {
	h := &Hub{
		policy:        p,
		clients:       map[*client]struct{}{},
		model:         newModelState(),
		pending:       []pendingCommand{},
		targetSession: targetSession,
	}
	h.resetParser()
	return h
}

func (h *Hub) BindTmux(tmux TmuxSender) error {
	h.tmux = tmux
	return nil
}

func (h *Hub) RequestStateSync() error {
	argv := []string{"list-panes", "-a", "-F", "__WMUX___pane\t#{session_name}\t#{pane_id}\t#{window_id}\t#{pane_index}\t#{pane_active}\t#{pane_left}\t#{pane_top}\t#{pane_width}\t#{pane_height}\t#{pane_current_command}\t#{pane_title}"}
	line, err := encodeArgvCommand(argv)
	if err != nil {
		return err
	}
	if h.tmux == nil {
		return fmt.Errorf("tmux backend unavailable")
	}
	if err := h.tmux.Send(line); err != nil {
		return err
	}
	h.registerPending(argv)
	return nil
}

func (h *Hub) RequestStateSyncWithRetry() {
	for i := 0; i < 10; i++ {
		if err := h.RequestStateSync(); err == nil {
			return
		}
		time.Sleep(300 * time.Millisecond)
	}
}

func (h *Hub) CurrentState() statePayload {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return filterStateToTargetSession(h.model.snapshot(), h.targetSession)
}

func (h *Hub) CurrentTargetSessionPanes() []panePayload {
	return h.CurrentState().Panes
}

func filterStateToTargetSession(state statePayload, targetSession string) statePayload {
	if targetSession == "" {
		return state
	}

	filteredPanes := make([]panePayload, 0, len(state.Panes))
	windowIDs := make(map[string]struct{}, len(state.Panes))
	for _, pane := range state.Panes {
		if pane.SessionName != targetSession {
			continue
		}
		filteredPanes = append(filteredPanes, pane)
		windowIDs[pane.WindowID] = struct{}{}
	}

	filteredWindows := make([]windowPayload, 0, len(state.Windows))
	for _, window := range state.Windows {
		if _, ok := windowIDs[window.ID]; ok {
			filteredWindows = append(filteredWindows, window)
		}
	}

	return statePayload{Windows: filteredWindows, Panes: filteredPanes}
}

func (h *Hub) CurrentTargetSessionPaneInfos() []PaneInfo {
	panes := h.CurrentTargetSessionPanes()
	out := make([]PaneInfo, 0, len(panes))
	for _, pane := range panes {
		out = append(out, PaneInfo{
			PaneID:      publicPaneID(pane.ID),
			PaneIndex:   pane.PaneIndex,
			TmuxPaneID:  pane.ID,
			Name:        pane.Name,
			SessionName: pane.SessionName,
			Width:       pane.Width,
			Height:      pane.Height,
		})
	}
	return out
}

func (h *Hub) TargetSessionPaneIDByPublicID(paneID string) (string, bool) {
	normalized := publicPaneID(paneID)
	if normalized == "" {
		return "", false
	}
	for _, pane := range h.CurrentTargetSessionPaneInfos() {
		if pane.PaneID == normalized {
			return pane.TmuxPaneID, true
		}
	}
	return "", false
}

func publicPaneID(tmuxPaneID string) string {
	return strings.TrimPrefix(strings.TrimSpace(tmuxPaneID), "%")
}

func (h *Hub) CapturePaneContent(paneID string, withEscapes bool) (string, error) {
	paneID = strings.TrimSpace(paneID)
	if paneID == "" {
		return "", fmt.Errorf("pane id is required")
	}

	argv := []string{"capture-pane", "-p", "-N", "-t", paneID}
	if withEscapes {
		argv = []string{"capture-pane", "-p", "-e", "-N", "-t", paneID}
	}

	res, err := h.runCommandAndWait(argv, 5*time.Second, false)
	if err != nil {
		return "", err
	}
	if !res.Success {
		if withEscapes {
			return "", fmt.Errorf("capture-pane with escapes failed")
		}
		return "", fmt.Errorf("capture-pane without escapes failed")
	}

	return strings.Join(res.Output, "\n"), nil
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
	c.enqueue(serverMsg{T: "tmux_state", State: statePointer(h.CurrentState())})

	go c.writeLoop()
	c.readLoop(h)
}

func statePointer(s statePayload) *statePayload {
	return &s
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
	go h.RequestStateSyncWithRetry()
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
			if pending.Wait != nil {
				select {
				case pending.Wait <- commandResult{Success: e.Success, Output: append([]string(nil), e.Output...)}:
				default:
				}
			}
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
			if pending.Name == "capture-pane" && pending.TargetPane != "" && pending.EmitPaneSnapshot {
				h.broadcast(serverMsg{T: "pane_snapshot", PaneSnapshot: &paneSnapshotPayload{
					PaneID: pending.TargetPane,
					Data:   strings.Join(e.Output, "\n"),
				}})
			}
			if pending.Name == "display-message" && pending.TargetPane != "" {
				if cursor, ok := parsePaneCursorOutput(e.Output); ok {
					cursor.PaneID = pending.TargetPane
					h.broadcast(serverMsg{T: "pane_cursor", PaneCursor: cursor})
				}
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
	p := pendingFromArgv(argv)
	h.mu.Lock()
	h.pending = append(h.pending, p)
	h.mu.Unlock()
}

func pendingFromArgv(argv []string) pendingCommand {
	p := pendingCommand{Name: strings.ToLower(strings.TrimSpace(argv[0]))}
	for i := 1; i < len(argv)-1; i++ {
		if argv[i] == "-t" {
			p.TargetPane = argv[i+1]
			break
		}
	}
	if p.Name == "capture-pane" && p.TargetPane != "" {
		p.EmitPaneSnapshot = true
	}
	return p
}

func (h *Hub) runCommandAndWait(argv []string, timeout time.Duration, emitPaneSnapshot bool) (commandResult, error) {
	if len(argv) == 0 {
		return commandResult{}, fmt.Errorf("argv cannot be empty")
	}
	line, err := encodeArgvCommand(argv)
	if err != nil {
		return commandResult{}, err
	}

	done := make(chan commandResult, 1)
	pending := pendingFromArgv(argv)
	pending.Wait = done
	pending.EmitPaneSnapshot = emitPaneSnapshot

	h.mu.Lock()
	h.pending = append(h.pending, pending)
	h.mu.Unlock()

	if h.tmux == nil {
		h.removePending(done)
		return commandResult{}, fmt.Errorf("tmux backend unavailable")
	}
	if err := h.tmux.Send(line); err != nil {
		h.removePending(done)
		return commandResult{}, err
	}

	select {
	case res := <-done:
		return res, nil
	case <-time.After(timeout):
		return commandResult{}, fmt.Errorf("timed out waiting for tmux response")
	}
}

func (h *Hub) removePending(done chan commandResult) {
	h.mu.Lock()
	defer h.mu.Unlock()
	for i := range h.pending {
		if h.pending[i].Wait == done {
			h.pending = append(h.pending[:i], h.pending[i+1:]...)
			return
		}
	}
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

func parsePaneCursorOutput(lines []string) (*paneCursorPayload, bool) {
	if len(lines) == 0 {
		return nil, false
	}
	line := strings.TrimSpace(lines[0])
	parts := strings.Split(line, "\t")
	if len(parts) != 3 || parts[0] != "__WMUX_CURSOR" {
		return nil, false
	}
	x, err := strconv.Atoi(parts[1])
	if err != nil {
		return nil, false
	}
	y, err := strconv.Atoi(parts[2])
	if err != nil {
		return nil, false
	}
	return &paneCursorPayload{X: x, Y: y}, true
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
