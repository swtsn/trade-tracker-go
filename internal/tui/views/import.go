package views

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	pb "trade-tracker-go/gen/tradetracker/v1"
	"trade-tracker-go/internal/tui/client"
)

// ImportDoneMsg is sent when an import stream completes.
// Exported so tests can inject it directly.
type ImportDoneMsg struct {
	Imported uint32
	Skipped  uint32
	Failed   uint32
	Errors   []string
	Err      error
}

type importStep int

const (
	stepIdle importStep = iota
	stepPath
	stepBroker
	stepConfirm
	stepRunning
	stepDone
)

// maxImportFileBytes is the upper bound for CSV files accepted by the import flow.
const maxImportFileBytes = 50 << 20 // 50 MiB

// ImportView handles the CSV import flow.
type ImportView struct {
	client               client.Client
	step                 importStep
	pathInput            textinput.Model
	csvPath              string
	broker               pb.Broker // selected broker
	accountID            string    // captured at stepConfirm entry
	blockedByAllAccounts bool      // set when user tries to start with All Accounts active
	result               *ImportDoneMsg
	width                int
	height               int
}

var brokers = []struct {
	label string
	value pb.Broker
}{
	{"Tastytrade", pb.Broker_BROKER_TASTYTRADE},
	{"Schwab", pb.Broker_BROKER_SCHWAB},
}

var (
	importTitleStyle   = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("12"))
	importDimStyle     = lipgloss.NewStyle().Foreground(lipgloss.Color("8"))
	importWarningStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("11"))
	importBrokerCursor = lipgloss.NewStyle().Foreground(lipgloss.Color("12")).Render("▶ ")
)

func NewImportView(c client.Client) ImportView {
	ti := textinput.New()
	ti.Placeholder = "/path/to/transactions.csv"
	ti.CharLimit = 256
	return ImportView{client: c, pathInput: ti}
}

// InputActive reports whether the view is currently capturing keyboard input.
// When true, the root app must not intercept global hotkeys.
func (v ImportView) InputActive() bool {
	return v.step == stepPath || v.step == stepRunning
}

func (v ImportView) Update(msg tea.Msg, state SharedState) (ImportView, tea.Cmd) {
	switch msg := msg.(type) {
	case LoadMsg:
		// No state reset on navigation — user may return mid-flow intentionally.
		return v, nil

	case ImportDoneMsg:
		v.step = stepDone
		v.result = &msg
		return v, nil

	case tea.KeyMsg:
		return v.handleKey(msg, state)
	}
	return v, nil
}

func (v ImportView) handleKey(msg tea.KeyMsg, state SharedState) (ImportView, tea.Cmd) {
	switch v.step {
	case stepIdle:
		if msg.String() == "enter" || msg.String() == "i" {
			if state.SelectedAccountID == AllAccountsID {
				v.blockedByAllAccounts = true
				return v, nil
			}
			v.blockedByAllAccounts = false
			v.step = stepPath
			v.pathInput.SetValue("")
			v.pathInput.Focus()
			return v, textinput.Blink
		}

	case stepPath:
		switch msg.String() {
		case "esc":
			v.step = stepIdle
			v.pathInput.Blur()
		case "enter":
			v.csvPath = strings.TrimSpace(v.pathInput.Value())
			if v.csvPath != "" {
				v.pathInput.Blur()
				v.step = stepBroker
				v.broker = pb.Broker_BROKER_TASTYTRADE
			}
		default:
			var cmd tea.Cmd
			v.pathInput, cmd = v.pathInput.Update(msg)
			return v, cmd
		}

	case stepBroker:
		switch msg.String() {
		case "esc":
			v.step = stepPath
			v.pathInput.Focus()
			return v, textinput.Blink
		case "enter":
			v.accountID = state.SelectedAccountID
			v.step = stepConfirm
		case "up", "k":
			v.prevBroker()
		case "down", "j":
			v.nextBroker()
		}

	case stepConfirm:
		switch msg.String() {
		case "esc":
			v.step = stepBroker
		case "enter", "y":
			v.step = stepRunning
			return v, v.runImport(state)
		case "n":
			v.step = stepIdle
		}

	case stepDone:
		if msg.String() == "enter" || msg.String() == "esc" {
			v.step = stepIdle
			v.result = nil
		}
	}
	return v, nil
}

