package views

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/table"
	"github.com/charmbracelet/bubbles/textinput"
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

type accountMutatedMsg struct {
	account *pb.Account
	err     error
}

// AccountsChangedMsg is produced after a successful account mutation.
// The root App handles it to update SharedState.Accounts; AccountsView
// handles it to refresh the table.
type AccountsChangedMsg struct {
	Accounts []*pb.Account
	rows     []accountSummaryRow // internal; only AccountsView uses this
}

type accountStep int

const (
	accountStepIdle accountStep = iota
	accountStepCreateBroker
	accountStepCreateNumber
	accountStepCreateName
	accountStepCreateConfirm
	accountStepRename
	accountStepMutating
	accountStepError
)

// AccountsView shows a summary table of all accounts with create/rename flows.
type AccountsView struct {
	client  client.Client
	table   table.Model
	rows    []accountSummaryRow
	loading bool
	err     error
	width   int
	height  int

	step        accountStep
	brokerInput textinput.Model
	numberInput textinput.Model
	nameInput   textinput.Model
	mutateErr   error
}

func NewAccountsView(c client.Client) AccountsView {
	brokerInput := textinput.New()
	brokerInput.Placeholder = "tastytrade"
	brokerInput.CharLimit = 64

	numberInput := textinput.New()
	numberInput.Placeholder = "account number"
	numberInput.CharLimit = 64

	nameInput := textinput.New()
	nameInput.Placeholder = "nickname (optional)"
	nameInput.CharLimit = 128

	return AccountsView{
		client:      c,
		loading:     true,
		brokerInput: brokerInput,
		numberInput: numberInput,
		nameInput:   nameInput,
	}
}

// InputActive reports true when a text input is focused so the root app
// suppresses global hotkeys.
func (v AccountsView) InputActive() bool {
	return v.step != accountStepIdle
}

func (v AccountsView) Update(msg tea.Msg, state SharedState) (AccountsView, tea.Cmd) {
	switch msg := msg.(type) {
	case LoadMsg:
		v.loading = true
		v.err = nil
		v.step = accountStepIdle
		return v, v.load(state)

	case accountsLoadedMsg:
		v.loading = false
		v.rows = msg.rows
		v.table = buildAccountsTable(msg.rows, v.width, v.height)
		return v, nil

	case accountMutatedMsg:
		if msg.err != nil {
			v.step = accountStepError
			v.mutateErr = msg.err
			return v, nil
		}
		// Re-fetch the account list from the server so newly created accounts appear.
		v.step = accountStepIdle
		v.loading = true
		return v, v.loadFresh()

	case AccountsChangedMsg:
		v.loading = false
		v.rows = msg.rows
		v.table = buildAccountsTable(msg.rows, v.width, v.height)
		return v, nil

	case tea.KeyMsg:
		return v.handleKey(msg, state)
	}
	return v, nil
}

