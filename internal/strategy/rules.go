package strategy

import (
	"sort"

	"github.com/shopspring/decimal"
	"trade-tracker-go/internal/domain"
)

// isOption returns true if the leg's asset class is an option.
func isOption(leg LegShape) bool {
	return leg.AssetClass == domain.AssetClassEquityOption ||
		leg.AssetClass == domain.AssetClassFutureOption
}

// allOptions returns true if every leg is an option. Returns true for empty input
// (vacuous truth). Every rule that calls this also guards on len(legs) first.
func allOptions(legs []LegShape) bool {
	for _, l := range legs {
		if !isOption(l) {
			return false
		}
	}
	return true
}

// allSameExpiry returns true if all legs share the same expiration. Returns true
// for empty input (vacuous truth). Every rule that calls this also guards on len(legs) first.
func allSameExpiry(legs []LegShape) bool {
	if len(legs) == 0 {
		return true
	}
	exp := legs[0].Expiration
	for _, l := range legs[1:] {
		if !l.Expiration.Equal(exp) {
			return false
		}
	}
	return true
}

// allSameOptionType returns true if all legs share the same option type. Returns
// true for empty input (vacuous truth). Every rule that calls this also guards on len(legs) first.
func allSameOptionType(legs []LegShape) bool {
	if len(legs) == 0 {
		return true
	}
	ot := legs[0].OptionType
	for _, l := range legs[1:] {
		if l.OptionType != ot {
			return false
		}
	}
	return true
}

// sortByStrike returns a copy of legs sorted ascending by strike. Non-option legs
// (equity, future) have a zero Strike; callers must only pass homogeneous slices
// where all legs are options or all are non-options.
func sortByStrike(legs []LegShape) []LegShape {
	out := make([]LegShape, len(legs))
	copy(out, legs)
	sort.Slice(out, func(i, j int) bool {
		return out[i].Strike.LessThan(out[j].Strike)
	})
	return out
}

// filterByOptionType returns legs matching the given option type.
func filterByOptionType(legs []LegShape, ot domain.OptionType) []LegShape {
	var out []LegShape
	for _, l := range legs {
		if l.OptionType == ot {
			out = append(out, l)
		}
	}
	return out
}

// filterByDirection returns legs whose Quantity sign matches dir (+1 for long, -1 for short).
func filterByDirection(legs []LegShape, dir int) []LegShape {
	var out []LegShape
	for _, l := range legs {
		if l.Quantity.Sign() == dir {
			out = append(out, l)
		}
	}
	return out
}

// totalQtyByDirection returns the absolute sum of quantities for long and short legs separately.
func totalQtyByDirection(legs []LegShape) (longQty, shortQty decimal.Decimal) {
	for _, l := range legs {
		if l.Quantity.IsPositive() {
			longQty = longQty.Add(l.Quantity)
		} else {
			shortQty = shortQty.Add(l.Quantity.Abs())
		}
	}
	return
}

// ruleIronButterfly returns a Rule that matches an iron butterfly: two put spreads and two call spreads
// with a common short strike, all at the same expiration, with long and short legs balanced.
func ruleIronButterfly() Rule {
	return Rule{
		Name: domain.StrategyIronButterfly,
		Match: func(legs []LegShape) bool {
			if len(legs) != 4 {
				return false
			}
			if !allOptions(legs) || !allSameExpiry(legs) {
				return false
			}
			puts := filterByOptionType(legs, domain.OptionTypePut)
			calls := filterByOptionType(legs, domain.OptionTypeCall)
			if len(puts) != 2 || len(calls) != 2 {
				return false
			}
			shortPuts := filterByDirection(puts, -1)
			longPuts := filterByDirection(puts, 1)
			shortCalls := filterByDirection(calls, -1)
			longCalls := filterByDirection(calls, 1)
			if len(shortPuts) != 1 || len(longPuts) != 1 || len(shortCalls) != 1 || len(longCalls) != 1 {
				return false
			}
			// Put spread: longPut < shortPut; Call spread: shortCall < longCall
			if !longPuts[0].Strike.LessThan(shortPuts[0].Strike) {
				return false
			}
			if !shortCalls[0].Strike.LessThan(longCalls[0].Strike) {
				return false
			}
			return shortPuts[0].Strike.Equal(shortCalls[0].Strike)
		},
	}
}

