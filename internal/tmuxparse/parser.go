package tmuxparse

import (
	"fmt"
	"strconv"
	"strings"
)

type Callbacks struct {
	OnCommandBegin func(BlockHeader)
	OnCommandLine  func(BlockHeader, string)
	OnCommandEnd   func(BlockHeader, BlockHeader, bool)
	OnNotification func(Notification)
	OnError        func(ParseError)
}

// Parser is the low-level control-mode parser. Call FeedLine once per tmux
// protocol line.
type Parser struct {
	cb      Callbacks
	current *BlockHeader
}

func NewParser(cb Callbacks) *Parser {
	return &Parser{cb: cb}
}

func (p *Parser) FeedLine(line string) {
	if p.current != nil {
		if hdr, ok, success, err := parseEndLine(line); ok {
			if err != nil {
				p.emitError(ParseError{Line: line, Message: err.Error()})
				return
			}
			p.finishBlock(*hdr, success, line)
			return
		}
		if malformedControlBoundary(line) {
			p.emitError(ParseError{Line: line, Message: "malformed control boundary"})
			return
		}
		if p.cb.OnCommandLine != nil {
			p.cb.OnCommandLine(*p.current, line)
		}
		return
	}

	if hdr, ok, err := parseBeginLine(line); ok {
		if err != nil {
			p.emitError(ParseError{Line: line, Message: err.Error()})
			return
		}
		p.current = hdr
		if p.cb.OnCommandBegin != nil {
			p.cb.OnCommandBegin(*hdr)
		}
		return
	}

	if _, ok, _, err := parseEndLine(line); ok {
		if err != nil {
			p.emitError(ParseError{Line: line, Message: err.Error()})
			return
		}
		p.emitError(ParseError{Line: line, Message: "end/error without begin"})
		return
	}

	if strings.HasPrefix(line, "%") {
		n, err := parseNotification(line)
		if err != nil {
			p.emitError(ParseError{Line: line, Message: err.Error()})
			return
		}
		if p.cb.OnNotification != nil {
			p.cb.OnNotification(n)
		}
		return
	}

	p.emitError(ParseError{Line: line, Message: "unexpected line outside command block"})
}

// Finish flushes parser state. Call when input stream ends.
func (p *Parser) Finish() {
	if p.current != nil {
		p.emitError(ParseError{Message: "unterminated command block at end of stream"})
		p.current = nil
	}
}

func (p *Parser) finishBlock(end BlockHeader, success bool, raw string) {
	if p.current == nil {
		p.emitError(ParseError{Line: raw, Message: "internal: missing active block"})
		return
	}
	begin := *p.current
	p.current = nil

	if begin != end {
		p.emitError(ParseError{Line: raw, Message: fmt.Sprintf("mismatched block header begin=%+v end=%+v", begin, end)})
	}

	if p.cb.OnCommandEnd != nil {
		p.cb.OnCommandEnd(begin, end, success)
	}
}

func (p *Parser) emitError(err ParseError) {
	if p.cb.OnError != nil {
		p.cb.OnError(err)
	}
}

func parseBeginLine(line string) (*BlockHeader, bool, error) {
	if !strings.HasPrefix(line, "%begin") {
		return nil, false, nil
	}
	if !strings.HasPrefix(line, "%begin ") {
		return nil, true, fmt.Errorf("invalid %%begin line")
	}
	h, err := parseHeader(strings.TrimPrefix(line, "%begin "))
	if err != nil {
		return nil, true, fmt.Errorf("invalid %%begin header: %w", err)
	}
	return &h, true, nil
}

func parseEndLine(line string) (*BlockHeader, bool, bool, error) {
	if strings.HasPrefix(line, "%end") {
		if !strings.HasPrefix(line, "%end ") {
			return nil, true, false, fmt.Errorf("invalid %%end line")
		}
		h, err := parseHeader(strings.TrimPrefix(line, "%end "))
		if err != nil {
			return nil, true, false, fmt.Errorf("invalid %%end header: %w", err)
		}
		return &h, true, true, nil
	}
	if strings.HasPrefix(line, "%error") {
		if !strings.HasPrefix(line, "%error ") {
			return nil, true, false, fmt.Errorf("invalid %%error line")
		}
		h, err := parseHeader(strings.TrimPrefix(line, "%error "))
		if err != nil {
			return nil, true, false, fmt.Errorf("invalid %%error header: %w", err)
		}
		return &h, true, false, nil
	}
	return nil, false, false, nil
}

