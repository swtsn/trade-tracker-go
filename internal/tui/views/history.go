package views

import (
	"context"
	"fmt"

	"github.com/charmbracelet/bubbles/table"
	tea "github.com/charmbracelet/bubbletea"

	pb "trade-tracker-go/gen/tradetracker/v1"
	"trade-tracker-go/internal/tui/client"
)

type historyLoadedMsg struct {
	positions []*pb.Position
	err       error
}

// HistoryView shows closed positions.
type HistoryView struct {
	client    client.Client
	table     table.Model
	positions []*pb.Position
	loading   bool
	err       error
	width     int
	height    int
}

func NewHistoryView(c client.Client) HistoryView {
	return HistoryView{client: c, loading: true}
}

func (v HistoryView) Update(msg tea.Msg, state SharedState) (HistoryView, tea.Cmd) {
	switch msg := msg.(type) {
	case LoadMsg:
		v.loading = true
		v.err = nil
		return v, v.load(state)

	case historyLoadedMsg:
		v.loading = false
		if msg.err != nil {
			v.err = msg.err
			return v, nil
		}
		v.positions = msg.positions
		v.table = buildPositionsTable(msg.positions, nil, nil, v.width, v.height, false)
		return v, nil

	case tea.KeyMsg:
		if !v.loading && v.err == nil {
			var cmd tea.Cmd
			v.table, cmd = v.table.Update(msg)
			return v, cmd
		}
	}
	return v, nil
}

func (v HistoryView) View() string {
	if v.loading {
		return "Loading history..."
	}
	if v.err != nil {
		return errorViewStyle.Render(fmt.Sprintf("Error: %v", v.err))
	}
	return tableStyle.Render(v.table.View())
}

func (v *HistoryView) Resize(w, h int) {
	v.width = w
	v.height = h
	if len(v.positions) > 0 {
		v.table = buildPositionsTable(v.positions, nil, nil, w, h, false)
	}
}

func (v HistoryView) load(state SharedState) tea.Cmd {
	ids := accountIDs(state)
	c := v.client
	return func() tea.Msg {
		var all []*pb.Position
		var lastErr error
		for _, id := range ids {
			resp, err := c.ListPositions(context.Background(), id, pb.PositionStatus_POSITION_STATUS_CLOSED)
			if err != nil {
				lastErr = err
				continue
			}
			all = append(all, resp...)
		}
		if len(all) == 0 && lastErr != nil {
			return historyLoadedMsg{err: lastErr}
		}
		return historyLoadedMsg{positions: all}
	}
}