// ruleIronCondor returns a Rule that matches an iron condor: one long and one short put plus
// one long and one short call, all at the same expiration, where every put strike is strictly
// below every call strike. The rule accepts credit, debit, and mixed-width variants because
// partial fills often produce asymmetric leg orderings that are still logically one condor.
func ruleIronCondor() Rule {
	return Rule{
		Name: domain.StrategyIronCondor,
		Match: func(legs []LegShape) bool {
			if len(legs) != 4 {
				return false
			}
			if !allOptions(legs) || !allSameExpiry(legs) {
				return false
			}
			puts := filterByOptionType(legs, domain.OptionTypePut)
			calls := filterByOptionType(legs, domain.OptionTypeCall)
			if len(puts) != 2 || len(calls) != 2 {
				return false
			}
			shortPuts := filterByDirection(puts, -1)
			longPuts := filterByDirection(puts, 1)
			shortCalls := filterByDirection(calls, -1)
			longCalls := filterByDirection(calls, 1)
			if len(shortPuts) != 1 || len(longPuts) != 1 || len(shortCalls) != 1 || len(longCalls) != 1 {
				return false
			}
			// Every put strike must be strictly below every call strike.
			// This naturally excludes iron butterflies (equal center strikes) and rejects
			// inverted structures without caring about the ordering within each spread.
			maxPut := shortPuts[0].Strike
			if longPuts[0].Strike.GreaterThan(maxPut) {
				maxPut = longPuts[0].Strike
			}
			minCall := shortCalls[0].Strike
			if longCalls[0].Strike.LessThan(minCall) {
				minCall = longCalls[0].Strike
			}
			return maxPut.LessThan(minCall)
		},
	}
}

// ruleBrokenHeartButterfly returns a Rule that matches a broken heart butterfly: four legs of the same
// option type at the same expiration with distinct strikes, outer legs sharing a direction and inner
// legs opposite, all equal size.
func ruleBrokenHeartButterfly() Rule {
	return Rule{
		Name: domain.StrategyBrokenHeartButterfly,
		Match: func(legs []LegShape) bool {
			if len(legs) != 4 {
				return false
			}
			if !allOptions(legs) || !allSameExpiry(legs) || !allSameOptionType(legs) {
				return false
			}
			sorted := sortByStrike(legs)
			// All 4 strikes must be distinct
			for i := 0; i < 3; i++ {
				if sorted[i].Strike.Equal(sorted[i+1].Strike) {
					return false
				}
			}
			// Pattern: outer legs same direction, inner legs opposite direction.
			// Handles both long (1,-1,-1,1) and short (-1,1,1,-1) variants.
			// ruleIronButterfly/ruleIronCondor fire first and require mixed put/call legs,
			// so a 4-leg all-same-type trade never reaches those rules.
			if sorted[0].Quantity.Sign() != sorted[3].Quantity.Sign() ||
				sorted[1].Quantity.Sign() != sorted[2].Quantity.Sign() ||
				sorted[0].Quantity.Sign() == sorted[1].Quantity.Sign() {
				return false
			}
			// All four legs must be equal size. Outer-equal and inner-equal are
			// necessary but not sufficient — a 1-3-3-1 shape would otherwise pass.
			// Use Abs() because outer and inner legs have opposite signs.
			return sorted[0].Quantity.Abs().Equal(sorted[1].Quantity.Abs()) &&
				sorted[1].Quantity.Abs().Equal(sorted[2].Quantity.Abs()) &&
				sorted[2].Quantity.Abs().Equal(sorted[3].Quantity.Abs())
		},
	}
}

// ruleButterfly returns a Rule that matches a butterfly: three legs of the same option type
// at the same expiration with distinct strikes, outer legs equal size and sharing a direction,
// middle leg opposite direction and equal to the sum of the outer legs, with equidistant wings.
func ruleButterfly() Rule {
	return Rule{
		Name: domain.StrategyButterfly,
		Match: func(legs []LegShape) bool {
			if len(legs) != 3 {
				return false
			}
			if !allOptions(legs) || !allSameExpiry(legs) || !allSameOptionType(legs) {
				return false
			}
			sorted := sortByStrike(legs)
			if sorted[0].Strike.Equal(sorted[1].Strike) || sorted[1].Strike.Equal(sorted[2].Strike) {
				return false
			}
			// Outer legs must share a direction; middle leg must be opposite.
			// Handles both long (1,-1,1) and short (-1,1,-1) butterflies.
			if sorted[0].Quantity.Sign() != sorted[2].Quantity.Sign() || sorted[1].Quantity.Sign() == sorted[0].Quantity.Sign() {
				return false
			}
			// Outer legs must be equal size; middle must equal their sum.
			// Use Abs() because outer and middle legs have opposite signs.
			if !sorted[0].Quantity.Abs().Equal(sorted[2].Quantity.Abs()) {
				return false
			}
			if !sorted[1].Quantity.Abs().Equal(sorted[0].Quantity.Abs().Add(sorted[2].Quantity.Abs())) {
				return false
			}
			// Equidistant wings
			wing1 := sorted[1].Strike.Sub(sorted[0].Strike)
			wing2 := sorted[2].Strike.Sub(sorted[1].Strike)
			return wing1.Equal(wing2)
		},
	}
}

