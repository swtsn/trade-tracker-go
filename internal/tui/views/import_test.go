package views_test

import (
	"os"
	"path/filepath"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	pb "trade-tracker-go/gen/tradetracker/v1"
	"trade-tracker-go/internal/tui/client"
	"trade-tracker-go/internal/tui/views"
)

func TestImportView_RequiresSpecificAccount(t *testing.T) {
	fake := &client.Fake{}
	state := views.SharedState{
		SelectedAccountID: views.AllAccountsID, // All Accounts
	}

	v := views.NewImportView(fake)
	// Pressing Enter when no specific account is selected should not advance.
	v, _ = v.Update(tea.KeyMsg{Type: tea.KeyEnter}, state)

	rendered := v.View()
	assert.Contains(t, rendered, "Import")
	assert.NotContains(t, rendered, "Select File")
}

func TestImportView_AdvancesToFilePickerWithSpecificAccount(t *testing.T) {
	fake := &client.Fake{}
	state := views.SharedState{SelectedAccountID: "acc1"}

	v := views.NewImportView(fake)
	v, _ = v.Update(tea.KeyMsg{Type: tea.KeyEnter}, state)

	rendered := v.View()
	assert.Contains(t, rendered, "Select File")
}

func TestImportView_EscapeFromFilePickerGoesIdle(t *testing.T) {
	fake := &client.Fake{}
	state := views.SharedState{SelectedAccountID: "acc1"}

	v := views.NewImportView(fake)
	v, _ = v.Update(tea.KeyMsg{Type: tea.KeyEnter}, state) // → stepFilePicker
	v, _ = v.Update(tea.KeyMsg{Type: tea.KeyEsc}, state)   // → stepIdle

	rendered := v.View()
	assert.NotContains(t, rendered, "Select File")
}

func TestImportView_ConfirmStepReached(t *testing.T) {
	// Create a temp directory with a CSV file so we can simulate file selection.
	tmpDir := t.TempDir()
	csvFile := filepath.Join(tmpDir, "trades.csv")
	require.NoError(t, os.WriteFile(csvFile, []byte("header\n"), 0644))

	account := &pb.Account{Id: "acc1", Broker: "tastytrade", AccountNumber: "12345"}
	fake := &client.Fake{}
	state := views.SharedState{
		SelectedAccountID: "acc1",
		Accounts:          []*pb.Account{account},
	}

	v := views.NewImportViewAt(fake, tmpDir)

	// Enter the flow; Init() returns a readDir command — run it synchronously.
	var cmd tea.Cmd
	v, cmd = v.Update(tea.KeyMsg{Type: tea.KeyEnter}, state) // → stepFilePicker
	require.NotNil(t, cmd)
	msg := cmd()                // readDir tmpDir
	v, _ = v.Update(msg, state) // filepicker loads the directory entries

	// Press Enter to select the first (and only) file → stepConfirm.
	v, _ = v.Update(tea.KeyMsg{Type: tea.KeyEnter}, state)

	rendered := v.View()
	assert.Contains(t, rendered, "Confirm")
	assert.Contains(t, rendered, "tastytrade")
	assert.Contains(t, rendered, "trades.csv")
}

func TestImportView_DoneMessageShowsSummary(t *testing.T) {
	fake := &client.Fake{}
	state := views.SharedState{SelectedAccountID: "acc1"}

	v := views.NewImportView(fake)
	v, _ = v.Update(views.ImportDoneMsg{Imported: 10, Skipped: 2, Failed: 0}, state)

	rendered := v.View()
	assert.Contains(t, rendered, "Import Complete")
	assert.Contains(t, rendered, "10")
	assert.Contains(t, rendered, "2")
}

func TestImportView_DoneWithErrors(t *testing.T) {
	fake := &client.Fake{}
	state := views.SharedState{SelectedAccountID: "acc1"}

	v := views.NewImportView(fake)
	v, _ = v.Update(views.ImportDoneMsg{
		Imported: 5,
		Failed:   1,
		Errors:   []string{"trade XYZ failed: invalid amount"},
	}, state)

	rendered := v.View()
	assert.Contains(t, rendered, "trade XYZ failed")
}
