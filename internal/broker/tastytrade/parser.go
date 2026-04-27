// Package tastytrade parses Tastytrade transaction history CSV exports into
// normalized domain.Transaction slices for use with ImportService.
package tastytrade

import (
	"encoding/csv"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"

	"trade-tracker-go/internal/broker/brokerutil"
	"trade-tracker-go/internal/domain"
)

// Broker is the canonical broker name for Tastytrade transactions.
const Broker = "tastytrade"

// Tastytrade CSV column indices (0-based).
const (
	colDate             = 0
	colType             = 1
	colSubType          = 2
	colAction           = 3
	colSymbol           = 4
	colInstrumentType   = 5
	colValue            = 7
	colQuantity         = 8
	colAveragePrice     = 9
	colCommissions      = 10
	colFees             = 11
	colMultiplier       = 12
	colRootSymbol       = 13
	colUnderlyingSymbol = 14
	colExpirationDate   = 15
	colStrikePrice      = 16
	colCallOrPut        = 17
	colOrderNumber      = 18
	numCols             = 21
)

// Parser parses Tastytrade transaction history CSV exports.
type Parser struct{}

// Parse reads a Tastytrade CSV from r and returns normalized domain transactions.
// accountID is the internal account identifier to stamp on each transaction.
//
// Skipped rows (not returned, no error):
//   - Type == "Money Movement" (balance adjustments, dividends paid out, etc.)
//   - Empty Action column (assignment removal rows)
//
// TradeID is derived deterministically from Order # so that the same order
// always produces the same TradeID, enabling idempotent re-imports.
// BrokerTxID is deterministic from (broker, accountID, orderGroup, rowSeqWithinGroup).
func (p *Parser) Parse(r io.Reader, accountID string) ([]domain.Transaction, error) {
	cr := csv.NewReader(r)
	cr.FieldsPerRecord = -1 // variable — first row may have fewer fields
	cr.LazyQuotes = true

	// Skip the column header row.
	if _, err := cr.Read(); err != nil {
		return nil, fmt.Errorf("tastytrade: read header: %w", err)
	}

	seqByGroup := make(map[string]int)        // row count per group key, for BrokerTxID uniqueness
	tradeIDByGroup := make(map[string]string) // group key → deterministic TradeID

	var txs []domain.Transaction
	lineNum := 1
	for {
		record, err := cr.Read()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("tastytrade: line %d: read: %w", lineNum, err)
		}
		lineNum++

		if len(record) < numCols {
			continue // skip malformed rows
		}

		rowType := strings.TrimSpace(record[colType])
		action := strings.TrimSpace(record[colAction])

		if rowType == "Money Movement" {
			continue
		}
		if action == "" {
			// Assignment removal rows and similar carry no action.
			continue
		}

		tx, err := parseRow(record, accountID, seqByGroup, tradeIDByGroup)
		if err != nil {
			return nil, fmt.Errorf("tastytrade: line %d: %w", lineNum, err)
		}
		txs = append(txs, tx)
	}
	return txs, nil
}

func parseRow(
	record []string,
	accountID string,
	seqByGroup map[string]int,
	tradeIDByGroup map[string]string,
) (domain.Transaction, error) {
	action := strings.TrimSpace(record[colAction])
	orderNum := strings.TrimSpace(record[colOrderNumber])
	symbol := strings.TrimSpace(record[colSymbol])

	executedAt, err := time.Parse("2006-01-02T15:04:05-0700", strings.TrimSpace(record[colDate]))
	if err != nil {
		return domain.Transaction{}, fmt.Errorf("parse date %q: %w", record[colDate], err)
	}
	executedAt = executedAt.UTC()

	subType := strings.TrimSpace(record[colSubType])
	domainAction, posEffect, err := mapAction(action, subType)
	if err != nil {
		return domain.Transaction{}, err
	}

	qty, err := parseDecimal(record[colQuantity])
	if err != nil {
		return domain.Transaction{}, fmt.Errorf("parse quantity: %w", err)
	}
	qty = qty.Abs()

	fillPrice, err := parseDecimal(record[colAveragePrice])
	if err != nil {
		return domain.Transaction{}, fmt.Errorf("parse average price: %w", err)
	}
	fillPrice = fillPrice.Abs()

	fees, err := parseFees(record[colCommissions], record[colFees])
	if err != nil {
		return domain.Transaction{}, err
	}

	instrument, err := parseInstrument(record)
	if err != nil {
		return domain.Transaction{}, fmt.Errorf("parse instrument %q: %w", symbol, err)
	}

	// Tastytrade's Average Price column is the per-contract dollar value (e.g. 246.00
	// for a $2.46 option). Normalize to per-share so position_service multiplying by
	// the multiplier produces the correct cash flow.
	if instrument.Option != nil && !instrument.Option.Multiplier.IsZero() {
		fillPrice = fillPrice.Div(instrument.Option.Multiplier)
	}

	// Group key: Order # when present; otherwise a per-row fallback so that
	// expirations and dividend reinvestments each become their own trade.
	groupKey := orderNum
	if groupKey == "" {
		groupKey = Broker + "|" + accountID + "|" + executedAt.UTC().Format(time.RFC3339) + "|" + action + "|" + symbol
	}

	tradeID, ok := tradeIDByGroup[groupKey]
	if !ok {
		tradeID = brokerutil.HashKey(Broker, accountID, groupKey)
		tradeIDByGroup[groupKey] = tradeID
	}

	seq := seqByGroup[groupKey]
	seqByGroup[groupKey]++
	brokerTxID := brokerutil.HashKey(Broker, accountID, groupKey, fmt.Sprintf("%d", seq))

	return domain.Transaction{
		ID:             uuid.New().String(),
		TradeID:        tradeID,
		BrokerTxID:     brokerTxID,
		BrokerOrderID:  orderNum,
		Broker:         Broker,
		AccountID:      accountID,
		Instrument:     instrument,
		Action:         domainAction,
		Quantity:       qty,
		FillPrice:      fillPrice,
		Fees:           fees,
		ExecutedAt:     executedAt,
		PositionEffect: posEffect,
	}, nil
}

