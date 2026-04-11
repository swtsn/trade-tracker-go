package strategy

import "trade-tracker-go/internal/domain"

// maxLegs is the maximum number of legs a broker multi-leg order can contain.
// Inputs with more legs are considered invalid and always yield StrategyUnknown.
const maxLegs = 4

// Rule is a named predicate over a normalized set of opening legs.
// Rule is intentionally exported for readability; this package is internal and
// cannot be imported by external callers. There is no Register API — all rules
// are wired in NewClassifier() and the rule slice is immutable after construction.
type Rule struct {
	Name  domain.StrategyType
	Match func(legs []LegShape) bool // pure function — no DB, no I/O, no side effects
}

// Classifier checks rules in registration order and returns the first match.
// Rules must be mutually exclusive by construction — if two rules match the same
// leg shape that is a bug in the rules, not a tie to be resolved by priority.
// Returns StrategyUnknown if no rule matches — this is valid state, not an error.
//
// A Classifier is safe for concurrent use after construction; its rule slice is
// never modified after NewClassifier() returns.
type Classifier struct{ rules []Rule }

// NewClassifier returns a Classifier with all strategy rules registered in evaluation order.
// More specific rules (4-leg) are registered before less specific ones (1-leg).
func NewClassifier() *Classifier {
	return &Classifier{
		rules: []Rule{
			ruleIronButterfly(),
			ruleIronCondor(),
			ruleBrokenHeartButterfly(),
			ruleButterfly(),
			ruleBrokenWingButterfly(),
			ruleCoveredCall(),
			ruleRatio(),
			ruleBackRatio(),
			ruleStraddle(),
			ruleStrangle(),
			ruleVertical(),
			ruleCalendar(),
			ruleDiagonal(),
			ruleSingle(),
			ruleStock(),
			ruleFuture(),
		},
	}
}

// Classify returns the first matching strategy for the given legs, or StrategyUnknown.
// Inputs with more than maxLegs legs are invalid and always return StrategyUnknown.
func (c *Classifier) Classify(legs []LegShape) domain.StrategyType {
	if len(legs) > maxLegs {
		return domain.StrategyUnknown
	}
	for _, rule := range c.rules {
		if rule.Match(legs) {
			return rule.Name
		}
	}
	return domain.StrategyUnknown
}
