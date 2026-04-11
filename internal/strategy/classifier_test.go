package strategy

import (
	"testing"
	"time"

	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
	"trade-tracker-go/internal/domain"
)

var (
	exp1 = time.Date(2025, 12, 19, 0, 0, 0, 0, time.UTC)
	exp2 = time.Date(2026, 1, 16, 0, 0, 0, 0, time.UTC)

	s90  = decimal.NewFromInt(90)
	s95  = decimal.NewFromInt(95)
	s100 = decimal.NewFromInt(100)
	s105 = decimal.NewFromInt(105)
	s110 = decimal.NewFromInt(110)
	s115 = decimal.NewFromInt(115)

	q1 = decimal.NewFromInt(1)
	q2 = decimal.NewFromInt(2)
	q3 = decimal.NewFromInt(3)
)

func leg(ac domain.AssetClass, ot domain.OptionType, strike decimal.Decimal, exp time.Time, qty decimal.Decimal) LegShape {
	return LegShape{
		AssetClass: ac,
		OptionType: ot,
		Strike:     strike,
		Expiration: exp,
		Quantity:   qty,
	}
}

func call(strike decimal.Decimal, exp time.Time, qty decimal.Decimal) LegShape {
	return leg(domain.AssetClassEquityOption, domain.OptionTypeCall, strike, exp, qty)
}

func put(strike decimal.Decimal, exp time.Time, qty decimal.Decimal) LegShape {
	return leg(domain.AssetClassEquityOption, domain.OptionTypePut, strike, exp, qty)
}

func stock(qty decimal.Decimal) LegShape {
	return LegShape{AssetClass: domain.AssetClassEquity, Quantity: qty}
}

func future(qty decimal.Decimal) LegShape {
	return LegShape{AssetClass: domain.AssetClassFuture, Quantity: qty}
}

