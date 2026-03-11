package wshub

import (
	"sort"
	"strconv"
	"strings"
)

const modelPrefix = "__WMUX__"

type statePayload struct {
	Windows     []windowPayload       `json:"windows"`
	Panes       []panePayload         `json:"panes"`
	Unavailable *tmuxUnavailableState `json:"unavailable,omitempty"`
}

type tmuxUnavailableState struct {
	Reason string `json:"reason"`
}

type windowPayload struct {
	ID    string `json:"id"`
	Index int    `json:"index"`
	Name  string `json:"name"`
}

type panePayload struct {
	ID          string `json:"pane_id"`
	Name        string `json:"name"`
	SessionName string `json:"session_name"`
	WindowID    string `json:"window_id"`
	WindowIndex int    `json:"window_index"`
	WindowName  string `json:"window_name"`
	PaneIndex   int    `json:"pane_index"`
	Active      bool   `json:"active"`
	Left        int    `json:"left"`
	Top         int    `json:"top"`
	Width       int    `json:"width"`
	Height      int    `json:"height"`
	Title       string `json:"title"`
}

type modelState struct {
	windows map[string]windowPayload
	panes   map[string]panePayload
}

func newModelState() modelState {
	return modelState{
		windows: map[string]windowPayload{},
		panes:   map[string]panePayload{},
	}
}

func (m *modelState) reset() {
	m.windows = map[string]windowPayload{}
	m.panes = map[string]panePayload{}
}

func (m *modelState) applyOutputLines(lines []string) bool {
	updated := false
	nextWindows := make(map[string]windowPayload)
	nextPanes := make(map[string]panePayload)
	sawWindows := false
	sawPanes := false

	for _, line := range lines {
		if !strings.HasPrefix(line, modelPrefix) {
			continue
		}
		parts := strings.Split(line, "\t")
		kind := strings.TrimPrefix(parts[0], modelPrefix+"_")
		switch kind {
		case "win":
			if len(parts) < 4 {
				continue
			}
			idx, err := strconv.Atoi(parts[2])
			if err != nil {
				continue
			}
			nextWindows[parts[1]] = windowPayload{ID: parts[1], Index: idx, Name: parts[3]}
			sawWindows = true
		case "pane":
			if len(parts) < 10 {
				continue
			}
			pane, ok := parsePane(parts)
			if !ok {
				continue
			}
			nextPanes[pane.ID] = pane
			sawPanes = true
		}
	}

	if sawPanes {
		if !paneMapsEqual(m.panes, nextPanes) {
			m.panes = nextPanes
			updated = true
		}
		if !sawWindows {
			nextWindows = windowsFromPanes(nextPanes)
			sawWindows = true
		}
	}

	if sawWindows {
		if !windowMapsEqual(m.windows, nextWindows) {
			m.windows = nextWindows
			updated = true
		}
	}

	return updated
}

func paneMapsEqual(a, b map[string]panePayload) bool {
	if len(a) != len(b) {
		return false
	}
	for k, av := range a {
		if bv, ok := b[k]; !ok || av != bv {
			return false
		}
	}
	return true
}

func windowMapsEqual(a, b map[string]windowPayload) bool {
	if len(a) != len(b) {
		return false
	}
	for k, av := range a {
		if bv, ok := b[k]; !ok || av != bv {
			return false
		}
	}
	return true
}

func windowsFromPanes(panes map[string]panePayload) map[string]windowPayload {
	windows := make(map[string]windowPayload)
	for _, pane := range panes {
		if pane.WindowID == "" {
			continue
		}
		windows[pane.WindowID] = windowPayload{ID: pane.WindowID, Index: pane.WindowIndex, Name: pane.WindowName}
	}
	return windows
}

func (m *modelState) snapshot() statePayload {
	windows := make([]windowPayload, 0, len(m.windows))
	for _, w := range m.windows {
		windows = append(windows, w)
	}
	sort.Slice(windows, func(i, j int) bool {
		if windows[i].Index != windows[j].Index {
			return windows[i].Index < windows[j].Index
		}
		return windows[i].ID < windows[j].ID
	})

	panes := make([]panePayload, 0, len(m.panes))
	for _, p := range m.panes {
		panes = append(panes, p)
	}
	sort.Slice(panes, func(i, j int) bool {
		if panes[i].WindowIndex != panes[j].WindowIndex {
			return panes[i].WindowIndex < panes[j].WindowIndex
		}
		if panes[i].WindowID != panes[j].WindowID {
			return panes[i].WindowID < panes[j].WindowID
		}
		if panes[i].PaneIndex != panes[j].PaneIndex {
			return panes[i].PaneIndex < panes[j].PaneIndex
		}
		return panes[i].ID < panes[j].ID
	})

	return statePayload{Windows: windows, Panes: panes}
}

func parsePane(parts []string) (panePayload, bool) {
	offset := 0
	sessionName := ""
	if len(parts) > 1 && !strings.HasPrefix(parts[1], "%") {
		sessionName = parts[1]
		offset = 1
	}

	paneIndex, err := strconv.Atoi(parts[3+offset])
	if err != nil {
		return panePayload{}, false
	}
	left, err := strconv.Atoi(parts[5+offset])
	if err != nil {
		return panePayload{}, false
	}
	top, err := strconv.Atoi(parts[6+offset])
	if err != nil {
		return panePayload{}, false
	}
	width, err := strconv.Atoi(parts[7+offset])
	if err != nil {
		return panePayload{}, false
	}
	height, err := strconv.Atoi(parts[8+offset])
	if err != nil {
		return panePayload{}, false
	}

	name := parts[9+offset]
	title := name
	if len(parts) > 10+offset {
		title = parts[10+offset]
	}
	windowIndex := 0
	if len(parts) > 11+offset {
		if idx, err := strconv.Atoi(parts[11+offset]); err == nil {
			windowIndex = idx
		}
	}
	windowName := ""
	if len(parts) > 12+offset {
		windowName = parts[12+offset]
	}

	return panePayload{
		ID:          parts[1+offset],
		Name:        name,
		SessionName: sessionName,
		WindowID:    parts[2+offset],
		WindowIndex: windowIndex,
		WindowName:  windowName,
		PaneIndex:   paneIndex,
		Active:      parts[4+offset] == "1",
		Left:        left,
		Top:         top,
		Width:       width,
		Height:      height,
		Title:       title,
	}, true
}
