package views_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	pb "trade-tracker-go/gen/tradetracker/v1"
	"trade-tracker-go/internal/tui/client"
	"trade-tracker-go/internal/tui/views"
)

func TestAnalyticsView_LoadSummary(t *testing.T) {
	fake := &client.Fake{
		Accounts: []*pb.Account{
			{Id: "acc1", Broker: "Tastytrade", AccountNumber: "12345"},
		},
		Summaries: map[string]*pb.GetAccountSummaryResponse{
			"acc1": {
				RealizedPnl:     "3200.00",
				WinRate:         "0.80",
				PositionsClosed: 20,
				CloseFees:       "45.00",
			},
		},
	}

	state := views.SharedState{
		Accounts:          fake.Accounts,
		SelectedAccountID: "acc1",
	}

	v := views.NewAnalyticsView(fake)
	v.Resize(120, 24)
	v, cmd := v.Update(views.LoadMsg{State: state}, state)
	require.NotNil(t, cmd)
	v, _ = v.Update(cmd(), state)

	rendered := v.View()
	assert.Contains(t, rendered, "80%")
	assert.Contains(t, rendered, "20")
}

func TestAnalyticsView_NilSummaryShowsDashes(t *testing.T) {
	// Summaries map does not contain "acc1" — GetAccountSummary returns nil, nil.
	fake := &client.Fake{
		Accounts:  []*pb.Account{{Id: "acc1", Broker: "Tastytrade", AccountNumber: "12345"}},
		Summaries: map[string]*pb.GetAccountSummaryResponse{},
	}

	state := views.SharedState{
		Accounts:          fake.Accounts,
		SelectedAccountID: "acc1",
	}

	v := views.NewAnalyticsView(fake)
	v.Resize(120, 24)
	v, cmd := v.Update(views.LoadMsg{State: state}, state)
	v, _ = v.Update(cmd(), state)

	rendered := v.View()
	// Should render the account row without panicking; P&L fields show "—".
	assert.Contains(t, rendered, "—")
}

func TestAnalyticsView_AllAccountsFansOut(t *testing.T) {
	fake := &client.Fake{
		Accounts: []*pb.Account{
			{Id: "acc1", Broker: "Tastytrade", AccountNumber: "111"},
			{Id: "acc2", Broker: "Schwab", AccountNumber: "222"},
		},
		Summaries: map[string]*pb.GetAccountSummaryResponse{
			"acc1": {RealizedPnl: "1000.00", WinRate: "0.70", PositionsClosed: 10},
			"acc2": {RealizedPnl: "-500.00", WinRate: "0.30", PositionsClosed: 5},
		},
	}

	state := views.SharedState{
		Accounts:          fake.Accounts,
		SelectedAccountID: views.AllAccountsID,
	}

	v := views.NewAnalyticsView(fake)
	v.Resize(120, 24)
	v, cmd := v.Update(views.LoadMsg{State: state}, state)
	v, _ = v.Update(cmd(), state)

	rendered := v.View()
	assert.Contains(t, rendered, "70%")
	assert.Contains(t, rendered, "30%")
}
