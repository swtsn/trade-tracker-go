// Package tui implements the trade-tracker terminal UI.
package tui

import (
	"context"
	"fmt"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	pb "trade-tracker-go/gen/tradetracker/v1"
	"trade-tracker-go/internal/tui/client"
	"trade-tracker-go/internal/tui/views"
)

// accountsLoadedMsg carries the result of the initial accounts fetch.
type accountsLoadedMsg struct {
	accounts []*pb.Account
	err      error
}

var (
	tabActiveStyle   = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("15")).Background(lipgloss.Color("4"))
	tabInactiveStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("8")).Background(lipgloss.Color("0"))
	tabSepStyle      = lipgloss.NewStyle().Foreground(lipgloss.Color("8")).Background(lipgloss.Color("0"))
	headerBGStyle    = lipgloss.NewStyle().Background(lipgloss.Color("0"))
	headerSepStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color("8"))
	errorStyle       = lipgloss.NewStyle().Foreground(lipgloss.Color("9"))
	helpStyle        = lipgloss.NewStyle().Foreground(lipgloss.Color("8"))
)

var viewLabels = [viewCount]string{
	ViewAccounts:  "Accounts",
	ViewPositions: "Positions",
	ViewHistory:   "History",
	ViewTrades:    "Trades",
	ViewAnalytics: "Analytics",
	ViewImport:    "Import",
}

// App is the root Bubbletea model.
type App struct {
	state  views.SharedState
	client client.Client
	addr   string

	accountsView  views.AccountsView
	positionsView views.PositionsView
	historyView   views.HistoryView
	tradesView    views.TradesView
	analyticsView views.AnalyticsView
	importView    views.ImportView

	activeView  ViewID
	initialized bool
	initErr     error
}

// New returns an App ready to run.
func New(c client.Client, addr string) *App {
	return &App{client: c, addr: addr}
}

func (a *App) Init() tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		accs, err := a.client.ListAccounts(ctx)
		return accountsLoadedMsg{accounts: accs, err: err}
	}
}

func (a *App) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		a.state.Width = msg.Width
		a.state.Height = msg.Height
		a.propagateSize()
		return a, nil

	case tea.KeyMsg:
		// ctrl+c always quits regardless of input state.
		if msg.String() == "ctrl+c" {
			return a, tea.Quit
		}
		// Other global hotkeys only fire when no view has captured the keyboard.
		if !a.activeViewCapturingInput() {
			switch msg.String() {
			case "q":
				return a, tea.Quit
			case "1":
				return a, a.switchView(ViewAccounts)
			case "2":
				return a, a.switchView(ViewPositions)
			case "3":
				return a, a.switchView(ViewHistory)
			case "4":
				return a, a.switchView(ViewTrades)
			case "5":
				return a, a.switchView(ViewAnalytics)
			case "6":
				return a, a.switchView(ViewImport)
			case "tab":
				next := ViewID((int(a.activeView) + 1) % int(viewCount))
				return a, a.switchView(next)
			case "shift+tab":
				prev := ViewID((int(a.activeView) - 1 + int(viewCount)) % int(viewCount))
				return a, a.switchView(prev)
			case "[":
				a.prevAccount()
				return a, a.reloadActiveView()
			case "]":
				a.nextAccount()
				return a, a.reloadActiveView()
			}
		}

	case accountsLoadedMsg:
		a.initialized = true
		if msg.err != nil {
			a.initErr = msg.err
			return a, nil
		}
		a.state.Accounts = msg.accounts
		a.accountsView = views.NewAccountsView(a.client)
		a.positionsView = views.NewPositionsView(a.client)
		a.historyView = views.NewHistoryView(a.client)
		a.tradesView = views.NewTradesView(a.client)
		a.analyticsView = views.NewAnalyticsView(a.client)
		a.importView = views.NewImportView(a.client)
		a.propagateSize()
		return a, a.loadActiveView()

	case views.AccountsChangedMsg:
		// A mutation (create/rename) completed; keep shared account list in sync
		// so the header selector and other views reflect the change.
		if msg.Accounts != nil {
			a.state.Accounts = msg.Accounts
		}
		return a, a.delegateToActiveView(msg)
	}

	// Delegate to active view.
	return a, a.delegateToActiveView(msg)
}

func (a *App) View() string {
	if a.initErr != nil {
		return errorStyle.Render(fmt.Sprintf("failed to connect to %s: %v\n\nPress q to quit.", a.addr, a.initErr))
	}
	if !a.initialized {
		return "Connecting..."
	}

	header := a.renderHeader()
	sep := headerSepStyle.Render(strings.Repeat("─", a.state.Width))
	content := a.renderActiveView()
	help := helpStyle.Render("[1-6] view  [Tab] next  [/] account  q quit")

	return lipgloss.JoinVertical(lipgloss.Left, header, sep, content, help)
}

