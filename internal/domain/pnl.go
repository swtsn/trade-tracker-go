package domain

import "github.com/shopspring/decimal"

// PnL is a value object representing profit and loss components.
type PnL struct {
	Realized decimal.Decimal
	Fees     decimal.Decimal
}

func (p PnL) NetRealized() decimal.Decimal {
	return p.Realized.Sub(p.Fees)
}