func TestClassifier(t *testing.T) {
	c := NewClassifier()

	tests := []struct {
		name   string
		legs   []LegShape
		expect domain.StrategyType
	}{
		// --- IronButterfly ---
		{
			name: "IronButterfly — exact match",
			legs: []LegShape{
				put(s90, exp1, q1),         // long put wing
				put(s100, exp1, q1.Neg()),  // short put at center
				call(s100, exp1, q1.Neg()), // short call at center (same strike)
				call(s110, exp1, q1),       // long call wing
			},
			expect: domain.StrategyIronButterfly,
		},
		{
			name: "IronButterfly — legs in different order",
			legs: []LegShape{
				call(s110, exp1, q1),
				put(s100, exp1, q1.Neg()),
				call(s100, exp1, q1.Neg()),
				put(s90, exp1, q1),
			},
			expect: domain.StrategyIronButterfly,
		},
		{
			name: "IronButterfly — short strikes differ → IronCondor",
			legs: []LegShape{
				put(s90, exp1, q1),
				put(s100, exp1, q1.Neg()),
				call(s105, exp1, q1.Neg()), // different short strike
				call(s110, exp1, q1),
			},
			expect: domain.StrategyIronCondor,
		},

		// --- IronCondor ---
		{
			name: "IronCondor — exact match",
			legs: []LegShape{
				put(s90, exp1, q1),
				put(s95, exp1, q1.Neg()),
				call(s105, exp1, q1.Neg()),
				call(s110, exp1, q1),
			},
			expect: domain.StrategyIronCondor,
		},
		{
			name: "IronCondor — legs in different order",
			legs: []LegShape{
				call(s110, exp1, q1),
				put(s95, exp1, q1.Neg()),
				put(s90, exp1, q1),
				call(s105, exp1, q1.Neg()),
			},
			expect: domain.StrategyIronCondor,
		},
		{
			name: "IronCondor — mixed expiries → Unknown",
			legs: []LegShape{
				put(s90, exp1, q1),
				put(s95, exp1, q1.Neg()),
				call(s105, exp2, q1.Neg()), // different expiry
				call(s110, exp2, q1),
			},
			expect: domain.StrategyUnknown,
		},

		// --- BrokenHeartButterfly ---
		{
			name: "BrokenHeartButterfly — exact match (calls)",
			legs: []LegShape{
				call(s90, exp1, q1),
				call(s95, exp1, q1.Neg()),
				call(s100, exp1, q1.Neg()),
				call(s110, exp1, q1),
			},
			expect: domain.StrategyBrokenHeartButterfly,
		},
		{
			name: "BrokenHeartButterfly — legs in different order",
			legs: []LegShape{
				call(s110, exp1, q1),
				call(s100, exp1, q1.Neg()),
				call(s90, exp1, q1),
				call(s95, exp1, q1.Neg()),
			},
			expect: domain.StrategyBrokenHeartButterfly,
		},
		{
			name: "BrokenHeartButterfly — mixed option types → not matched",
			legs: []LegShape{
				put(s90, exp1, q1),
				put(s95, exp1, q1.Neg()),
				call(s100, exp1, q1.Neg()),
				call(s110, exp1, q1),
			},
			expect: domain.StrategyIronCondor, // it's actually an IC
		},

		// --- Butterfly ---
		{
			name: "Butterfly — calls, equidistant",
			legs: []LegShape{
				call(s90, exp1, q1),
				call(s100, exp1, q2.Neg()),
				call(s110, exp1, q1),
			},
			expect: domain.StrategyButterfly,
		},
		{
			name: "Butterfly — puts, equidistant",
			legs: []LegShape{
				put(s90, exp1, q1),
				put(s100, exp1, q2.Neg()),
				put(s110, exp1, q1),
			},
			expect: domain.StrategyButterfly,
		},
		{
			name: "Butterfly — legs in different order",
			legs: []LegShape{
				call(s110, exp1, q1),
				call(s90, exp1, q1),
				call(s100, exp1, q2.Neg()),
			},
			expect: domain.StrategyButterfly,
		},
		{
			name: "Butterfly — asymmetric wings → BrokenWingButterfly",
			legs: []LegShape{
				call(s90, exp1, q1),
				call(s100, exp1, q2.Neg()),
				call(s115, exp1, q1), // wider upper wing
			},
			expect: domain.StrategyBrokenWingButterfly,
		},

		// --- BrokenWingButterfly ---
		{
			name: "BrokenWingButterfly — calls, asymmetric",
			legs: []LegShape{
				call(s90, exp1, q1),
				call(s100, exp1, q2.Neg()),
				call(s115, exp1, q1),
			},
			expect: domain.StrategyBrokenWingButterfly,
		},
		{
			name: "BrokenWingButterfly — legs in different order",
			legs: []LegShape{
				call(s115, exp1, q1),
				call(s100, exp1, q2.Neg()),
				call(s90, exp1, q1),
			},
			expect: domain.StrategyBrokenWingButterfly,
		},
		{
			name: "BrokenWingButterfly — equidistant → Butterfly",
			legs: []LegShape{
				call(s90, exp1, q1),
				call(s100, exp1, q2.Neg()),
				call(s110, exp1, q1),
			},
			expect: domain.StrategyButterfly,
		},

		// --- CoveredCall ---
		{
			name: "CoveredCall — stock long + call short",
			legs: []LegShape{
				stock(q1),
				call(s100, exp1, q1.Neg()),
			},
			expect: domain.StrategyCoveredCall,
		},
		{
			name: "CoveredCall — legs in different order",
			legs: []LegShape{
				call(s100, exp1, q1.Neg()),
				stock(q1),
			},
			expect: domain.StrategyCoveredCall,
		},
		{
			name: "CoveredCall — stock short + call short → Unknown",
			legs: []LegShape{
				stock(q1.Neg()),
				call(s100, exp1, q1.Neg()),
			},
			expect: domain.StrategyUnknown,
		},

		// --- Ratio ---
		{
			name: "Ratio — 2-leg 1-2-0 (different strikes)",
			legs: []LegShape{
				call(s100, exp1, q1),
				call(s105, exp1, q2.Neg()),
			},
			expect: domain.StrategyRatio,
		},
		{
			name: "Ratio — 2-leg 1-3-0",
			legs: []LegShape{
				call(s100, exp1, q1),
				call(s105, exp1, q3.Neg()),
			},
			expect: domain.StrategyRatio,
		},
		{
			name: "Ratio — 3-leg 1-1-1 (long at A, short at B and C)",
			legs: []LegShape{
				call(s90, exp1, q1),
				call(s100, exp1, q1.Neg()),
				call(s105, exp1, q1.Neg()),
			},
			expect: domain.StrategyRatio,
		},
		{
			name: "Ratio — put ratio",
			legs: []LegShape{
				put(s100, exp1, q1),
				put(s95, exp1, q2.Neg()),
			},
			expect: domain.StrategyRatio,
		},

		// --- BackRatio ---
		{
			name: "BackRatio — 2-leg 2-1-0",
			legs: []LegShape{
				call(s100, exp1, q2),
				call(s105, exp1, q1.Neg()),
			},
			expect: domain.StrategyBackRatio,
		},
		{
			name: "BackRatio — 2-leg 3-1-0",
			legs: []LegShape{
				call(s100, exp1, q3),
				call(s105, exp1, q1.Neg()),
			},
			expect: domain.StrategyBackRatio,
		},
		{
			name: "BackRatio — 3-leg (short at A, long at B and C)",
			legs: []LegShape{
				call(s90, exp1, q1.Neg()),
				call(s100, exp1, q1),
				call(s105, exp1, q1),
			},
			expect: domain.StrategyBackRatio,
		},
		{
			name: "BackRatio — put back ratio",
			legs: []LegShape{
				put(s95, exp1, q2),
				put(s100, exp1, q1.Neg()),
			},
			expect: domain.StrategyBackRatio,
		},
		{
			name: "BackRatio — equal qty → Vertical",
			legs: []LegShape{
				call(s100, exp1, q1),
				call(s105, exp1, q1.Neg()),
			},
			expect: domain.StrategyVertical,
		},

		// --- Straddle ---
		{
			name: "Straddle — short put + short call, same strike",
			legs: []LegShape{
				put(s100, exp1, q1.Neg()),
				call(s100, exp1, q1.Neg()),
			},
			expect: domain.StrategyStraddle,
		},
		{
			name: "Straddle — long put + long call, same strike",
			legs: []LegShape{
				put(s100, exp1, q1),
				call(s100, exp1, q1),
			},
			expect: domain.StrategyStraddle,
		},
		{
			name: "Straddle — different strikes → Strangle",
			legs: []LegShape{
				put(s95, exp1, q1.Neg()),
				call(s105, exp1, q1.Neg()),
			},
			expect: domain.StrategyStrangle,
		},

		// --- Strangle ---
		{
			name: "Strangle — short put + short call, different strikes",
			legs: []LegShape{
				put(s95, exp1, q1.Neg()),
				call(s105, exp1, q1.Neg()),
			},
			expect: domain.StrategyStrangle,
		},
		{
			name: "Strangle — legs in different order",
			legs: []LegShape{
				call(s105, exp1, q1.Neg()),
				put(s95, exp1, q1.Neg()),
			},
			expect: domain.StrategyStrangle,
		},
		{
			name: "Strangle — opposite directions → not Strangle",
			legs: []LegShape{
				put(s95, exp1, q1.Neg()),
				call(s105, exp1, q1),
			},
			expect: domain.StrategyUnknown,
		},
		{
			// put.Strike > call.Strike: inverted (guts) strangle, still a strangle.
			name: "Strangle — inverted (put above call) → Strangle",
			legs: []LegShape{
				put(s105, exp1, q1.Neg()),
				call(s95, exp1, q1.Neg()),
			},
			expect: domain.StrategyStrangle,
		},

		// --- Vertical ---
		{
			name: "Vertical — call vertical",
			legs: []LegShape{
				call(s100, exp1, q1),
				call(s105, exp1, q1.Neg()),
			},
			expect: domain.StrategyVertical,
		},
		{
			name: "Vertical — put vertical",
			legs: []LegShape{
				put(s95, exp1, q1.Neg()),
				put(s100, exp1, q1),
			},
			expect: domain.StrategyVertical,
		},
		{
			name: "Vertical — legs in different order",
			legs: []LegShape{
				call(s105, exp1, q1.Neg()),
				call(s100, exp1, q1),
			},
			expect: domain.StrategyVertical,
		},
		{
			name: "Vertical — same direction → Unknown",
			legs: []LegShape{
				call(s100, exp1, q1.Neg()),
				call(s105, exp1, q1.Neg()),
			},
			expect: domain.StrategyUnknown,
		},

		// --- Calendar ---
		{
			name: "Calendar — same strike, different expiry",
			legs: []LegShape{
				call(s100, exp1, q1.Neg()),
				call(s100, exp2, q1),
			},
			expect: domain.StrategyCalendar,
		},
		{
			name: "Calendar — put calendar",
			legs: []LegShape{
				put(s100, exp1, q1.Neg()),
				put(s100, exp2, q1),
			},
			expect: domain.StrategyCalendar,
		},
		{
			name: "Calendar — different strike → Diagonal",
			legs: []LegShape{
				call(s100, exp1, q1.Neg()),
				call(s105, exp2, q1),
			},
			expect: domain.StrategyDiagonal,
		},

		// --- Diagonal ---
		{
			name: "Diagonal — different strike and expiry",
			legs: []LegShape{
				call(s100, exp1, q1.Neg()),
				call(s105, exp2, q1),
			},
			expect: domain.StrategyDiagonal,
		},
		{
			name: "Diagonal — put diagonal",
			legs: []LegShape{
				put(s100, exp1, q1.Neg()),
				put(s95, exp2, q1),
			},
			expect: domain.StrategyDiagonal,
		},
		{
			name: "Diagonal — same expiry → Vertical",
			legs: []LegShape{
				call(s100, exp1, q1.Neg()),
				call(s105, exp1, q1),
			},
			expect: domain.StrategyVertical,
		},

		// --- Single ---
		{
			name:   "Single — long call",
			legs:   []LegShape{call(s100, exp1, q1)},
			expect: domain.StrategySingle,
		},
		{
			name:   "Single — short put",
			legs:   []LegShape{put(s100, exp1, q1.Neg())},
			expect: domain.StrategySingle,
		},

		// --- Stock ---
		{
			name:   "Stock — long equity",
			legs:   []LegShape{stock(q1)},
			expect: domain.StrategyStock,
		},
		{
			name:   "Stock — short equity",
			legs:   []LegShape{stock(q1.Neg())},
			expect: domain.StrategyStock,
		},

		// --- Future ---
		{
			name:   "Future — long future",
			legs:   []LegShape{future(q1)},
			expect: domain.StrategyFuture,
		},

		// --- Short butterfly variants ---
		{
			name: "Butterfly — short (sell outer, buy middle)",
			legs: []LegShape{
				call(s90, exp1, q1.Neg()),
				call(s100, exp1, q2),
				call(s110, exp1, q1.Neg()),
			},
			expect: domain.StrategyButterfly,
		},
		{
			name: "BrokenWingButterfly — short variant",
			legs: []LegShape{
				call(s90, exp1, q1.Neg()),
				call(s100, exp1, q2),
				call(s115, exp1, q1.Neg()),
			},
			expect: domain.StrategyBrokenWingButterfly,
		},
		{
			name: "BrokenHeartButterfly — short variant (inner longs, outer shorts)",
			legs: []LegShape{
				call(s90, exp1, q1.Neg()),
				call(s95, exp1, q1),
				call(s100, exp1, q1),
				call(s110, exp1, q1.Neg()),
			},
			expect: domain.StrategyBrokenHeartButterfly,
		},

		// --- IronCondor inverted (call spread below put spread) ---
		{
			name: "IronCondor — inverted (call spread below put spread) → Unknown",
			legs: []LegShape{
				put(s100, exp1, q1),       // longPut
				put(s105, exp1, q1.Neg()), // shortPut
				call(s95, exp1, q1.Neg()), // shortCall < shortPut → inverted
				call(s100, exp1, q1),      // longCall
			},
			expect: domain.StrategyUnknown,
		},

		// --- Ratio/BackRatio same strike (valid — strikes need not differ) ---
		{
			name: "Ratio — same strike, unequal qty",
			legs: []LegShape{
				call(s100, exp1, q1),
				call(s100, exp1, q2.Neg()),
			},
			expect: domain.StrategyRatio,
		},
		{
			name: "BackRatio — same strike, unequal qty",
			legs: []LegShape{
				call(s100, exp1, q2),
				call(s100, exp1, q1.Neg()),
			},
			expect: domain.StrategyBackRatio,
		},

		// --- FutureOption single ---
		{
			name: "Single — future option",
			legs: []LegShape{
				leg(domain.AssetClassFutureOption, domain.OptionTypeCall, s100, exp1, q1),
			},
			expect: domain.StrategySingle,
		},

		// --- Butterfly quantity guards ---
		{
			// long 1 @ 90, short 3 @ 100, long 2 @ 110: outer qty unequal → not Butterfly
			name: "Butterfly — unequal outer qty → Unknown",
			legs: []LegShape{
				call(s90, exp1, q1),
				call(s100, exp1, q3.Neg()),
				call(s110, exp1, q2),
			},
			expect: domain.StrategyUnknown,
		},
		{
			// Same but asymmetric wings — still not BWB
			name: "BrokenWingButterfly — unequal outer qty → Unknown",
			legs: []LegShape{
				call(s90, exp1, q1),
				call(s100, exp1, q3.Neg()),
				call(s115, exp1, q2),
			},
			expect: domain.StrategyUnknown,
		},

		// --- BrokenHeartButterfly quantity guard ---
		{
			// 1-3-3-1 shape: outer qty != inner qty → not a broken heart butterfly.
			name: "BrokenHeartButterfly — unequal outer vs inner qty → Unknown",
			legs: []LegShape{
				call(s90, exp1, q1),
				call(s95, exp1, q3.Neg()),
				call(s100, exp1, q3.Neg()),
				call(s110, exp1, q1),
			},
			expect: domain.StrategyUnknown,
		},

		// --- 3-leg BackRatio structurally distinct from butterfly ---
		{
			// short@90, short@100, long@110: two shorts, one long → Ratio
			name: "Ratio — 3-leg (short@90, short@100, long@110)",
			legs: []LegShape{
				call(s90, exp1, q1.Neg()),
				call(s100, exp1, q1.Neg()),
				call(s110, exp1, q1),
			},
			expect: domain.StrategyRatio,
		},
		{
			// long@90, long@100, short@110: two longs, one short → BackRatio
			name: "BackRatio — 3-leg (long@90, long@100, short@110)",
			legs: []LegShape{
				call(s90, exp1, q1),
				call(s100, exp1, q1),
				call(s110, exp1, q1.Neg()),
			},
			expect: domain.StrategyBackRatio,
		},

		// --- Multi-leg FutureOption strategies ---
		{
			name: "Vertical — future option vertical",
			legs: []LegShape{
				leg(domain.AssetClassFutureOption, domain.OptionTypeCall, s100, exp1, q1),
				leg(domain.AssetClassFutureOption, domain.OptionTypeCall, s105, exp1, q1.Neg()),
			},
			expect: domain.StrategyVertical,
		},
		{
			name: "IronCondor — future option iron condor",
			legs: []LegShape{
				leg(domain.AssetClassFutureOption, domain.OptionTypePut, s90, exp1, q1),
				leg(domain.AssetClassFutureOption, domain.OptionTypePut, s95, exp1, q1.Neg()),
				leg(domain.AssetClassFutureOption, domain.OptionTypeCall, s105, exp1, q1.Neg()),
				leg(domain.AssetClassFutureOption, domain.OptionTypeCall, s110, exp1, q1),
			},
			expect: domain.StrategyIronCondor,
		},

		// --- >maxLegs guard ---
		{
			name: "Unknown — more than 4 legs",
			legs: []LegShape{
				call(s90, exp1, q1),
				call(s95, exp1, q1.Neg()),
				call(s100, exp1, q1.Neg()),
				call(s105, exp1, q1),
				call(s110, exp1, q1),
			},
			expect: domain.StrategyUnknown,
		},

		// --- Unknown ---
		{
			name:   "Unknown — empty legs",
			legs:   []LegShape{},
			expect: domain.StrategyUnknown,
		},
		{
			name: "Unknown — same direction vertical",
			legs: []LegShape{
				call(s100, exp1, q1),
				call(s105, exp1, q1),
			},
			expect: domain.StrategyUnknown,
		},
		{
			name: "Unknown — Ratio with equal quantities → Vertical",
			legs: []LegShape{
				call(s100, exp1, q3),
				call(s105, exp1, q3.Neg()),
			},
			expect: domain.StrategyVertical,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := c.Classify(tt.legs)
			assert.Equal(t, tt.expect, got)
		})
	}
}

