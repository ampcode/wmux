package tmuxparse

// BlockHeader is the three-field header used by %begin/%end/%error lines.
type BlockHeader struct {
	EpochSeconds int64
	CommandID    int64
	Flags        int64
}

// Command is a completed command response block.
type Command struct {
	Header  BlockHeader
	End     BlockHeader
	Success bool
	Output  []string
}

func (Command) streamEvent() {}

// Notification is a tmux out-of-band %notification line.
type Notification struct {
	Name  string
	Raw   string
	Args  []string
	Text  string
	Value string
}

func (Notification) streamEvent() {}

// ParseError describes a malformed control-mode line or invalid state
// transition encountered while parsing.
type ParseError struct {
	Line    string
	Message string
}

func (e ParseError) Error() string {
	if e.Line == "" {
		return e.Message
	}
	return e.Message + ": " + e.Line
}

func (ParseError) streamEvent() {}

// StreamEvent is delivered by the high-level parser API.
type StreamEvent interface {
	streamEvent()
}