func parseHeader(raw string) (BlockHeader, error) {
	parts := strings.Fields(raw)
	if len(parts) != 3 {
		return BlockHeader{}, fmt.Errorf("invalid block header")
	}
	epoch, err := strconv.ParseInt(parts[0], 10, 64)
	if err != nil {
		return BlockHeader{}, err
	}
	commandID, err := strconv.ParseInt(parts[1], 10, 64)
	if err != nil {
		return BlockHeader{}, err
	}
	flags, err := strconv.ParseInt(parts[2], 10, 64)
	if err != nil {
		return BlockHeader{}, err
	}
	return BlockHeader{EpochSeconds: epoch, CommandID: commandID, Flags: flags}, nil
}

func parseNotification(line string) (Notification, error) {
	if line == "" || line[0] != '%' {
		return Notification{}, fmt.Errorf("not a notification")
	}

	name, rest := splitNameAndRest(strings.TrimPrefix(line, "%"))
	n := Notification{Name: name, Raw: line}

	switch name {
	case "output":
		paneID, value := splitFirstTokenPreserve(rest)
		if paneID == "" {
			return Notification{}, fmt.Errorf("output missing pane id")
		}
		n.Args = []string{paneID}
		n.Value = value
	case "extended-output":
		base, value := splitByColon(rest)
		fields := strings.Fields(base)
		if len(fields) < 2 {
			return Notification{}, fmt.Errorf("extended-output missing required fields")
		}
		n.Args = fields
		n.Value = value
	case "subscription-changed":
		base, value := splitByColon(rest)
		fields := strings.Fields(base)
		if len(fields) < 5 {
			return Notification{}, fmt.Errorf("subscription-changed missing required fields")
		}
		n.Args = fields
		n.Value = value
	case "message", "config-error", "session-renamed":
		n.Text = strings.TrimSpace(rest)
	case "exit":
		n.Text = strings.TrimSpace(rest)
	case "client-session-changed":
		a, b, tail := takeTwoAndTail(rest)
		if a == "" || b == "" {
			return Notification{}, fmt.Errorf("client-session-changed missing required fields")
		}
		n.Args = []string{a, b}
		n.Text = tail
	case "session-changed":
		a, tail := splitOnce(strings.TrimSpace(rest), ' ')
		if a == "" {
			return Notification{}, fmt.Errorf("session-changed missing required fields")
		}
		n.Args = []string{a}
		n.Text = tail
	case "window-renamed":
		a, tail := splitOnce(strings.TrimSpace(rest), ' ')
		if a == "" {
			return Notification{}, fmt.Errorf("window-renamed missing required fields")
		}
		n.Args = []string{a}
		n.Text = tail
	default:
		n.Args = strings.Fields(rest)
	}

	return n, nil
}

func splitOnce(s string, sep rune) (string, string) {
	s = strings.TrimSpace(s)
	if s == "" {
		return "", ""
	}
	i := strings.IndexRune(s, sep)
	if i == -1 {
		return s, ""
	}
	return s[:i], strings.TrimSpace(s[i+1:])
}

func splitNameAndRest(s string) (string, string) {
	if s == "" {
		return "", ""
	}
	i := strings.IndexByte(s, ' ')
	if i == -1 {
		return s, ""
	}
	return s[:i], s[i+1:]
}

func splitFirstTokenPreserve(s string) (string, string) {
	s = strings.TrimLeft(s, " ")
	if s == "" {
		return "", ""
	}
	i := strings.IndexByte(s, ' ')
	if i == -1 {
		return s, ""
	}
	return s[:i], s[i+1:]
}

func splitByColon(s string) (string, string) {
	for i := 0; i < len(s); i++ {
		if s[i] != ':' {
			continue
		}
		left := strings.TrimSpace(s[:i])
		right := strings.TrimLeft(s[i+1:], " ")
		return left, right
	}
	return strings.TrimSpace(s), ""
}

func takeTwoAndTail(s string) (string, string, string) {
	a, rest := splitOnce(strings.TrimSpace(s), ' ')
	b, tail := splitOnce(strings.TrimSpace(rest), ' ')
	return a, b, tail
}

func malformedControlBoundary(line string) bool {
	if strings.HasPrefix(line, "%begin") && !strings.HasPrefix(line, "%begin ") {
		return true
	}
	if strings.HasPrefix(line, "%end") && !strings.HasPrefix(line, "%end ") {
		return true
	}
	if strings.HasPrefix(line, "%error") && !strings.HasPrefix(line, "%error ") {
		return true
	}
	return false
}
