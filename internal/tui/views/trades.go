package views

import (
	"context"
	"fmt"

	"github.com/charmbracelet/bubbles/table"
	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	pb "trade-tracker-go/gen/tradetracker/v1"
	"trade-tracker-go/internal/tui/client"
)

type tradesLoadedMsg struct {
	trades []*pb.Trade
	err    error
}

// TradesView shows a paginated audit log of all trades.
type TradesView struct {
	client       client.Client
	table        table.Model
	trades       []*pb.Trade
	filterInput  textinput.Model
	filterOpen   bool
	symbolFilter string
	loading      bool
	err          error
	width        int
	height       int
}

func NewTradesView(c client.Client) TradesView {
	ti := textinput.New()
	ti.Placeholder = "symbol filter"
	ti.CharLimit = 10
	return TradesView{client: c, loading: true, filterInput: ti}
}

func (v TradesView) Update(msg tea.Msg, state SharedState) (TradesView, tea.Cmd) {
	switch msg := msg.(type) {
	case LoadMsg:
		v.loading = true
		v.err = nil
		return v, v.load(state)

	case tradesLoadedMsg:
		v.loading = false
		if msg.err != nil {
			v.err = msg.err
			return v, nil
		}
		v.trades = msg.trades
		v.table = buildTradesTable(msg.trades, v.width, v.height)
		return v, nil

	case tea.KeyMsg:
		if v.filterOpen {
			switch msg.String() {
			case "enter":
				v.symbolFilter = v.filterInput.Value()
				v.filterInput.Blur()
				v.filterOpen = false
				// Re-load with the new filter.
				v.loading = true
				return v, v.load(state)
			case "esc":
				// Cancel: restore previous filter value without reloading.
				v.filterInput.Blur()
				v.filterOpen = false
				return v, nil
			default:
				var cmd tea.Cmd
				v.filterInput, cmd = v.filterInput.Update(msg)
				return v, cmd
			}
		}
		switch msg.String() {
		case "/":
			v.filterOpen = true
			v.filterInput.SetValue(v.symbolFilter)
			v.filterInput.Focus()
			return v, textinput.Blink
		default:
			if !v.loading && v.err == nil {
				var cmd tea.Cmd
				v.table, cmd = v.table.Update(msg)
				return v, cmd
			}
		}
	}
	return v, nil
}

// InputActive reports whether the view is currently capturing keyboard input
// for a text field. When true, the root app must not intercept global hotkeys.
func (v TradesView) InputActive() bool { return v.filterOpen }

func (v TradesView) View() string {
	if v.loading {
		return "Loading trades..."
	}
	if v.err != nil {
		return errorViewStyle.Render(fmt.Sprintf("Error: %v", v.err))
	}

	filter := ""
	if v.filterOpen {
		filter = lipgloss.NewStyle().Foreground(lipgloss.Color("11")).Render("Symbol: ") + v.filterInput.View()
	} else if v.symbolFilter != "" {
		filter = lipgloss.NewStyle().Foreground(lipgloss.Color("8")).Render(
			fmt.Sprintf("Filter: %s  (/ to change)", v.symbolFilter))
	} else {
		filter = lipgloss.NewStyle().Foreground(lipgloss.Color("8")).Render("/ to filter by symbol")
	}

	return lipgloss.JoinVertical(lipgloss.Left,
		tableStyle.Render(v.table.View()),
		filter,
	)
}

func (v *TradesView) Resize(w, h int) {
	v.width = w
	v.height = h
	if len(v.trades) > 0 {
		v.table = buildTradesTable(v.trades, w, h)
	}
}

func (v TradesView) load(state SharedState) tea.Cmd {
	ids := accountIDs(state)
	c := v.client
	sym := v.symbolFilter
	return func() tea.Msg {
		var all []*pb.Trade
		var lastErr error
		for _, id := range ids {
			trades, err := c.ListTrades(context.Background(), client.ListTradesParams{
				AccountID: id,
				Symbol:    sym,
			})
			if err != nil {
				lastErr = err
				continue
			}
			all = append(all, trades...)
		}
		if len(all) == 0 && lastErr != nil {
			return tradesLoadedMsg{err: lastErr}
		}
		return tradesLoadedMsg{trades: all}
	}
}

func buildTradesTable(trades []*pb.Trade, w, h int) table.Model {
	cols := []table.Column{
		{Title: "Symbol", Width: 10},
		{Title: "Executed", Width: 12},
		{Title: "Notes", Width: 20},
	}

	rows := make([]table.Row, len(trades))
	for i, tr := range trades {
		executedAt := "—"
		if tr.ExecutedAt != nil {
			executedAt = formatTS(tr.ExecutedAt.AsTime())
		}
		rows[i] = table.Row{
			tr.UnderlyingSymbol,
			executedAt,
			tr.Notes,
		}
	}

	t := table.New(
		table.WithColumns(cols),
		table.WithRows(rows),
		table.WithFocused(true),
		table.WithHeight(h-5), // 1 extra line for filter bar, 2 for tableStyle borders
	)
	t.SetStyles(defaultTableStyles())
	return t
}
