package views

import (
	"context"
	"fmt"
	"strings"

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
	expanded     map[int]bool // set of expanded trade indices
}

func NewTradesView(c client.Client) TradesView {
	ti := textinput.New()
	ti.Placeholder = "symbol filter"
	ti.CharLimit = 10
	return TradesView{client: c, loading: true, filterInput: ti, expanded: make(map[int]bool)}
}

func (v TradesView) Update(msg tea.Msg, state SharedState) (TradesView, tea.Cmd) {
	switch msg := msg.(type) {
	case LoadMsg:
		v.loading = true
		v.err = nil
		v.expanded = make(map[int]bool)
		return v, v.load(state)

	case tradesLoadedMsg:
		v.loading = false
		if msg.err != nil {
			v.err = msg.err
			return v, nil
		}
		v.trades = msg.trades
		v.expanded = make(map[int]bool)
		v.table = buildTradesTable(v.trades, v.expanded, v.width, v.height)
		return v, nil

	case tea.KeyMsg:
		if v.filterOpen {
			switch msg.String() {
			case "enter":
				v.symbolFilter = v.filterInput.Value()
				v.filterInput.Blur()
				v.filterOpen = false
				v.loading = true
				return v, v.load(state)
			case "esc":
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
		case "enter":
			if !v.loading && v.err == nil && len(v.trades) > 0 {
				cursor := v.table.Cursor()
				tradeIdx := tradeIdxAtCursor(v.trades, v.expanded, cursor)
				if tradeIdx < 0 {
					return v, nil // on a detail row
				}
				if v.expanded[tradeIdx] {
					delete(v.expanded, tradeIdx)
					v.table = buildTradesTable(v.trades, v.expanded, v.width, v.height)
					v.table.SetCursor(tradeIdx)
				} else {
					v.expanded[tradeIdx] = true
					v.table = buildTradesTable(v.trades, v.expanded, v.width, v.height)
					v.table.SetCursor(cursor)
				}
			}
			return v, nil
		case "e":
			if !v.loading && v.err == nil {
				for i := range v.trades {
					v.expanded[i] = true
				}
				cursor := v.table.Cursor()
				v.table = buildTradesTable(v.trades, v.expanded, v.width, v.height)
				v.table.SetCursor(cursor)
			}
			return v, nil
		case "esc":
			if len(v.expanded) > 0 {
				cursor := v.table.Cursor()
				v.expanded = make(map[int]bool)
				v.table = buildTradesTable(v.trades, v.expanded, v.width, v.height)
				v.table.SetCursor(cursor)
				return v, nil
			}
		case "/":
			v.filterOpen = true
			v.filterInput.SetValue(v.symbolFilter)
			v.filterInput.Focus()
			return v, textinput.Blink
		}
		if !v.loading && v.err == nil {
			var cmd tea.Cmd
			v.table, cmd = v.table.Update(msg)
			return v, cmd
		}
	}
	return v, nil
}

// InputActive reports whether the view is capturing keyboard input for a text field.
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
		cursor := v.table.Cursor()
		v.table = buildTradesTable(v.trades, v.expanded, w, h)
		v.table.SetCursor(cursor)
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

// tradeIdxAtCursor maps a table cursor back to the trade index,
// accounting for all injected detail rows. Returns -1 if on a detail row.
func tradeIdxAtCursor(trades []*pb.Trade, expanded map[int]bool, cursor int) int {
	row := 0
	for i, tr := range trades {
		if row == cursor {
			return i
		}
		row++
		if expanded[i] {
			row += 2 + len(tr.Transactions) // header + separator + transactions
		}
	}
	return -1
}

func buildTradesTable(trades []*pb.Trade, expanded map[int]bool, w, h int) table.Model {
	cols := []table.Column{
		{Title: "Symbol", Width: 10},
		{Title: "Instrument", Width: 20},
		{Title: "Qty / Price", Width: 26},
	}

	var rows []table.Row
	for i, tr := range trades {
		executedAt := "—"
		if tr.ExecutedAt != nil {
			executedAt = formatTS(tr.ExecutedAt.AsTime())
		}
		rows = append(rows, table.Row{tr.UnderlyingSymbol, executedAt, tr.Notes})

		if expanded[i] {
			rows = append(rows, table.Row{"  Action", "Instrument", "Qty / Price"})
			rows = append(rows, table.Row{
				"  " + strings.Repeat("─", 8),
				strings.Repeat("─", 18),
				strings.Repeat("─", 24),
			})
			for _, tx := range tr.Transactions {
				price := fmt.Sprintf("%s @ %s  fees %s",
					tx.Quantity, formatCurrency(tx.FillPrice), formatCurrency(tx.Fees))
				rows = append(rows, table.Row{
					"  " + formatAction(tx.Action),
					formatInstrument(tx.Instrument),
					price,
				})
			}
		}
	}

	t := table.New(
		table.WithColumns(cols),
		table.WithRows(rows),
		table.WithFocused(true),
		table.WithHeight(h-5),
	)
	t.SetStyles(defaultTableStyles())
	return t
}