func (v AccountsView) handleKey(msg tea.KeyMsg, state SharedState) (AccountsView, tea.Cmd) {
	switch v.step {
	case accountStepIdle:
		switch msg.String() {
		case "n":
			v.step = accountStepCreateBroker
			v.brokerInput.SetValue("")
			v.brokerInput.Focus()
			return v, textinput.Blink
		case "r":
			sel := v.selectedAccount()
			if sel == nil {
				return v, nil
			}
			v.step = accountStepRename
			v.nameInput.SetValue(sel.Name)
			v.nameInput.Focus()
			return v, textinput.Blink
		default:
			var cmd tea.Cmd
			v.table, cmd = v.table.Update(msg)
			return v, cmd
		}

	case accountStepCreateBroker:
		switch msg.String() {
		case "esc":
			v.step = accountStepIdle
			v.brokerInput.Blur()
		case "enter":
			if strings.TrimSpace(v.brokerInput.Value()) != "" {
				v.brokerInput.Blur()
				v.numberInput.SetValue("")
				v.numberInput.Focus()
				v.step = accountStepCreateNumber
				return v, textinput.Blink
			}
		default:
			var cmd tea.Cmd
			v.brokerInput, cmd = v.brokerInput.Update(msg)
			return v, cmd
		}

	case accountStepCreateNumber:
		switch msg.String() {
		case "esc":
			v.step = accountStepCreateBroker
			v.numberInput.Blur()
			v.brokerInput.Focus()
			return v, textinput.Blink
		case "enter":
			if strings.TrimSpace(v.numberInput.Value()) != "" {
				v.numberInput.Blur()
				v.nameInput.SetValue("")
				v.nameInput.Focus()
				v.step = accountStepCreateName
				return v, textinput.Blink
			}
		default:
			var cmd tea.Cmd
			v.numberInput, cmd = v.numberInput.Update(msg)
			return v, cmd
		}

	case accountStepCreateName:
		switch msg.String() {
		case "esc":
			v.step = accountStepCreateNumber
			v.nameInput.Blur()
			v.numberInput.Focus()
			return v, textinput.Blink
		case "enter":
			v.nameInput.Blur()
			v.step = accountStepCreateConfirm
		default:
			var cmd tea.Cmd
			v.nameInput, cmd = v.nameInput.Update(msg)
			return v, cmd
		}

	case accountStepCreateConfirm:
		switch msg.String() {
		case "esc":
			v.step = accountStepCreateName
			v.nameInput.Focus()
			return v, textinput.Blink
		case "enter", "y":
			v.step = accountStepMutating
			return v, v.runCreate()
		case "n":
			v.step = accountStepIdle
		}

	case accountStepRename:
		switch msg.String() {
		case "esc":
			v.step = accountStepIdle
			v.nameInput.Blur()
		case "enter":
			v.nameInput.Blur()
			sel := v.selectedAccount()
			if sel == nil {
				v.step = accountStepIdle
				return v, nil
			}
			v.step = accountStepMutating
			return v, v.runRename(sel.Id, v.nameInput.Value())
		default:
			var cmd tea.Cmd
			v.nameInput, cmd = v.nameInput.Update(msg)
			return v, cmd
		}

	case accountStepError:
		if msg.String() == "enter" || msg.String() == "esc" {
			v.step = accountStepIdle
			v.mutateErr = nil
		}
	}
	return v, nil
}

func (v AccountsView) View() string {
	switch v.step {
	case accountStepCreateBroker:
		return strings.Join([]string{
			accountTitleStyle.Render("New Account — Broker"),
			"",
			"Broker name (e.g. tastytrade, schwab):",
			v.brokerInput.View(),
			"",
			accountDimStyle.Render("Enter to confirm  Esc to cancel"),
		}, "\n")

	case accountStepCreateNumber:
		return strings.Join([]string{
			accountTitleStyle.Render("New Account — Account Number"),
			"",
			"Account number:",
			v.numberInput.View(),
			"",
			accountDimStyle.Render("Enter to confirm  Esc to go back"),
		}, "\n")

	case accountStepCreateName:
		return strings.Join([]string{
			accountTitleStyle.Render("New Account — Nickname"),
			"",
			"Nickname (optional, press Enter to skip):",
			v.nameInput.View(),
			"",
			accountDimStyle.Render("Enter to confirm  Esc to go back"),
		}, "\n")

	case accountStepCreateConfirm:
		return strings.Join([]string{
			accountTitleStyle.Render("New Account — Confirm"),
			"",
			fmt.Sprintf("  Broker:  %s", v.brokerInput.Value()),
			fmt.Sprintf("  Number:  %s", v.numberInput.Value()),
			fmt.Sprintf("  Name:    %s", v.nameInput.Value()),
			"",
			accountWarningStyle.Render("Press Enter or 'y' to create, 'n' or Esc to cancel."),
		}, "\n")

	case accountStepRename:
		sel := v.selectedAccount()
		label := ""
		if sel != nil {
			label = AccountLabel(sel)
		}
		return strings.Join([]string{
			accountTitleStyle.Render("Rename Account"),
			"",
			fmt.Sprintf("Account: %s", label),
			"New nickname:",
			v.nameInput.View(),
			"",
			accountDimStyle.Render("Enter to save  Esc to cancel"),
		}, "\n")

	case accountStepMutating:
		return "Saving..."

	case accountStepError:
		return strings.Join([]string{
			errorViewStyle.Render("Error"),
			"",
			errorViewStyle.Render(v.mutateErr.Error()),
			"",
			accountDimStyle.Render("Enter or Esc to return."),
		}, "\n")
	}

	// accountStepIdle — normal table view
	if v.loading {
		return "Loading accounts..."
	}
	if v.err != nil {
		return errorViewStyle.Render(fmt.Sprintf("Error: %v", v.err))
	}
	help := accountDimStyle.Render("n new  r rename")
	return lipgloss.JoinVertical(lipgloss.Left,
		tableStyle.Render(v.table.View()),
		help,
	)
}