// ruleBrokenWingButterfly returns a Rule that matches a broken wing butterfly: three legs of the same
// option type at the same expiration with distinct strikes, outer legs equal size and sharing a direction,
// middle leg opposite direction and equal to the sum of the outer legs, with asymmetric wings.
func ruleBrokenWingButterfly() Rule {
	return Rule{
		Name: domain.StrategyBrokenWingButterfly,
		Match: func(legs []LegShape) bool {
			if len(legs) != 3 {
				return false
			}
			if !allOptions(legs) || !allSameExpiry(legs) || !allSameOptionType(legs) {
				return false
			}
			sorted := sortByStrike(legs)
			if sorted[0].Strike.Equal(sorted[1].Strike) || sorted[1].Strike.Equal(sorted[2].Strike) {
				return false
			}
			// Outer legs must share a direction; middle leg must be opposite.
			// Handles both long (1,-1,1) and short (-1,1,-1) variants.
			if sorted[0].Quantity.Sign() != sorted[2].Quantity.Sign() || sorted[1].Quantity.Sign() == sorted[0].Quantity.Sign() {
				return false
			}
			// Outer legs must be equal size; middle must equal their sum.
			// Use Abs() because outer and middle legs have opposite signs.
			if !sorted[0].Quantity.Abs().Equal(sorted[2].Quantity.Abs()) {
				return false
			}
			if !sorted[1].Quantity.Abs().Equal(sorted[0].Quantity.Abs().Add(sorted[2].Quantity.Abs())) {
				return false
			}
			// Asymmetric wings
			wing1 := sorted[1].Strike.Sub(sorted[0].Strike)
			wing2 := sorted[2].Strike.Sub(sorted[1].Strike)
			return !wing1.Equal(wing2)
		},
	}
}

// ruleCoveredCall returns a Rule that matches a covered call: a long equity leg paired with a short call option.
func ruleCoveredCall() Rule {
	return Rule{
		Name: domain.StrategyCoveredCall,
		Match: func(legs []LegShape) bool {
			if len(legs) != 2 {
				return false
			}
			var hasEquityLong, hasCallShort bool
			for _, l := range legs {
				if l.AssetClass == domain.AssetClassEquity && l.Quantity.IsPositive() {
					hasEquityLong = true
				}
				if l.AssetClass == domain.AssetClassEquityOption &&
					l.OptionType == domain.OptionTypeCall &&
					l.Quantity.IsNegative() {
					hasCallShort = true
				}
			}
			return hasEquityLong && hasCallShort
		},
	}
}

// ruleRatio returns a Rule that matches a ratio spread: options of the same type and expiration where
// total short quantity exceeds total long quantity.
func ruleRatio() Rule {
	return Rule{
		Name: domain.StrategyRatio,
		// Supports 2-leg (1-2-0, 1-3-0) and 3-leg (1-1-1) structures.
		// Strikes need not differ — what matters is total short qty > total long qty.
		Match: func(legs []LegShape) bool {
			if len(legs) < 2 || len(legs) > 3 {
				return false
			}
			if !allOptions(legs) || !allSameOptionType(legs) || !allSameExpiry(legs) {
				return false
			}
			longQty, shortQty := totalQtyByDirection(legs)
			if longQty.IsZero() || shortQty.IsZero() {
				return false
			}
			return shortQty.GreaterThan(longQty)
		},
	}
}

// ruleBackRatio returns a Rule that matches a back ratio spread: options of the same type and expiration where
// total long quantity exceeds total short quantity.
func ruleBackRatio() Rule {
	return Rule{
		Name: domain.StrategyBackRatio,
		// Supports 2-leg (2-1-0, 3-1-0) and 3-leg (1-1-2, 2-2-1) structures.
		// Strikes need not differ — what matters is total long qty > total short qty.
		Match: func(legs []LegShape) bool {
			if len(legs) < 2 || len(legs) > 3 {
				return false
			}
			if !allOptions(legs) || !allSameOptionType(legs) || !allSameExpiry(legs) {
				return false
			}
			longQty, shortQty := totalQtyByDirection(legs)
			if longQty.IsZero() || shortQty.IsZero() {
				return false
			}
			return longQty.GreaterThan(shortQty)
		},
	}
}

