package tmuxparse

import "sync"

// StreamParser is the high-level control-mode parser API. It exposes parsed
// Command, Notification, and ParseError structs over a single event channel.
type StreamParser struct {
	mu      sync.Mutex
	events  chan StreamEvent
	closed  bool
	current *Command
	parser  *Parser
}

func NewStreamParser(buffer int) *StreamParser {
	sp := &StreamParser{
		events: make(chan StreamEvent, buffer),
	}
	sp.parser = NewParser(Callbacks{
		OnCommandBegin: sp.onCommandBegin,
		OnCommandLine:  sp.onCommandLine,
		OnCommandEnd:   sp.onCommandEnd,
		OnNotification: sp.onNotification,
		OnError:        sp.onError,
	})
	return sp
}

func (s *StreamParser) Events() <-chan StreamEvent {
	return s.events
}

func (s *StreamParser) FeedLine(line string) {
	s.mu.Lock()
	closed := s.closed
	s.mu.Unlock()
	if closed {
		return
	}
	s.parser.FeedLine(line)
}

func (s *StreamParser) Close() {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return
	}
	s.mu.Unlock()

	s.parser.Finish()

	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return
	}
	s.closed = true
	close(s.events)
	s.mu.Unlock()
}

func (s *StreamParser) onCommandBegin(h BlockHeader) {
	s.current = &Command{Header: h}
}

func (s *StreamParser) onCommandLine(_ BlockHeader, line string) {
	if s.current == nil {
		return
	}
	s.current.Output = append(s.current.Output, line)
}

func (s *StreamParser) onCommandEnd(begin BlockHeader, end BlockHeader, success bool) {
	if s.current == nil {
		s.emit(ParseError{Message: "command end without active command"})
		return
	}
	s.current.Header = begin
	s.current.End = end
	s.current.Success = success
	s.emit(*s.current)
	s.current = nil
}

func (s *StreamParser) onNotification(n Notification) {
	s.emit(n)
}

func (s *StreamParser) onError(err ParseError) {
	s.emit(err)
}

func (s *StreamParser) emit(ev StreamEvent) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return
	}
	s.events <- ev
}