func TestFromTransactions(t *testing.T) {
	txs := []domain.Transaction{
		{
			Action:         domain.ActionBTO,
			PositionEffect: domain.PositionEffectOpening,
			Quantity:       q1,
			Instrument: domain.Instrument{
				AssetClass: domain.AssetClassEquityOption,
				Option: &domain.OptionDetails{
					OptionType: domain.OptionTypeCall,
					Strike:     s100,
					Expiration: exp1,
				},
			},
		},
		{
			Action:         domain.ActionSTO,
			PositionEffect: domain.PositionEffectOpening,
			Quantity:       q1,
			Instrument: domain.Instrument{
				AssetClass: domain.AssetClassEquityOption,
				Option: &domain.OptionDetails{
					OptionType: domain.OptionTypePut,
					Strike:     s95,
					Expiration: exp1,
				},
			},
		},
		{
			// Closing leg — must be excluded
			Action:         domain.ActionBTC,
			PositionEffect: domain.PositionEffectClosing,
			Quantity:       q1,
			Instrument: domain.Instrument{
				AssetClass: domain.AssetClassEquityOption,
				Option: &domain.OptionDetails{
					OptionType: domain.OptionTypeCall,
					Strike:     s100,
					Expiration: exp1,
				},
			},
		},
	}

	legs := FromTransactions(txs)
	assert.Len(t, legs, 2)
	assert.True(t, legs[0].Quantity.IsPositive())
	assert.Equal(t, domain.OptionTypeCall, legs[0].OptionType)
	assert.True(t, legs[1].Quantity.IsNegative())
	assert.Equal(t, domain.OptionTypePut, legs[1].OptionType)
}

