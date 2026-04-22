package views_test

import (
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/types/known/timestamppb"

	pb "trade-tracker-go/gen/tradetracker/v1"
	"trade-tracker-go/internal/tui/client"
	"trade-tracker-go/internal/tui/views"
)

func TestTradesView_Load(t *testing.T) {
	fake := &client.Fake{
		Trades: map[string][]*pb.Trade{
			"acc1": {
				{
					Id:               "t1",
					UnderlyingSymbol: "SPY",
					StrategyType:     pb.StrategyType_STRATEGY_TYPE_VERTICAL,
					ExecutedAt:       timestamppb.Now(),
				},
			},
		},
	}

	state := views.SharedState{
		Accounts:          []*pb.Account{{Id: "acc1"}},
		SelectedAccountID: "acc1",
	}

	v := views.NewTradesView(fake)
	v.Resize(120, 24)
	v, cmd := v.Update(views.LoadMsg{State: state}, state)
	require.NotNil(t, cmd)
	v, _ = v.Update(cmd(), state)

	rendered := v.View()
	assert.Contains(t, rendered, "SPY")
	assert.Contains(t, rendered, "Vert")
}

func TestTradesView_SymbolFilterRoundTrip(t *testing.T) {
	fake := &client.Fake{
		Trades: map[string][]*pb.Trade{
			"acc1": {
				{Id: "t1", UnderlyingSymbol: "SPY", ExecutedAt: timestamppb.Now()},
				{Id: "t2", UnderlyingSymbol: "AAPL", ExecutedAt: timestamppb.Now()},
			},
		},
	}

	state := views.SharedState{
		Accounts:          []*pb.Account{{Id: "acc1"}},
		SelectedAccountID: "acc1",
	}

	v := views.NewTradesView(fake)
	v.Resize(120, 24)
	// Initial load.
	v, cmd := v.Update(views.LoadMsg{State: state}, state)
	v, _ = v.Update(cmd(), state)

	// Open filter with '/'.
	v, _ = v.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'/'}}, state)
	assert.True(t, v.InputActive(), "filter should be active after /")

	// Type "SPY".
	for _, ch := range "SPY" {
		v, _ = v.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{ch}}, state)
	}

	// Confirm with Enter — this triggers a reload.
	v, cmd = v.Update(tea.KeyMsg{Type: tea.KeyEnter}, state)
	assert.False(t, v.InputActive(), "filter should close after Enter")
	require.NotNil(t, cmd)

	// Execute the reload.
	v, _ = v.Update(cmd(), state)

	// Only SPY should be in the fake's filtered result.
	rendered := v.View()
	assert.Contains(t, rendered, "SPY")
}

func TestTradesView_InputActiveWhileFilterOpen(t *testing.T) {
	fake := &client.Fake{}
	state := views.SharedState{
		Accounts:          []*pb.Account{{Id: "acc1"}},
		SelectedAccountID: "acc1",
	}

	v := views.NewTradesView(fake)
	assert.False(t, v.InputActive())

	v, _ = v.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'/'}}, state)
	assert.True(t, v.InputActive())

	v, _ = v.Update(tea.KeyMsg{Type: tea.KeyEsc}, state)
	assert.False(t, v.InputActive())
}

func TestTradesView_AllAccountsFansOut(t *testing.T) {
	fake := &client.Fake{
		Trades: map[string][]*pb.Trade{
			"acc1": {{Id: "t1", UnderlyingSymbol: "SPY", ExecutedAt: timestamppb.Now()}},
			"acc2": {{Id: "t2", UnderlyingSymbol: "TSLA", ExecutedAt: timestamppb.Now()}},
		},
	}

	state := views.SharedState{
		Accounts:          []*pb.Account{{Id: "acc1"}, {Id: "acc2"}},
		SelectedAccountID: views.AllAccountsID,
	}

	v := views.NewTradesView(fake)
	v.Resize(120, 24)
	v, cmd := v.Update(views.LoadMsg{State: state}, state)
	v, _ = v.Update(cmd(), state)

	rendered := v.View()
	assert.Contains(t, rendered, "SPY")
	assert.Contains(t, rendered, "TSLA")
}

func TestTradesView_ErrorShowsMessage(t *testing.T) {
	fake := &client.Fake{Err: errSentinel}
	state := views.SharedState{
		Accounts:          []*pb.Account{{Id: "acc1"}},
		SelectedAccountID: "acc1",
	}

	v := views.NewTradesView(fake)
	v.Resize(120, 24)
	v, cmd := v.Update(views.LoadMsg{State: state}, state)
	v, _ = v.Update(cmd(), state)

	assert.Contains(t, v.View(), "sentinel error")
}
