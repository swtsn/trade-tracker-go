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

type analyticsRow struct {
	account *pb.Account
	summary *pb.GetAccountSummaryResponse
	err     error
}

type analyticsLoadedMsg struct {
	rows []analyticsRow
	from time.Time
	to   time.Time
}

// AnalyticsView shows per-account P&L summaries for a configurable date range.
type AnalyticsView struct {
	client  client.Client
	table   table.Model
	rows    []analyticsRow
	from    time.Time
	to      time.Time
	loading bool
	err     error
	width   int
	height  int
}

func NewAnalyticsView(c client.Client) AnalyticsView {
	now := time.Now()
	ytdStart := time.Date(now.Year(), 1, 1, 0, 0, 0, 0, now.Location())
	return AnalyticsView{
		client:  c,
		loading: true,
		from:    ytdStart,
		to:      now,
	}
}

func (v AnalyticsView) Update(msg tea.Msg, state SharedState) (AnalyticsView, tea.Cmd) {
	switch msg := msg.(type) {
	case LoadMsg:
		v.loading = true
		v.err = nil
		return v, v.load(state)

	case analyticsLoadedMsg:
		v.loading = false
		v.rows = msg.rows
		v.from = msg.from
		v.to = msg.to
		v.table = buildAnalyticsTable(msg.rows, v.width, v.height)
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

func (v AnalyticsView) View() string {
	if v.loading {
		return "Loading analytics..."
	}
	if v.err != nil {
		return errorViewStyle.Render(fmt.Sprintf("Error: %v", v.err))
	}

	period := lipgloss.NewStyle().Foreground(lipgloss.Color("8")).Render(
		fmt.Sprintf("Period: %s – %s", v.from.Format("2006-01-02"), v.to.Format("2006-01-02")),
	)
	return lipgloss.JoinVertical(lipgloss.Left,
		period,
		tableStyle.Render(v.table.View()),
	)
}

func (v *AnalyticsView) Resize(w, h int) {
	v.width = w
	v.height = h
	if len(v.rows) > 0 {
		v.table = buildAnalyticsTable(v.rows, w, h)
	}
}

func (v AnalyticsView) load(state SharedState) tea.Cmd {
	ids := accountIDs(state)
	accounts := state.Accounts
	c := v.client
	from := v.from
	to := v.to
	return func() tea.Msg {
		// Build account lookup for the requested IDs.
		accMap := make(map[string]*pb.Account, len(accounts))
		for _, a := range accounts {
			accMap[a.Id] = a
		}
		rows := make([]analyticsRow, len(ids))
		for i, id := range ids {
			summary, err := c.GetAccountSummary(context.Background(), id, from, to)
			rows[i] = analyticsRow{account: accMap[id], summary: summary, err: err}
		}
		return analyticsLoadedMsg{rows: rows, from: from, to: to}
	}
}

func buildAnalyticsTable(rows []analyticsRow, w, h int) table.Model {
	cols := []table.Column{
		{Title: "Account", Width: 24},
		{Title: "Realized P&L", Width: 14},
		{Title: "Close Fees", Width: 12},
		{Title: "Win Rate", Width: 10},
		{Title: "Positions Closed", Width: 16},
	}

	tableRows := make([]table.Row, len(rows))
	for i, r := range rows {
		acctLabel := "—"
		if r.account != nil {
			acctLabel = AccountLabel(r.account)
		}
		if r.err != nil {
			tableRows[i] = table.Row{acctLabel, "—", "—", "—", "—"}
			continue
		}
		winRate := "—"
		realizedPnl := "—"
		closeFees := "—"
		closedCount := "—"
		if r.summary != nil {
			winRate = fmt.Sprintf("%.0f%%", parseDecimalOrZero(r.summary.WinRate)*100)
			realizedPnl = formatPnl(r.summary.RealizedPnl)
			closeFees = fmt.Sprintf("$%.2f", parseDecimalOrZero(r.summary.CloseFees))
			closedCount = fmt.Sprintf("%d", r.summary.PositionsClosed)
		}
		tableRows[i] = table.Row{acctLabel, realizedPnl, closeFees, winRate, closedCount}
	}

	t := table.New(
		table.WithColumns(cols),
		table.WithRows(tableRows),
		table.WithFocused(true),
		table.WithHeight(h-3),
	)
	t.SetStyles(defaultTableStyles())
	return t
}