func mapAction(action, subType string) (domain.Action, domain.PositionEffect, error) {
	switch action {
	case "BUY_TO_OPEN":
		return domain.ActionBTO, domain.PositionEffectOpening, nil
	case "SELL_TO_OPEN":
		return domain.ActionSTO, domain.PositionEffectOpening, nil
	case "BUY_TO_CLOSE":
		return domain.ActionBTC, domain.PositionEffectClosing, nil
	case "SELL_TO_CLOSE":
		return domain.ActionSTC, domain.PositionEffectClosing, nil
	case "BUY":
		// Futures use bare BUY/SELL. subType disambiguates direction when available.
		if strings.Contains(strings.ToLower(subType), "to close") {
			return domain.ActionBTC, domain.PositionEffectClosing, nil
		}
		return domain.ActionBuy, domain.PositionEffectOpening, nil
	case "SELL":
		if strings.Contains(strings.ToLower(subType), "to open") {
			return domain.ActionSTO, domain.PositionEffectOpening, nil
		}
		return domain.ActionSell, domain.PositionEffectClosing, nil
	default:
		return "", "", fmt.Errorf("unrecognized action %q", action)
	}
}

func parseInstrument(record []string) (domain.Instrument, error) {
	instType := strings.TrimSpace(record[colInstrumentType])
	symbol := strings.TrimSpace(record[colSymbol])
	underlyingSymbol := strings.TrimSpace(record[colUnderlyingSymbol])

	switch instType {
	case "Equity":
		return domain.Instrument{
			Symbol:     symbol,
			AssetClass: domain.AssetClassEquity,
		}, nil

	case "Future":
		return domain.Instrument{
			Symbol:     symbol,
			AssetClass: domain.AssetClassFuture,
			Future:     &domain.FutureDetails{},
		}, nil

	case "Equity Option":
		mult := decimal.NewFromInt(100)
		opt, err := parseOptionDetails(record, mult)
		if err != nil {
			return domain.Instrument{}, err
		}
		return domain.Instrument{
			Symbol:     underlyingSymbol,
			AssetClass: domain.AssetClassEquityOption,
			Option:     opt,
		}, nil

	case "Future Option":
		mult, err := parseDecimal(record[colMultiplier])
		if err != nil || mult.IsZero() {
			mult = decimal.NewFromInt(1)
		}
		opt, err := parseOptionDetails(record, mult)
		if err != nil {
			return domain.Instrument{}, err
		}
		return domain.Instrument{
			Symbol:     underlyingSymbol,
			AssetClass: domain.AssetClassFutureOption,
			Option:     opt,
		}, nil

	default:
		return domain.Instrument{}, fmt.Errorf("unrecognized instrument type %q", instType)
	}
}

func parseOptionDetails(record []string, multiplier decimal.Decimal) (*domain.OptionDetails, error) {
	expStr := strings.TrimSpace(record[colExpirationDate])
	strikeStr := strings.TrimSpace(record[colStrikePrice])
	callPut := strings.ToUpper(strings.TrimSpace(record[colCallOrPut]))
	osi := strings.TrimSpace(record[colSymbol])

	// NOTE: "06" is a two-digit year. Go maps 69–99 → 1969–1999 and 00–68 → 2000–2068.
	// Near-term options expirations (up to 2068) are safe; historical data beyond that
	// would require migrating to a four-digit year format.
	expiration, err := time.Parse("1/2/06", expStr)
	if err != nil {
		return nil, fmt.Errorf("parse expiration %q: %w", expStr, err)
	}
	expiration = expiration.UTC()

	strike, err := decimal.NewFromString(strikeStr)
	if err != nil {
		return nil, fmt.Errorf("parse strike %q: %w", strikeStr, err)
	}

	var optType domain.OptionType
	switch callPut {
	case "CALL":
		optType = domain.OptionTypeCall
	case "PUT":
		optType = domain.OptionTypePut
	default:
		return nil, fmt.Errorf("unrecognized option type %q", callPut)
	}

	return &domain.OptionDetails{
		Expiration: expiration,
		Strike:     strike,
		OptionType: optType,
		Multiplier: multiplier,
		OSI:        osi,
	}, nil
}

// parseFees returns the total fee as a positive decimal.
// Tastytrade stores commissions and fees as negative values (costs).
// "--" means the field is not applicable; treated as zero.
func parseFees(commissionsStr, feesStr string) (decimal.Decimal, error) {
	commissions := decimal.Zero
	if strings.TrimSpace(commissionsStr) != "--" {
		c, err := parseDecimal(commissionsStr)
		if err != nil {
			return decimal.Zero, fmt.Errorf("parse commissions %q: %w", commissionsStr, err)
		}
		commissions = c
	}

	fees := decimal.Zero
	if strings.TrimSpace(feesStr) != "--" {
		f, err := parseDecimal(feesStr)
		if err != nil {
			return decimal.Zero, fmt.Errorf("parse fees %q: %w", feesStr, err)
		}
		fees = f
	}

	// Both are negative (costs). Negate the sum to get a positive fee amount.
	return commissions.Add(fees).Neg(), nil
}

// parseDecimal parses a decimal string, stripping commas from formatted numbers.
func parseDecimal(s string) (decimal.Decimal, error) {
	s = strings.TrimSpace(s)
	s = strings.ReplaceAll(s, ",", "")
	return decimal.NewFromString(s)
}
