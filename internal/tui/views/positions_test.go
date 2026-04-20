package views_test

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/types/known/timestamppb"

	pb "trade-tracker-go/gen/tradetracker/v1"
	"trade-tracker-go/internal/tui/client"
	"trade-tracker-go/internal/tui/views"
)

var errSentinel = errors.New("sentinel error")

func TestPositionsView_LoadOpenPositions(t *testing.T) {
	fake := &client.Fake{
		Positions: map[string][]*pb.Position{
			"acc1": {
				{
					Id:               "p1",
					AccountId:        "acc1",
					UnderlyingSymbol: "AAPL",
					StrategyType:     pb.StrategyType_STRATEGY_TYPE_COVERED_CALL,
					CostBasis:        "150.00",
					OpenedAt:         timestamppb.Now(),
				},
			},
		},
	}

	state := views.SharedState{
		Accounts:          []*pb.Account{{Id: "acc1"}},
		SelectedAccountID: "acc1",
	}

	v := views.NewPositionsView(fake)
	v.Resize(120, 24)
	v, cmd := v.Update(views.LoadMsg{State: state}, state)
	require.NotNil(t, cmd)
	msg := cmd()
	v, _ = v.Update(msg, state)

	rendered := v.View()
	assert.Contains(t, rendered, "AAPL")
	assert.Contains(t, rendered, "CC")
}

func TestPositionsView_AllAccounts_FansOut(t *testing.T) {
	fake := &client.Fake{
		Positions: map[string][]*pb.Position{
			"acc1": {{Id: "p1", UnderlyingSymbol: "AAPL", OpenedAt: timestamppb.Now()}},
			"acc2": {{Id: "p2", UnderlyingSymbol: "TSLA", OpenedAt: timestamppb.Now()}},
		},
	}

	state := views.SharedState{
		Accounts: []*pb.Account{
			{Id: "acc1"},
			{Id: "acc2"},
		},
		SelectedAccountID: views.AllAccountsID,
	}

	v := views.NewPositionsView(fake)
	v.Resize(120, 24)
	v, cmd := v.Update(views.LoadMsg{State: state}, state)
	msg := cmd()
	v, _ = v.Update(msg, state)

	rendered := v.View()
	assert.Contains(t, rendered, "AAPL")
	assert.Contains(t, rendered, "TSLA")
}

func TestPositionsView_ErrorShowsMessage(t *testing.T) {
	fake := &client.Fake{Err: errSentinel}
	state := views.SharedState{
		Accounts:          []*pb.Account{{Id: "acc1"}},
		SelectedAccountID: "acc1",
	}

	v := views.NewPositionsView(fake)
	v.Resize(120, 24)
	v, cmd := v.Update(views.LoadMsg{State: state}, state)
	msg := cmd()
	v, _ = v.Update(msg, state)

	rendered := v.View()
	assert.Contains(t, rendered, "sentinel error")
}
