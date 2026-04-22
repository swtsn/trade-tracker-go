package views

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/charmbracelet/bubbles/filepicker"
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
	stepIdle       importStep = iota
	stepFilePicker            // user browses and selects a CSV file
	stepConfirm               // user reviews account + file + broker before running
	stepRunning               // import in progress
	stepDone                  // import finished (success or error)
)

// maxImportFileBytes is the upper bound for CSV files accepted by the import flow.
const maxImportFileBytes = 50 << 20 // 50 MiB

// ImportView handles the CSV import flow.
type ImportView struct {
	client               client.Client
	step                 importStep
	fp                   filepicker.Model
	baseDir              string // starting directory for the file picker; defaults to os.Getwd()
	csvPath              string
	accountLabel         string // display label captured at confirm entry
	accountBroker        string // broker string from the selected account
	blockedByAllAccounts bool   // set when user tries to start with All Accounts active
	result               *ImportDoneMsg
	width                int
	height               int
}

var (
	importTitleStyle   = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("12"))
	importDimStyle     = lipgloss.NewStyle().Foreground(lipgloss.Color("8"))
	importWarningStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("11"))
)

func NewImportView(c client.Client) ImportView {
	return ImportView{client: c}
}

// NewImportViewAt creates an ImportView where the file picker starts in dir.
// Intended for testing; production code should use NewImportView.
func NewImportViewAt(c client.Client, dir string) ImportView {
	return ImportView{client: c, baseDir: dir}
}

// InputActive reports whether the view is currently capturing keyboard input.
// When true, the root app must not intercept global hotkeys.
func (v ImportView) InputActive() bool {
	return v.step == stepFilePicker || v.step == stepRunning
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

	// Pass non-key messages to the filepicker (e.g. the internal readDirMsg).
	if v.step == stepFilePicker {
		var cmd tea.Cmd
		v.fp, cmd = v.fp.Update(msg)
		return v, cmd
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
			v.step = stepFilePicker
			v.fp = v.newFilePicker(v.height - 4)
			return v, v.fp.Init()
		}

	case stepFilePicker:
		// Esc cancels the picker and returns to idle; all other keys go to the filepicker.
		if msg.String() == "esc" {
			v.step = stepIdle
			return v, nil
		}
		var cmd tea.Cmd
		v.fp, cmd = v.fp.Update(msg)
		if ok, path := v.fp.DidSelectFile(msg); ok {
			v.csvPath = path
			if acc := state.SelectedAccount(); acc != nil {
				v.accountLabel = AccountLabel(acc)
				v.accountBroker = acc.Broker
			} else {
				v.accountLabel = state.SelectedAccountID
				v.accountBroker = ""
			}
			v.step = stepConfirm
		}
		return v, cmd

	case stepConfirm:
		switch msg.String() {
		case "esc":
			v.step = stepFilePicker
			return v, nil
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

	case stepFilePicker:
		return strings.Join([]string{
			importTitleStyle.Render("CSV Import — Select File"),
			importDimStyle.Render(v.fp.CurrentDirectory),
			"",
			v.fp.View(),
			importDimStyle.Render("↑↓/jk navigate  l/→/Enter open  h/←/Bksp go up  Enter select  Esc cancel"),
		}, "\n")

	case stepConfirm:
		return strings.Join([]string{
			importTitleStyle.Render("CSV Import — Confirm"),
			"",
			fmt.Sprintf("  Account: %s", v.accountLabel),
			fmt.Sprintf("  Broker:  %s", v.accountBroker),
			fmt.Sprintf("  File:    %s", v.csvPath),
			"",
			importWarningStyle.Render("Press Enter or 'y' to import, 'n' or Esc to go back."),
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
	v.fp.SetHeight(h - 4)
}

// newFilePicker creates a filepicker filtered to .csv files.
// It starts in v.baseDir if set, otherwise the working directory.
func (v ImportView) newFilePicker(height int) filepicker.Model {
	fp := filepicker.New()
	fp.AllowedTypes = []string{".csv"}
	fp.ShowPermissions = false
	fp.AutoHeight = false
	fp.SetHeight(height)
	dir := v.baseDir
	if dir == "" {
		var err error
		dir, err = os.Getwd()
		if err != nil {
			dir = "."
		}
	}
	fp.CurrentDirectory = dir
	return fp
}

// brokerEnum maps an account broker string (e.g. "tastytrade") to the import enum.
// Returns BROKER_UNSPECIFIED for unrecognised values; the server will reject the import.
func brokerEnum(s string) pb.Broker {
	switch strings.ToLower(s) {
	case "tastytrade":
		return pb.Broker_BROKER_TASTYTRADE
	case "schwab":
		return pb.Broker_BROKER_SCHWAB
	default:
		return pb.Broker_BROKER_UNSPECIFIED
	}
}

func (v ImportView) runImport(state SharedState) tea.Cmd {
	c := v.client
	path := v.csvPath
	broker := brokerEnum(v.accountBroker)
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
