package wshub

import (
	"sort"
	"strconv"
	"strings"
)

const modelPrefix = "__WMUX__"

type statePayload struct {
	Windows []windowPayload `json:"windows"`
	Panes   []panePayload   `json:"panes"`
}

type windowPayload struct {
	ID    string `json:"id"`
	Index int    `json:"index"`
	Name  string `json:"name"`
}

type panePayload struct {
	ID        string `json:"id"`
	WindowID  string `json:"window_id"`
	PaneIndex int    `json:"pane_index"`
	Active    bool   `json:"active"`
	Left      int    `json:"left"`
	Top       int    `json:"top"`
	Width     int    `json:"width"`
	Height    int    `json:"height"`
	Title     string `json:"title"`
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
			next := windowPayload{ID: parts[1], Index: idx, Name: parts[3]}
			if cur, ok := m.windows[next.ID]; !ok || cur != next {
				m.windows[next.ID] = next
				updated = true
			}
		case "pane":
			if len(parts) < 10 {
				continue
			}
			pane, ok := parsePane(parts)
			if !ok {
				continue
			}
			if cur, ok := m.panes[pane.ID]; !ok || cur != pane {
				m.panes[pane.ID] = pane
				updated = true
			}
		}
	}
	return updated
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
	paneIndex, err := strconv.Atoi(parts[3])
	if err != nil {
		return panePayload{}, false
	}
	left, err := strconv.Atoi(parts[5])
	if err != nil {
		return panePayload{}, false
	}
	top, err := strconv.Atoi(parts[6])
	if err != nil {
		return panePayload{}, false
	}
	width, err := strconv.Atoi(parts[7])
	if err != nil {
		return panePayload{}, false
	}
	height, err := strconv.Atoi(parts[8])
	if err != nil {
		return panePayload{}, false
	}

	return panePayload{
		ID:        parts[1],
		WindowID:  parts[2],
		PaneIndex: paneIndex,
		Active:    parts[4] == "1",
		Left:      left,
		Top:       top,
		Width:     width,
		Height:    height,
		Title:     parts[9],
	}, true
}