func TestFromTransactions_AllClosing(t *testing.T) {
	txs := []domain.Transaction{
		{
			Action:         domain.ActionBTC,
			PositionEffect: domain.PositionEffectClosing,
			Quantity:       q1,
			Instrument: domain.Instrument{
				AssetClass: domain.AssetClassEquityOption,
				Option: &domain.OptionDetails{
					OptionType: domain.OptionTypeCall,
					Strike:     s100,
					Expiration: exp1,
				},
			},
		},
	}
	legs := FromTransactions(txs)
	assert.Len(t, legs, 0)
}

func TestFromTransactions_NonPositiveQuantity(t *testing.T) {
	zero := decimal.NewFromInt(0)
	neg := decimal.NewFromInt(-1)
	txs := []domain.Transaction{
		{
			Action:         domain.ActionBTO,
			PositionEffect: domain.PositionEffectOpening,
			Quantity:       zero,
			Instrument:     domain.Instrument{AssetClass: domain.AssetClassEquity},
		},
		{
			Action:         domain.ActionBTO,
			PositionEffect: domain.PositionEffectOpening,
			Quantity:       neg,
			Instrument:     domain.Instrument{AssetClass: domain.AssetClassEquity},
		},
	}
	legs := FromTransactions(txs)
	assert.Len(t, legs, 0)
}

func TestFromTransactions_ActionBuyAndSell(t *testing.T) {
	txs := []domain.Transaction{
		{
			// Stock buy — uses ActionBuy, not ActionBTO
			Action:         domain.ActionBuy,
			PositionEffect: domain.PositionEffectOpening,
			Quantity:       q1,
			Instrument:     domain.Instrument{AssetClass: domain.AssetClassEquity},
		},
		{
			// Future sell — uses ActionSell, not ActionSTO
			Action:         domain.ActionSell,
			PositionEffect: domain.PositionEffectOpening,
			Quantity:       q1,
			Instrument:     domain.Instrument{AssetClass: domain.AssetClassFuture},
		},
	}

	legs := FromTransactions(txs)
	assert.Len(t, legs, 2)
	assert.True(t, legs[0].Quantity.IsPositive())
	assert.Equal(t, domain.AssetClassEquity, legs[0].AssetClass)
	assert.True(t, legs[1].Quantity.IsNegative())
	assert.Equal(t, domain.AssetClassFuture, legs[1].AssetClass)
}