func (v ImportView) View() string {
	switch v.step {
	case stepIdle:
		lines := []string{
			importTitleStyle.Render("CSV Import"),
			"",
			importDimStyle.Render("Press Enter or 'i' to start an import."),
		}
		if v.blockedByAllAccounts {
			lines = append(lines, "",
				importWarningStyle.Render("Select a specific account ([ ]) before importing."),
			)
		}
		return strings.Join(lines, "\n")

	case stepPath:
		return strings.Join([]string{
			importTitleStyle.Render("CSV Import — File Path"),
			"",
			"Enter the path to the CSV file:",
			v.pathInput.View(),
			"",
			importDimStyle.Render("Enter to confirm  Esc to cancel"),
		}, "\n")

	case stepBroker:
		lines := []string{
			importTitleStyle.Render("CSV Import — Broker"),
			"",
			"Select broker:",
		}
		for _, b := range brokers {
			prefix := "  "
			if b.value == v.broker {
				prefix = importBrokerCursor
			}
			lines = append(lines, prefix+b.label)
		}
		lines = append(lines, "", importDimStyle.Render("↑↓ to select  Enter to confirm  Esc to go back"))
		return strings.Join(lines, "\n")

	case stepConfirm:
		brokerLabel := "—"
		for _, b := range brokers {
			if b.value == v.broker {
				brokerLabel = b.label
			}
		}
		return strings.Join([]string{
			importTitleStyle.Render("CSV Import — Confirm"),
			"",
			fmt.Sprintf("  Account: %s", v.accountID),
			fmt.Sprintf("  File:    %s", v.csvPath),
			fmt.Sprintf("  Broker:  %s", brokerLabel),
			"",
			importWarningStyle.Render("Press Enter or 'y' to import, 'n' or Esc to cancel."),
		}, "\n")

	case stepRunning:
		return "Importing... please wait."

	case stepDone:
		if v.result == nil {
			return ""
		}
		if v.result.Err != nil {
			return strings.Join([]string{
				importTitleStyle.Render("Import Failed"),
				"",
				errorViewStyle.Render(v.result.Err.Error()),
				"",
				importDimStyle.Render("Enter or Esc to return."),
			}, "\n")
		}
		lines := []string{
			importTitleStyle.Render("Import Complete"),
			"",
			fmt.Sprintf("  Imported: %d", v.result.Imported),
			fmt.Sprintf("  Skipped:  %d", v.result.Skipped),
			fmt.Sprintf("  Failed:   %d", v.result.Failed),
		}
		if len(v.result.Errors) > 0 {
			lines = append(lines, "", errorViewStyle.Render("Errors:"))
			for _, e := range v.result.Errors {
				lines = append(lines, "  • "+e)
			}
		}
		lines = append(lines, "", importDimStyle.Render("Enter or Esc to return."))
		return strings.Join(lines, "\n")
	}
	return ""
}

func (v *ImportView) Resize(w, h int) {
	v.width = w
	v.height = h
}

func (v *ImportView) nextBroker() {
	for i, b := range brokers {
		if b.value == v.broker {
			v.broker = brokers[(i+1)%len(brokers)].value
			return
		}
	}
}

func (v *ImportView) prevBroker() {
	for i, b := range brokers {
		if b.value == v.broker {
			v.broker = brokers[(i-1+len(brokers))%len(brokers)].value
			return
		}
	}
}

func (v ImportView) runImport(state SharedState) tea.Cmd {
	c := v.client
	path := v.csvPath
	broker := v.broker
	accountID := state.SelectedAccountID
	return func() tea.Msg {
		info, err := os.Stat(path)
		if err != nil {
			return ImportDoneMsg{Err: fmt.Errorf("stat file: %w", err)}
		}
		if !info.Mode().IsRegular() {
			return ImportDoneMsg{Err: fmt.Errorf("%s is not a regular file", path)}
		}
		if info.Size() > maxImportFileBytes {
			return ImportDoneMsg{Err: fmt.Errorf("file exceeds %d MiB limit", maxImportFileBytes>>20)}
		}

		data, err := os.ReadFile(path)
		if err != nil {
			return ImportDoneMsg{Err: fmt.Errorf("read file: %w", err)}
		}
		ch, err := c.ImportTransactions(context.Background(), client.ImportParams{
			AccountID: accountID,
			Broker:    broker,
			CSVData:   data,
		})
		if err != nil {
			return ImportDoneMsg{Err: fmt.Errorf("import: %w", err)}
		}

		var imported, skipped, failed uint32
		var errMsgs []string
		var streamErr error
		for ev := range ch {
			if ev.Err != nil {
				streamErr = ev.Err
				break
			}
			r := ev.Response
			imported += r.Imported
			skipped += r.Skipped
			failed += r.Failed
			for _, e := range r.Errors {
				errMsgs = append(errMsgs, e.Message)
			}
		}
		if streamErr != nil {
			return ImportDoneMsg{Err: fmt.Errorf("import stream: %w", streamErr)}
		}
		return ImportDoneMsg{
			Imported: imported,
			Skipped:  skipped,
			Failed:   failed,
			Errors:   errMsgs,
		}
	}
}