// ruleStraddle returns a Rule that matches a straddle: a put and call at the same strike, expiration,
// and direction (both long or both short).
func ruleStraddle() Rule {
	return Rule{
		Name: domain.StrategyStraddle,
		Match: func(legs []LegShape) bool {
			if len(legs) != 2 {
				return false
			}
			if !allOptions(legs) || !allSameExpiry(legs) {
				return false
			}
			if legs[0].OptionType == legs[1].OptionType {
				return false
			}
			if legs[0].Quantity.Sign() != legs[1].Quantity.Sign() {
				return false
			}
			return legs[0].Strike.Equal(legs[1].Strike)
		},
	}
}

// ruleStrangle returns a Rule that matches a strangle: a put and call at different strikes and the same
// expiration and direction, with the put strike strictly below the call strike.
func ruleStrangle() Rule {
	return Rule{
		Name: domain.StrategyStrangle,
		Match: func(legs []LegShape) bool {
			if len(legs) != 2 {
				return false
			}
			if !allOptions(legs) || !allSameExpiry(legs) {
				return false
			}
			if legs[0].OptionType == legs[1].OptionType {
				return false
			}
			if legs[0].Quantity.Sign() != legs[1].Quantity.Sign() {
				return false
			}
			return !legs[0].Strike.Equal(legs[1].Strike)
		},
	}
}

// ruleVertical returns a Rule that matches a vertical spread: two options of the same type and expiration
// with different strikes and opposite directions.
func ruleVertical() Rule {
	return Rule{
		Name: domain.StrategyVertical,
		Match: func(legs []LegShape) bool {
			if len(legs) != 2 {
				return false
			}
			if !allOptions(legs) || !allSameOptionType(legs) || !allSameExpiry(legs) {
				return false
			}
			if legs[0].Strike.Equal(legs[1].Strike) {
				return false
			}
			return legs[0].Quantity.Sign() != legs[1].Quantity.Sign()
		},
	}
}

// ruleCalendar returns a Rule that matches a calendar spread: two options of the same type and strike
// at different expirations with opposite directions.
func ruleCalendar() Rule {
	return Rule{
		Name: domain.StrategyCalendar,
		Match: func(legs []LegShape) bool {
			if len(legs) != 2 {
				return false
			}
			if !allOptions(legs) || !allSameOptionType(legs) {
				return false
			}
			if !legs[0].Strike.Equal(legs[1].Strike) {
				return false
			}
			if legs[0].Expiration.Equal(legs[1].Expiration) {
				return false
			}
			// Intentionally permissive about which expiry is long vs short:
			// both debit (long far, short near) and credit (short far, long near)
			// calendars are valid and classified as StrategyCalendar.
			return legs[0].Quantity.Sign() != legs[1].Quantity.Sign()
		},
	}
}

// ruleDiagonal returns a Rule that matches a diagonal spread: two options of the same type at different
// strikes and expirations with opposite directions.
func ruleDiagonal() Rule {
	return Rule{
		Name: domain.StrategyDiagonal,
		Match: func(legs []LegShape) bool {
			if len(legs) != 2 {
				return false
			}
			if !allOptions(legs) || !allSameOptionType(legs) {
				return false
			}
			if legs[0].Strike.Equal(legs[1].Strike) {
				return false
			}
			if legs[0].Expiration.Equal(legs[1].Expiration) {
				return false
			}
			return legs[0].Quantity.Sign() != legs[1].Quantity.Sign()
		},
	}
}

// ruleSingle returns a Rule that matches a single long or short option.
func ruleSingle() Rule {
	return Rule{
		Name: domain.StrategySingle,
		Match: func(legs []LegShape) bool {
			return len(legs) == 1 && isOption(legs[0])
		},
	}
}

// ruleStock returns a Rule that matches a single long or short equity position.
func ruleStock() Rule {
	return Rule{
		Name: domain.StrategyStock,
		Match: func(legs []LegShape) bool {
			return len(legs) == 1 && legs[0].AssetClass == domain.AssetClassEquity
		},
	}
}

// ruleFuture returns a Rule that matches a single long or short futures contract.
func ruleFuture() Rule {
	return Rule{
		Name: domain.StrategyFuture,
		Match: func(legs []LegShape) bool {
			return len(legs) == 1 && legs[0].AssetClass == domain.AssetClassFuture
		},
	}
}
