package views

import (
	"context"
	"fmt"
	"time"

	"github.com/charmbracelet/bubbles/table"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	pb "trade-tracker-go/gen/tradetracker/v1"
	"trade-tracker-go/internal/tui/client"
)

// accountSummaryRow combines account metadata with its analytics summary.
type accountSummaryRow struct {
	account *pb.Account
	summary *pb.GetAccountSummaryResponse
	err     error
}

type accountsLoadedMsg struct {
	rows []accountSummaryRow
}

// AccountsView shows a summary table of all accounts.
type AccountsView struct {
	client  client.Client
	table   table.Model
	rows    []accountSummaryRow
	loading bool
	err     error
	width   int
	height  int
}

func NewAccountsView(c client.Client) AccountsView {
	return AccountsView{client: c, loading: true}
}

func (v AccountsView) Update(msg tea.Msg, state SharedState) (AccountsView, tea.Cmd) {
	switch msg := msg.(type) {
	case LoadMsg:
		v.loading = true
		v.err = nil
		return v, v.load(state)

	case accountsLoadedMsg:
		v.loading = false
		v.rows = msg.rows
		v.table = buildAccountsTable(msg.rows, v.width, v.height)
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

func (v AccountsView) View() string {
	if v.loading {
		return "Loading accounts..."
	}
	if v.err != nil {
		return errorViewStyle.Render(fmt.Sprintf("Error: %v", v.err))
	}
	return tableStyle.Render(v.table.View())
}

func (v *AccountsView) Resize(w, h int) {
	v.width = w
	v.height = h
	if len(v.rows) > 0 {
		v.table = buildAccountsTable(v.rows, w, h)
	}
}

func (v AccountsView) load(state SharedState) tea.Cmd {
	accounts := state.Accounts
	c := v.client
	now := time.Now()
	ytdStart := time.Date(now.Year(), 1, 1, 0, 0, 0, 0, now.Location())
	return func() tea.Msg {
		rows := make([]accountSummaryRow, len(accounts))
		for i, acc := range accounts {
			summary, err := c.GetAccountSummary(context.Background(), acc.Id, ytdStart, now)
			rows[i] = accountSummaryRow{account: acc, summary: summary, err: err}
		}
		return accountsLoadedMsg{rows: rows}
	}
}

func buildAccountsTable(rows []accountSummaryRow, w, h int) table.Model {
	cols := []table.Column{
		{Title: "Account", Width: 24},
		{Title: "Broker", Width: 14},
		{Title: "Realized P&L (YTD)", Width: 20},
		{Title: "Win Rate", Width: 10},
		{Title: "Closed", Width: 8},
	}

	tableRows := make([]table.Row, len(rows))
	for i, r := range rows {
		acctLabel := AccountLabel(r.account)
		if r.err != nil {
			tableRows[i] = table.Row{acctLabel, r.account.Broker, "—", "—", "—"}
			continue
		}
		winRate := "—"
		if r.summary != nil {
			winRate = fmt.Sprintf("%.0f%%", parseDecimalOrZero(r.summary.WinRate)*100)
		}
		realizedPnl := "—"
		closedCount := "—"
		if r.summary != nil {
			realizedPnl = formatPnl(r.summary.RealizedPnl)
			closedCount = fmt.Sprintf("%d", r.summary.PositionsClosed)
		}
		tableRows[i] = table.Row{acctLabel, r.account.Broker, realizedPnl, winRate, closedCount}
	}

	t := table.New(
		table.WithColumns(cols),
		table.WithRows(tableRows),
		table.WithFocused(true),
		table.WithHeight(h-2),
	)
	t.SetStyles(defaultTableStyles())
	return t
}

var errorViewStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("9"))
var tableStyle = lipgloss.NewStyle().BorderStyle(lipgloss.NormalBorder()).BorderForeground(lipgloss.Color("8"))
