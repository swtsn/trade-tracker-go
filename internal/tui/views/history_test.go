package views_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/types/known/timestamppb"

	pb "trade-tracker-go/gen/tradetracker/v1"
	"trade-tracker-go/internal/tui/client"
	"trade-tracker-go/internal/tui/views"
)

func TestHistoryView_LoadClosedPositions(t *testing.T) {
	closed := timestamppb.Now()
	fake := &client.Fake{
		Positions: map[string][]*pb.Position{
			"acc1": {
				{
					Id:               "p1",
					UnderlyingSymbol: "AAPL",
					StrategyType:     pb.StrategyType_STRATEGY_TYPE_COVERED_CALL,
					CostBasis:        "150.00",
					RealizedPnl:      "75.00",
					OpenedAt:         timestamppb.Now(),
					ClosedAt:         closed,
				},
			},
		},
	}

	state := views.SharedState{
		Accounts:          []*pb.Account{{Id: "acc1"}},
		SelectedAccountID: "acc1",
	}

	v := views.NewHistoryView(fake)
	v.Resize(120, 24)
	v, cmd := v.Update(views.LoadMsg{State: state}, state)
	require.NotNil(t, cmd)
	v, _ = v.Update(cmd(), state)

	rendered := v.View()
	assert.Contains(t, rendered, "AAPL")
	assert.Contains(t, rendered, "CC")
}

func TestHistoryView_AllAccountsFansOut(t *testing.T) {
	closed := timestamppb.Now()
	fake := &client.Fake{
		Positions: map[string][]*pb.Position{
			"acc1": {{Id: "p1", UnderlyingSymbol: "AAPL", OpenedAt: timestamppb.Now(), ClosedAt: closed}},
			"acc2": {{Id: "p2", UnderlyingSymbol: "TSLA", OpenedAt: timestamppb.Now(), ClosedAt: closed}},
		},
	}

	state := views.SharedState{
		Accounts:          []*pb.Account{{Id: "acc1"}, {Id: "acc2"}},
		SelectedAccountID: views.AllAccountsID,
	}

	v := views.NewHistoryView(fake)
	v.Resize(120, 24)
	v, cmd := v.Update(views.LoadMsg{State: state}, state)
	v, _ = v.Update(cmd(), state)

	rendered := v.View()
	assert.Contains(t, rendered, "AAPL")
	assert.Contains(t, rendered, "TSLA")
}

func TestHistoryView_ErrorShowsMessage(t *testing.T) {
	fake := &client.Fake{Err: errSentinel}
	state := views.SharedState{
		Accounts:          []*pb.Account{{Id: "acc1"}},
		SelectedAccountID: "acc1",
	}

	v := views.NewHistoryView(fake)
	v.Resize(120, 24)
	v, cmd := v.Update(views.LoadMsg{State: state}, state)
	v, _ = v.Update(cmd(), state)

	assert.Contains(t, v.View(), "sentinel error")
}