func (a *App) renderHeader() string {
	tabs := ""
	for i := ViewID(0); i < viewCount; i++ {
		if i > 0 {
			tabs += tabSepStyle.Render("│")
		}
		label := fmt.Sprintf(" %d:%s ", i+1, viewLabels[i])
		if i == a.activeView {
			tabs += tabActiveStyle.Render(label)
		} else {
			tabs += tabInactiveStyle.Render(label)
		}
	}

	acct := "All Accounts"
	if a.state.SelectedAccountID != views.AllAccountsID {
		if ac := a.state.SelectedAccount(); ac != nil {
			acct = views.AccountLabel(ac)
		}
	}
	selector := lipgloss.NewStyle().Foreground(lipgloss.Color("11")).Render(
		fmt.Sprintf(" [%s]", acct),
	)

	gap := a.state.Width - lipgloss.Width(tabs) - lipgloss.Width(selector)
	if gap < 0 {
		gap = 0
	}
	spacer := lipgloss.NewStyle().Width(gap).Render("")
	return headerBGStyle.Width(a.state.Width).Render(tabs + spacer + selector)
}

func (a *App) renderActiveView() string {
	switch a.activeView {
	case ViewAccounts:
		return a.accountsView.View()
	case ViewPositions:
		return a.positionsView.View()
	case ViewHistory:
		return a.historyView.View()
	case ViewTrades:
		return a.tradesView.View()
	case ViewAnalytics:
		return a.analyticsView.View()
	case ViewImport:
		return a.importView.View()
	}
	return ""
}

func (a *App) delegateToActiveView(msg tea.Msg) tea.Cmd {
	var cmd tea.Cmd
	switch a.activeView {
	case ViewAccounts:
		a.accountsView, cmd = a.accountsView.Update(msg, a.state)
	case ViewPositions:
		a.positionsView, cmd = a.positionsView.Update(msg, a.state)
	case ViewHistory:
		a.historyView, cmd = a.historyView.Update(msg, a.state)
	case ViewTrades:
		a.tradesView, cmd = a.tradesView.Update(msg, a.state)
	case ViewAnalytics:
		a.analyticsView, cmd = a.analyticsView.Update(msg, a.state)
	case ViewImport:
		a.importView, cmd = a.importView.Update(msg, a.state)
	}
	return cmd
}

func (a *App) switchView(v ViewID) tea.Cmd {
	a.activeView = v
	return a.loadActiveView()
}

func (a *App) loadActiveView() tea.Cmd {
	return a.delegateToActiveView(views.LoadMsg{State: a.state})
}

func (a *App) reloadActiveView() tea.Cmd {
	return a.loadActiveView()
}

func (a *App) nextAccount() {
	accs := a.state.Accounts
	if len(accs) == 0 {
		return
	}
	if a.state.SelectedAccountID == views.AllAccountsID {
		a.state.SelectedAccountID = accs[0].Id
		return
	}
	for i, acc := range accs {
		if acc.Id == a.state.SelectedAccountID {
			a.state.SelectedAccountID = accs[(i+1)%len(accs)].Id
			return
		}
	}
	// Fell through (account removed?) — reset to All.
	a.state.SelectedAccountID = views.AllAccountsID
}

func (a *App) prevAccount() {
	accs := a.state.Accounts
	if len(accs) == 0 {
		return
	}
	if a.state.SelectedAccountID == views.AllAccountsID {
		a.state.SelectedAccountID = accs[len(accs)-1].Id
		return
	}
	for i, acc := range accs {
		if acc.Id == a.state.SelectedAccountID {
			a.state.SelectedAccountID = accs[(i-1+len(accs))%len(accs)].Id
			return
		}
	}
	a.state.SelectedAccountID = views.AllAccountsID
}

// activeViewCapturingInput returns true when the active view owns keyboard
// input (e.g. a text field is focused). Global hotkeys are suppressed in that state.
func (a *App) activeViewCapturingInput() bool {
	switch a.activeView {
	case ViewAccounts:
		return a.accountsView.InputActive()
	case ViewTrades:
		return a.tradesView.InputActive()
	case ViewImport:
		return a.importView.InputActive()
	}
	return false
}

func (a *App) propagateSize() {
	// Reserve 3 lines for header + separator + help bar.
	contentH := a.state.Height - 3
	if contentH < 1 {
		contentH = 1
	}
	a.accountsView.Resize(a.state.Width, contentH)
	a.positionsView.Resize(a.state.Width, contentH)
	a.historyView.Resize(a.state.Width, contentH)
	a.tradesView.Resize(a.state.Width, contentH)
	a.analyticsView.Resize(a.state.Width, contentH)
	a.importView.Resize(a.state.Width, contentH)
}
