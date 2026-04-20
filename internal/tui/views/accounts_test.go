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
