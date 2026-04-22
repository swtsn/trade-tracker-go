package views

import (
	"context"
	"fmt"

	"github.com/charmbracelet/bubbles/table"
	tea "github.com/charmbracelet/bubbletea"

	pb "trade-tracker-go/gen/tradetracker/v1"
	"trade-tracker-go/internal/tui/client"
)

type positionsLoadedMsg struct {
	positions []*pb.Position
	err       error
}

// PositionsView shows the open positions table.
type PositionsView struct {
	client    client.Client
	table     table.Model
	positions []*pb.Position
	loading   bool
	err       error
	width     int
	height    int
}

func NewPositionsView(c client.Client) PositionsView {
	return PositionsView{client: c, loading: true}
}

func (v PositionsView) Update(msg tea.Msg, state SharedState) (PositionsView, tea.Cmd) {
	switch msg := msg.(type) {
	case LoadMsg:
		v.loading = true
		v.err = nil
		return v, v.load(state)

	case positionsLoadedMsg:
		v.loading = false
		if msg.err != nil {
			v.err = msg.err
			return v, nil
		}
		v.positions = msg.positions
		v.table = buildPositionsTable(msg.positions, v.width, v.height, true)
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

func (v PositionsView) View() string {
	if v.loading {
		return "Loading positions..."
	}
	if v.err != nil {
		return errorViewStyle.Render(fmt.Sprintf("Error: %v", v.err))
	}
	return tableStyle.Render(v.table.View())
}

func (v *PositionsView) Resize(w, h int) {
	v.width = w
	v.height = h
	if len(v.positions) > 0 {
		v.table = buildPositionsTable(v.positions, w, h, true)
	}
}

func (v PositionsView) load(state SharedState) tea.Cmd {
	ids := accountIDs(state)
	c := v.client
	return func() tea.Msg {
		var all []*pb.Position
		var lastErr error
		for _, id := range ids {
			resp, err := c.ListPositions(context.Background(), id, pb.PositionStatus_POSITION_STATUS_OPEN)
			if err != nil {
				lastErr = err
				continue
			}
			all = append(all, resp...)
		}
		// Only surface an error when we got no data at all.
		if len(all) == 0 && lastErr != nil {
			return positionsLoadedMsg{err: lastErr}
		}
		return positionsLoadedMsg{positions: all}
	}
}

// buildPositionsTable builds a table model for position rows.
// showOpen controls whether to show cost_basis (open) or realized_pnl (closed).
func buildPositionsTable(positions []*pb.Position, w, h int, showOpen bool) table.Model {
	cols := []table.Column{
		{Title: "Symbol", Width: 10},
		{Title: "Strategy", Width: 10},
		{Title: "Cost Basis", Width: pnlColumnWidth},
		{Title: "Opened", Width: 12},
	}
	if !showOpen {
		cols = append(cols, table.Column{Title: "Realized P&L", Width: pnlColumnWidth})
		cols = append(cols, table.Column{Title: "Closed", Width: 12})
	}

	rows := make([]table.Row, len(positions))
	for i, p := range positions {
		openedAt := "—"
		if p.OpenedAt != nil {
			openedAt = formatTS(p.OpenedAt.AsTime())
		}
		row := table.Row{
			p.UnderlyingSymbol,
			strategyLabel(p.StrategyType.String()),
			formatPnl(p.CostBasis),
			openedAt,
		}
		if !showOpen {
			closedAt := "—"
			if p.ClosedAt != nil {
				closedAt = formatTS(p.ClosedAt.AsTime())
			}
			row = append(row, formatPnl(p.RealizedPnl), closedAt)
		}
		rows[i] = row
	}

	t := table.New(
		table.WithColumns(cols),
		table.WithRows(rows),
		table.WithFocused(true),
		table.WithHeight(h-4),
	)
	t.SetStyles(defaultTableStyles())
	return t
}