func (v *AccountsView) Resize(w, h int) {
	v.width = w
	v.height = h
	if len(v.rows) > 0 {
		v.table = buildAccountsTable(v.rows, w, h)
	}
}

// selectedAccount returns the account corresponding to the highlighted table row,
// or nil if the table is empty.
func (v *AccountsView) selectedAccount() *pb.Account {
	idx := v.table.Cursor()
	if idx < 0 || idx >= len(v.rows) {
		return nil
	}
	return v.rows[idx].account
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

// loadFresh re-fetches the account list from the server before building summary rows.
// Used after mutations so newly created or renamed accounts are reflected immediately.
// Produces AccountsChangedMsg so the root App can also update SharedState.Accounts.
func (v AccountsView) loadFresh() tea.Cmd {
	c := v.client
	now := time.Now()
	ytdStart := time.Date(now.Year(), 1, 1, 0, 0, 0, 0, now.Location())
	return func() tea.Msg {
		accounts, err := c.ListAccounts(context.Background())
		if err != nil {
			return AccountsChangedMsg{}
		}
		rows := make([]accountSummaryRow, len(accounts))
		for i, acc := range accounts {
			summary, _ := c.GetAccountSummary(context.Background(), acc.Id, ytdStart, now)
			rows[i] = accountSummaryRow{account: acc, summary: summary}
		}
		return AccountsChangedMsg{Accounts: accounts, rows: rows}
	}
}

func (v AccountsView) runCreate() tea.Cmd {
	c := v.client
	broker := strings.TrimSpace(v.brokerInput.Value())
	number := strings.TrimSpace(v.numberInput.Value())
	name := strings.TrimSpace(v.nameInput.Value())
	return func() tea.Msg {
		account, err := c.CreateAccount(context.Background(), broker, number, name)
		return accountMutatedMsg{account: account, err: err}
	}
}

func (v AccountsView) runRename(id, name string) tea.Cmd {
	c := v.client
	return func() tea.Msg {
		account, err := c.UpdateAccount(context.Background(), id, strings.TrimSpace(name))
		return accountMutatedMsg{account: account, err: err}
	}
}

func buildAccountsTable(rows []accountSummaryRow, w, h int) table.Model {
	cols := []table.Column{
		{Title: "Account", Width: 32},
		{Title: "Realized P&L (YTD)", Width: 20},
		{Title: "Win Rate", Width: 10},
		{Title: "Closed", Width: 8},
	}

	tableRows := make([]table.Row, len(rows))
	for i, r := range rows {
		acctLabel := AccountLabel(r.account)
		if r.err != nil {
			tableRows[i] = table.Row{acctLabel, "—", "—", "—"}
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
		tableRows[i] = table.Row{acctLabel, realizedPnl, winRate, closedCount}
	}

	t := table.New(
		table.WithColumns(cols),
		table.WithRows(tableRows),
		table.WithFocused(true),
		table.WithHeight(h-5), // reserve 1 extra line for the help hint, 2 for tableStyle borders
	)
	t.SetStyles(defaultTableStyles())
	return t
}

var (
	errorViewStyle      = lipgloss.NewStyle().Foreground(lipgloss.Color("9"))
	tableStyle          = lipgloss.NewStyle().BorderStyle(lipgloss.NormalBorder()).BorderForeground(lipgloss.Color("8"))
	accountTitleStyle   = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("12"))
	accountDimStyle     = lipgloss.NewStyle().Foreground(lipgloss.Color("8"))
	accountWarningStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("11"))
)
