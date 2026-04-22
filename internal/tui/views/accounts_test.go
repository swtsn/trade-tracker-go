package views_test

import (
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	pb "trade-tracker-go/gen/tradetracker/v1"
	"trade-tracker-go/internal/tui/client"
	"trade-tracker-go/internal/tui/views"
)

func TestAccountsView_LoadPopulatesTable(t *testing.T) {
	fake := &client.Fake{
		Accounts: []*pb.Account{
			{Id: "acc1", Broker: "Tastytrade", AccountNumber: "12345"},
			{Id: "acc2", Broker: "Schwab", AccountNumber: "67890"},
		},
		Summaries: map[string]*pb.GetAccountSummaryResponse{
			"acc1": {RealizedPnl: "1234.56", WinRate: "0.75", PositionsClosed: 12},
			"acc2": {RealizedPnl: "-200.00", WinRate: "0.40", PositionsClosed: 5},
		},
	}

	state := views.SharedState{
		Accounts:          fake.Accounts,
		SelectedAccountID: views.AllAccountsID,
	}

	v := views.NewAccountsView(fake)
	v.Resize(120, 24)
	v, cmd := v.Update(views.LoadMsg{State: state}, state)
	require.NotNil(t, cmd)

	// Execute the load command synchronously.
	msg := cmd()
	v, _ = v.Update(msg, state)

	rendered := v.View()
	assert.Contains(t, rendered, "Tastytrade")
	assert.Contains(t, rendered, "Schwab")
}

// TestAccountsView_SummaryErrorShowsDashes verifies that a per-account summary
// error degrades gracefully: the account row is still shown but P&L fields are "—".
func TestAccountsView_SummaryErrorShowsDashes(t *testing.T) {
	fake := &client.Fake{
		Accounts: []*pb.Account{{Id: "acc1", Broker: "Tastytrade", AccountNumber: "12345"}},
		Err:      errSentinel,
	}

	state := views.SharedState{
		Accounts:          fake.Accounts,
		SelectedAccountID: views.AllAccountsID,
	}

	v := views.NewAccountsView(fake)
	v.Resize(120, 24)
	v, cmd := v.Update(views.LoadMsg{State: state}, state)
	msg := cmd()
	v, _ = v.Update(msg, state)

	rendered := v.View()
	// Account should still appear; summary columns show "—".
	assert.Contains(t, rendered, "Tastytrade")
	assert.Contains(t, rendered, "—")
}

func TestAccountsView_CreateFlow(t *testing.T) {
	fake := &client.Fake{
		Accounts:  []*pb.Account{},
		Summaries: map[string]*pb.GetAccountSummaryResponse{},
	}
	state := views.SharedState{Accounts: fake.Accounts}

	v := views.NewAccountsView(fake)
	v.Resize(120, 24)
	v, cmd := v.Update(views.LoadMsg{State: state}, state)
	v, _ = v.Update(cmd(), state)

	// Press 'n' to start create flow.
	v, _ = v.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("n")}, state)
	assert.Contains(t, v.View(), "Broker")
	assert.True(t, v.InputActive())

	// Type broker and advance.
	for _, r := range "tastytrade" {
		v, _ = v.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}}, state)
	}
	v, _ = v.Update(tea.KeyMsg{Type: tea.KeyEnter}, state)
	assert.Contains(t, v.View(), "Account Number")

	// Type account number and advance.
	for _, r := range "12345" {
		v, _ = v.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}}, state)
	}
	v, _ = v.Update(tea.KeyMsg{Type: tea.KeyEnter}, state)
	assert.Contains(t, v.View(), "Nickname")

	// Skip name and advance to confirm.
	v, _ = v.Update(tea.KeyMsg{Type: tea.KeyEnter}, state)
	assert.Contains(t, v.View(), "Confirm")
	assert.Contains(t, v.View(), "tastytrade")
	assert.Contains(t, v.View(), "12345")

	// Confirm — triggers RPC.
	v, cmd = v.Update(tea.KeyMsg{Type: tea.KeyEnter}, state)
	require.NotNil(t, cmd)
	v, _ = v.Update(cmd(), state)
	// After success it reloads; loading state is set.
	assert.True(t, len(fake.Accounts) == 1)
}

func TestAccountsView_CreateFlow_Esc(t *testing.T) {
	fake := &client.Fake{Accounts: []*pb.Account{}, Summaries: map[string]*pb.GetAccountSummaryResponse{}}
	state := views.SharedState{Accounts: fake.Accounts}

	v := views.NewAccountsView(fake)
	v.Resize(120, 24)
	v, cmd := v.Update(views.LoadMsg{State: state}, state)
	v, _ = v.Update(cmd(), state)

	v, _ = v.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("n")}, state)
	// Esc cancels back to idle.
	v, _ = v.Update(tea.KeyMsg{Type: tea.KeyEsc}, state)
	assert.False(t, v.InputActive())
}

func TestAccountsView_RenameFlow(t *testing.T) {
	fake := &client.Fake{
		Accounts: []*pb.Account{
			{Id: "a1", Broker: "tastytrade", AccountNumber: "12345", Name: "Old"},
		},
		Summaries: map[string]*pb.GetAccountSummaryResponse{"a1": {}},
	}
	state := views.SharedState{Accounts: fake.Accounts}

	v := views.NewAccountsView(fake)
	v.Resize(120, 24)
	v, cmd := v.Update(views.LoadMsg{State: state}, state)
	v, _ = v.Update(cmd(), state)

	// Press 'r' to rename selected account.
	v, _ = v.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("r")}, state)
	assert.Contains(t, v.View(), "Rename Account")
	assert.True(t, v.InputActive())

	// The input is pre-populated with "Old" (3 chars). Delete it before typing.
	for i := 0; i < 3; i++ {
		v, _ = v.Update(tea.KeyMsg{Type: tea.KeyBackspace}, state)
	}
	for _, r := range "New" {
		v, _ = v.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}}, state)
	}
	v, cmd = v.Update(tea.KeyMsg{Type: tea.KeyEnter}, state)
	require.NotNil(t, cmd)

	// Execute the rename RPC; verify the correct name was sent.
	v, loadCmd := v.Update(cmd(), state)
	assert.Equal(t, "New", fake.Accounts[0].Name)

	// Execute the post-mutation refresh.
	if loadCmd != nil {
		v, _ = v.Update(loadCmd(), state)
	}
	assert.False(t, v.InputActive())
}

func TestAccountsView_KeyDelegatedToTable(t *testing.T) {
	fake := &client.Fake{
		Accounts: []*pb.Account{
			{Id: "acc1", Broker: "Tastytrade", AccountNumber: "12345"},
			{Id: "acc2", Broker: "Schwab", AccountNumber: "67890"},
		},
		Summaries: map[string]*pb.GetAccountSummaryResponse{
			"acc1": {},
			"acc2": {},
		},
	}
	state := views.SharedState{Accounts: fake.Accounts}

	v := views.NewAccountsView(fake)
	v.Resize(120, 24)
	v, cmd := v.Update(views.LoadMsg{State: state}, state)
	v, _ = v.Update(cmd(), state)

	// Sending a down key should not panic or error.
	v, _ = v.Update(tea.KeyMsg{Type: tea.KeyDown}, state)
	assert.NotEmpty(t, v.View())
}
