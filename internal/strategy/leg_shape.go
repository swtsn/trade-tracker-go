package strategy

import (
	"time"

	"github.com/shopspring/decimal"
	"trade-tracker-go/internal/domain"
)

// LegShape is a normalized view of one opening transaction leg.
// All classifier rules operate on []LegShape — never on domain.Transaction directly.
// Only opening legs are included (PositionEffectOpening). Closing legs are excluded.
//
// Preconditions callers must satisfy:
//   - All legs in a slice passed to Classify must belong to the same underlying instrument.
//     The classifier has no Symbol field and cannot detect cross-underlying mixtures.
//   - Expiration must be normalized to UTC midnight before constructing a LegShape.
//     time.Time.Equal compares instants; different timezone offsets on the same calendar
//     date will cause allSameExpiry to return false incorrectly.
type LegShape struct {
	AssetClass domain.AssetClass
	OptionType domain.OptionType // "" for non-options
	Strike     decimal.Decimal   // zero for non-options
	Expiration time.Time         // zero for non-options; must be UTC midnight
	Quantity   decimal.Decimal   // positive = long (BTO/BUY), negative = short (STO/SELL)
}

// FromTransactions normalizes a slice of transactions into LegShapes for classification.
// Only opening legs with a recognized direction action are included.
// Transactions with an unrecognized Action (e.g. ActionExercise, ActionExpiration) are
// silently skipped — they do not represent market-opening legs. Transactions with a
// non-positive Quantity are also skipped; broker quantities are always positive.
func FromTransactions(txs []domain.Transaction) []LegShape {
	var legs []LegShape
	for _, tx := range txs {
		if tx.PositionEffect != domain.PositionEffectOpening {
			continue
		}
		if !tx.Quantity.IsPositive() {
			continue
		}
		var qty decimal.Decimal
		switch tx.Action {
		case domain.ActionBTO, domain.ActionBuy:
			qty = tx.Quantity
		case domain.ActionSTO, domain.ActionSell:
			qty = tx.Quantity.Neg()
		default:
			// Actions like ActionAssignment, ActionExercise, ActionExpiration are
			// not market-opening legs and are intentionally excluded from classification.
			continue
		}
		leg := LegShape{
			AssetClass: tx.Instrument.AssetClass,
			Quantity:   qty,
		}
		if tx.Instrument.Option != nil {
			leg.OptionType = tx.Instrument.Option.OptionType
			leg.Strike = tx.Instrument.Option.Strike
			leg.Expiration = tx.Instrument.Option.Expiration
		}
		legs = append(legs, leg)
	}
	return legs
}
