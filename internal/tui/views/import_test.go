package views_test

import (
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/stretchr/testify/assert"

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
	assert.NotContains(t, rendered, "File Path")
}

func TestImportView_AdvancesToPathWithSpecificAccount(t *testing.T) {
	fake := &client.Fake{}
	state := views.SharedState{SelectedAccountID: "acc1"}

	v := views.NewImportView(fake)
	v, _ = v.Update(tea.KeyMsg{Type: tea.KeyEnter}, state)

	rendered := v.View()
	assert.Contains(t, rendered, "File Path")
}

func TestImportView_EscapeFromPathGoesIdle(t *testing.T) {
	fake := &client.Fake{}
	state := views.SharedState{SelectedAccountID: "acc1"}

	v := views.NewImportView(fake)
	v, _ = v.Update(tea.KeyMsg{Type: tea.KeyEnter}, state) // → stepPath
	v, _ = v.Update(tea.KeyMsg{Type: tea.KeyEsc}, state)   // → stepIdle

	rendered := v.View()
	assert.NotContains(t, rendered, "File Path")
}

func TestImportView_BrokerStepReached(t *testing.T) {
	fake := &client.Fake{}
	state := views.SharedState{SelectedAccountID: "acc1"}

	v := views.NewImportView(fake)
	v, _ = v.Update(tea.KeyMsg{Type: tea.KeyEnter}, state) // → stepPath

	// Type a path and press Enter.
	for _, ch := range "/tmp/test.csv" {
		v, _ = v.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{ch}}, state)
	}
	v, _ = v.Update(tea.KeyMsg{Type: tea.KeyEnter}, state) // → stepBroker

	rendered := v.View()
	assert.Contains(t, rendered, "Broker")
	assert.Contains(t, rendered, "Tastytrade")
	assert.Contains(t, rendered, "Schwab")
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
