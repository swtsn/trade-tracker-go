package domain

import (
	"crypto/sha256"
	"fmt"
	"time"

	"github.com/shopspring/decimal"
)

// AssetClass represents the category of a financial instrument.
type AssetClass string

const (
	AssetClassEquity       AssetClass = "equity"
	AssetClassEquityOption AssetClass = "equity_option"
	AssetClassFuture       AssetClass = "future"
	AssetClassFutureOption AssetClass = "future_option"
)

// OptionType represents the type of an options contract.
type OptionType string

const (
	OptionTypeCall OptionType = "C"
	OptionTypePut  OptionType = "P"
)

// OptionDetails contains the contract-specific parameters for an options instrument.
type OptionDetails struct {
	Expiration time.Time
	Strike     decimal.Decimal
	OptionType OptionType
	Multiplier decimal.Decimal
	OSI        string
}

// FutureDetails contains the contract-specific parameters for a futures instrument.
type FutureDetails struct {
	ExpiryMonth  time.Time
	ExchangeCode string
}

// Instrument represents a tradeable security: equity, option, or future.
// It is identified deterministically by its symbol and contract-specific fields.
// FuturesRoot is set only for future options and identifies the root product
// (e.g. "/NG") for contract spec lookup. It is not stored in the instruments table.
type Instrument struct {
	Symbol      string
	AssetClass  AssetClass
	Option      *OptionDetails
	Future      *FutureDetails
	FuturesRoot string // e.g. "/NG" for a /NGK26 option; empty for non-future-options
}

// InstrumentID returns the deterministic SHA-256 hash ID for this instrument.
// The hash covers all fields that make an instrument unique: symbol, asset class,
// option fields (expiration, strike, option type), and future fields (expiry month,
// exchange code). This matches the DB UNIQUE constraint.
func (inst Instrument) InstrumentID() string {
	var expiration, strike, optionType string
	if inst.Option != nil {
		expiration = inst.Option.Expiration.UTC().Format(time.RFC3339)
		strike = inst.Option.Strike.String()
		optionType = string(inst.Option.OptionType)
	}
	var expiryMonth, exchangeCode string
	if inst.Future != nil {
		if !inst.Future.ExpiryMonth.IsZero() {
			expiryMonth = inst.Future.ExpiryMonth.UTC().Format("2006-01")
		}
		exchangeCode = inst.Future.ExchangeCode
	}
	input := fmt.Sprintf("%s|%s|%s|%s|%s|%s|%s",
		inst.Symbol, inst.AssetClass, expiration, strike, optionType, expiryMonth, exchangeCode)
	sum := sha256.Sum256([]byte(input))
	return fmt.Sprintf("%x", sum)
}
