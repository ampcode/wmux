package policy

import (
	"fmt"
	"strings"
)

type Policy struct {
	allowed map[string]struct{}
}

func Default() Policy {
	return Policy{allowed: map[string]struct{}{
		"send-keys":       {},
		"refresh-client":  {},
		"kill-window":     {},
		"list-windows":    {},
		"list-panes":      {},
		"display-message": {},
		"capture-pane":    {},
		"show-options":    {},
	}}
}

func (p Policy) Validate(line string) error {
	cmd := commandName(line)
	return p.ValidateCommand(cmd)
}

func (p Policy) ValidateCommand(cmd string) error {
	if cmd == "" {
		return fmt.Errorf("empty command")
	}
	if _, ok := p.allowed[cmd]; !ok {
		return fmt.Errorf("blocked command: %s", cmd)
	}
	return nil
}

func commandName(line string) string {
	line = strings.TrimSpace(line)
	if line == "" {
		return ""
	}
	parts := strings.Fields(line)
	if len(parts) == 0 {
		return ""
	}
	return strings.ToLower(parts[0])
}
