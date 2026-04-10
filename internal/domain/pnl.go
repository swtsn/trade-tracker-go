package domain

import "github.com/shopspring/decimal"

// PnL is a value object representing profit and loss components.
type PnL struct {
	Realized decimal.Decimal
	Fees     decimal.Decimal
}

// NetRealized returns realized P&L minus fees.
func (p PnL) NetRealized() decimal.Decimal {
	return p.Realized.Sub(p.Fees)
}
