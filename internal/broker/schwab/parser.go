// Package schwab parses Schwab "Account Trade History" CSV exports into
// normalized domain.Transaction slices for use with ImportService.
package schwab

import (
	"encoding/csv"
	"fmt"
	"io"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"

	"trade-tracker-go/internal/broker/brokerutil"
	"trade-tracker-go/internal/domain"
)

// Broker is the canonical broker name for Schwab transactions.
const Broker = "schwab"

// Schwab CSV column indices (0-based). The CSV has a leading empty column
// (every row starts with a comma), so data starts at index 1.
const (
	schColTime      = 1  // Exec Time: "4/6/26 08:38:00" or "" for continuation rows
	schColSpread    = 2  // Spread: "IRON CONDOR", "SINGLE", etc.
	schColSide      = 3  // Side: "BUY" or "SELL"
	schColQty       = 4  // Qty: "+2" or "-1"
	schColEffect    = 5  // Pos Effect: "TO OPEN" or "TO CLOSE"
	schColSymbol    = 6  // Symbol (underlying or futures description)
	schColExp       = 7  // Exp: "15 MAY 26", "APR 26", options code, or ""
	schColStrike    = 8  // Strike: numeric or ""
	schColType      = 9  // Type: "CALL", "PUT", "FUTURE", "ETF", "STOCK"
	schColPrice     = 10 // Price: per-leg fill price
	schColNetPrice  = 11 // Net Price: net price for the order
	schColOrderType = 12 // Order Type: e.g. "LIMIT", "MARKET"
	schNumCols      = 13
)

// futureOptExpRe matches a date embedded in a future option symbol description,
// e.g. "27 APR 26" in "/NGK26 1/10000 27 APR 26 (Financial) (MAY 26)".
var futureOptExpRe = regexp.MustCompile(`\b(\d{1,2} [A-Z]+ \d{2})\b`)

const (
	expectedTitle  = "Account Trade History"
	expectedHeader = ",Exec Time,Spread,Side,Qty,Pos Effect,Symbol,Exp,Strike,Type,Price,Net Price,Order Type"
)

// Parser parses Schwab Account Trade History CSV exports.
type Parser struct{}

// Parse reads a Schwab CSV from r and returns normalized domain transactions.
// accountID is the internal account identifier to stamp on each transaction.
//
// The Schwab format groups multi-leg orders visually: the first row of each
// order has a non-empty Exec Time; continuation rows have an empty Exec Time.
// All legs in a group share the same TradeID.
//
// Timestamps are parsed as UTC. Schwab does not export timezone information.
func (p *Parser) Parse(r io.Reader, accountID string) ([]domain.Transaction, error) {
	cr := csv.NewReader(r)
	cr.FieldsPerRecord = -1
	cr.LazyQuotes = true

	// First line is either "Account Trade History" (full export) or the column
	// header row directly (header-only export). Both are valid.
	first, err := cr.Read()
	if err != nil {
		return nil, fmt.Errorf("schwab: read first line: %w", err)
	}
	// Strip a leading UTF-8 BOM that Windows tools sometimes prepend.
	first[0] = strings.TrimPrefix(first[0], "\ufeff")
	firstJoined := strings.Join(first, ",")

	preambleLines := 1 // tracks consumed lines so the data-loop line counter starts correctly
	switch {
	case len(first) == 1 && firstJoined == expectedTitle:
		// Title line present — next line must be the header.
		header, err := cr.Read()
		if err != nil {
			return nil, fmt.Errorf("schwab: read header line: %w", err)
		}
		if got := strings.Join(header, ","); got != expectedHeader {
			return nil, fmt.Errorf("schwab: unexpected header %q; export format may have changed", got)
		}
		preambleLines = 2
	case firstJoined == expectedHeader:
		// Header line only — no title row, which is fine.
	default:
		return nil, fmt.Errorf("schwab: unexpected first line %q; is this a Schwab Account Trade History export?", firstJoined)
	}

	// groupCounts tracks how many groups have started for a given
	// (execTime, spread, firstSymbol) key, to disambiguate same-second orders.
	groupCounts := make(map[string]int)

	var txs []domain.Transaction
	var currentRows [][]string
	var groupExecTime, groupSpread string

	flush := func() error {
		if len(currentRows) == 0 {
			return nil
		}
		groupTxs, err := parseGroup(currentRows, accountID, groupExecTime, groupSpread, groupCounts)
		if err != nil {
			return err
		}
		txs = append(txs, groupTxs...)
		currentRows = nil
		return nil
	}

	// lineNum starts at preambleLines so that after the first successful Read
	// (which increments it) it correctly identifies the first data row.
	lineNum := preambleLines
	for {
		record, err := cr.Read()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("schwab: line %d: %w", lineNum, err)
		}
		lineNum++

		if len(record) < schNumCols {
			// Blank lines (single empty field) are silently skipped.
			if len(record) == 1 && record[0] == "" {
				continue
			}
			return nil, fmt.Errorf("schwab: line %d: expected %d columns, got %d", lineNum, schNumCols, len(record))
		}

		execTime := strings.TrimSpace(record[schColTime])
		if execTime != "" {
			// New group: flush the previous one first.
			if err := flush(); err != nil {
				return nil, fmt.Errorf("schwab: line %d: %w", lineNum, err)
			}
			groupExecTime = execTime
			groupSpread = strings.TrimSpace(record[schColSpread])
		}

		currentRows = append(currentRows, record)
	}

	if err := flush(); err != nil {
		return nil, fmt.Errorf("schwab: final group: %w", err)
	}

	return txs, nil
}

// parseGroup converts one multi-leg order (a slice of CSV rows sharing an Exec Time)
// into domain.Transactions, all stamped with the same TradeID.
func parseGroup(
	rows [][]string,
	accountID, execTime, spread string,
	groupCounts map[string]int,
) ([]domain.Transaction, error) {
	if len(rows) == 0 {
		return nil, nil
	}

	firstSymbol := underlyingSymbol(strings.TrimSpace(rows[0][schColSymbol]))

	groupKey := execTime + "|" + spread + "|" + firstSymbol
	seq := groupCounts[groupKey]
	groupCounts[groupKey]++

	tradeID := brokerutil.HashKey(Broker, accountID, groupKey, strconv.Itoa(seq))

	executedAt, err := time.Parse("1/2/06 15:04:05", execTime)
	if err != nil {
		return nil, fmt.Errorf("parse exec time %q: %w", execTime, err)
	}
	executedAt = executedAt.UTC()

	txs := make([]domain.Transaction, 0, len(rows))
	for legIdx, row := range rows {
		tx, err := parseLeg(row, accountID, tradeID, groupKey+"|"+strconv.Itoa(seq), legIdx, executedAt)
		if err != nil {
			return nil, fmt.Errorf("leg %d: %w", legIdx, err)
		}
		txs = append(txs, tx)
	}
	return txs, nil
}

// parseLeg converts one CSV row into a domain.Transaction.
func parseLeg(
	record []string,
	accountID, tradeID, groupKey string,
	legIdx int,
	executedAt time.Time,
) (domain.Transaction, error) {
	side := strings.ToUpper(strings.TrimSpace(record[schColSide]))
	posEffect := strings.ToUpper(strings.TrimSpace(record[schColEffect]))
	qtyStr := strings.TrimSpace(record[schColQty])
	symbol := strings.TrimSpace(record[schColSymbol])
	expStr := strings.TrimSpace(record[schColExp])
	strikeStr := strings.TrimSpace(record[schColStrike])
	typStr := strings.ToUpper(strings.TrimSpace(record[schColType]))
	priceStr := strings.TrimSpace(record[schColPrice])

	domainAction, domainEffect, err := mapAction(side, posEffect)
	if err != nil {
		return domain.Transaction{}, err
	}

	qty, err := parseQty(qtyStr)
	if err != nil {
		return domain.Transaction{}, fmt.Errorf("parse qty %q: %w", qtyStr, err)
	}

	// Treasury future options (/ZB, /ZN) quote prices in 64ths of a point; all others use 32nds.
	fracDivisor := int64(32)
	if root := futuresRoot(underlyingSymbol(symbol)); root == "/ZB" || root == "/ZN" {
		fracDivisor = 64
	}
	fillPrice, err := parsePrice(priceStr, fracDivisor)
	if err != nil {
		return domain.Transaction{}, fmt.Errorf("parse price %q: %w", priceStr, err)
	}
	fillPrice = fillPrice.Abs()

	instrument, err := parseInstrument(symbol, expStr, strikeStr, typStr)
	if err != nil {
		return domain.Transaction{}, fmt.Errorf("parse instrument: %w", err)
	}

	brokerTxID := brokerutil.HashKey(Broker, accountID, groupKey, strconv.Itoa(legIdx))

	// TODO: Schwab does not currently include a fees column in this export format.
	// When it does, parse it here and replace decimal.Zero.
	fees := decimal.Zero

	return domain.Transaction{
		ID:             uuid.New().String(),
		TradeID:        tradeID,
		BrokerTxID:     brokerTxID,
		BrokerOrderID:  "",
		Broker:         Broker,
		AccountID:      accountID,
		Instrument:     instrument,
		Action:         domainAction,
		Quantity:       qty,
		FillPrice:      fillPrice,
		Fees:           fees,
		ExecutedAt:     executedAt,
		PositionEffect: domainEffect,
	}, nil
}

// mapAction derives a domain.Action and domain.PositionEffect from the Schwab
// Side ("BUY"/"SELL") and Pos Effect ("TO OPEN"/"TO CLOSE") fields.
func mapAction(side, posEffect string) (domain.Action, domain.PositionEffect, error) {
	switch {
	case side == "BUY" && posEffect == "TO OPEN":
		return domain.ActionBTO, domain.PositionEffectOpening, nil
	case side == "SELL" && posEffect == "TO OPEN":
		return domain.ActionSTO, domain.PositionEffectOpening, nil
	case side == "BUY" && posEffect == "TO CLOSE":
		return domain.ActionBTC, domain.PositionEffectClosing, nil
	case side == "SELL" && posEffect == "TO CLOSE":
		return domain.ActionSTC, domain.PositionEffectClosing, nil
	default:
		return "", "", fmt.Errorf("unrecognized side/effect %q/%q", side, posEffect)
	}
}

// parseInstrument builds a domain.Instrument from the Schwab leg columns.
func parseInstrument(symbol, expStr, strikeStr, typStr string) (domain.Instrument, error) {
	switch typStr {
	case "ETF", "STOCK":
		return domain.Instrument{
			Symbol:     symbol,
			AssetClass: domain.AssetClassEquity,
		}, nil

	case "FUTURE":
		expiry, err := parseMonthYear(expStr) // "APR 26" → April 1, 2026 UTC
		if err != nil {
			return domain.Instrument{}, fmt.Errorf("parse future expiry: %w", err)
		}
		return domain.Instrument{
			Symbol:     symbol,
			AssetClass: domain.AssetClassFuture,
			Future: &domain.FutureDetails{
				ExpiryMonth: expiry,
			},
		}, nil

	case "CALL", "PUT":
		return parseOptionInstrument(symbol, expStr, strikeStr, typStr)

	default:
		return domain.Instrument{}, fmt.Errorf("unrecognized instrument type %q", typStr)
	}
}

// parseOptionInstrument handles equity options and future options.
// Future options are identified by the symbol starting with "/".
func parseOptionInstrument(symbol, expStr, strikeStr, typStr string) (domain.Instrument, error) {
	optType := domain.OptionTypeCall
	if typStr == "PUT" {
		optType = domain.OptionTypePut
	}

	strike, err := decimal.NewFromString(strikeStr)
	if err != nil {
		return domain.Instrument{}, fmt.Errorf("parse strike %q: %w", strikeStr, err)
	}

	isFutureOpt := strings.HasPrefix(symbol, "/")

	var underlying string
	var expiration time.Time

	if isFutureOpt {
		underlying = underlyingSymbol(symbol)
		// The Exp field for future options is an options code (e.g. "/LNEK26"), not a date.
		// Extract the expiration date from the symbol description instead.
		m := futureOptExpRe.FindString(symbol)
		if m == "" {
			return domain.Instrument{}, fmt.Errorf("parse future option expiration: no date found in symbol %q", symbol)
		}
		expiration, err = time.Parse("2 Jan 06", titleMonth(m))
		if err != nil {
			return domain.Instrument{}, fmt.Errorf("parse future option expiration from %q: %w", m, err)
		}
		expiration = expiration.UTC()
	} else {
		underlying = symbol
		expiration, err = time.Parse("2 Jan 06", titleMonth(expStr))
		if err != nil {
			return domain.Instrument{}, fmt.Errorf("parse expiration %q: %w", expStr, err)
		}
		expiration = expiration.UTC()
	}

	multiplier := decimal.NewFromInt(100) // standard equity option multiplier
	var root string
	if isFutureOpt {
		root = futuresRoot(underlying)
		switch root {
		case "/ZB", "/ZN":
			// Treasury options: each tick (0''01) = $15.625, 64 ticks per point, 1 point = $1,000.
			multiplier = decimal.NewFromInt(1000)
		default:
			// Multiplier unknown — use 1 pending post-processing via contract_specs table.
			multiplier = decimal.NewFromInt(1)
		}
	}

	assetClass := domain.AssetClassEquityOption
	if isFutureOpt {
		assetClass = domain.AssetClassFutureOption
	}

	return domain.Instrument{
		Symbol:      underlying,
		AssetClass:  assetClass,
		FuturesRoot: root,
		Option: &domain.OptionDetails{
			Expiration: expiration,
			Strike:     strike,
			OptionType: optType,
			Multiplier: multiplier,
		},
	}, nil
}

// underlyingSymbol extracts the root symbol from a futures description.
// "/NGK26 1/10000 27 APR 26 ..." → "/NGK26"
// Returns the input unchanged for non-futures symbols.
func underlyingSymbol(symbol string) string {
	if !strings.HasPrefix(symbol, "/") {
		return symbol
	}
	if i := strings.Index(symbol, " "); i > 0 {
		return symbol[:i]
	}
	return symbol
}

// parseMonthYear parses "APR 26" into the first day of that month in UTC.
// Returns an error if the input cannot be parsed.
func parseMonthYear(s string) (time.Time, error) {
	t, err := time.Parse("Jan 06", titleMonth(s))
	if err != nil {
		return time.Time{}, fmt.Errorf("parse month-year %q: %w", s, err)
	}
	return t.UTC(), nil
}

// knownMonths is the set of three-letter month abbreviations accepted by Go's time.Parse.
var knownMonths = map[string]bool{
	"JAN": true, "FEB": true, "MAR": true, "APR": true,
	"MAY": true, "JUN": true, "JUL": true, "AUG": true,
	"SEP": true, "OCT": true, "NOV": true, "DEC": true,
}

// titleMonth converts known uppercase month abbreviations in a date string to
// title case so Go's time.Parse can match them: "15 MAY 26" → "15 May 26".
// Only tokens in the knownMonths set are converted; other tokens (e.g. ticker
// symbols, exchange codes) are left unchanged.
func titleMonth(s string) string {
	parts := strings.Fields(s)
	for i, p := range parts {
		if knownMonths[p] {
			parts[i] = strings.ToUpper(p[:1]) + strings.ToLower(p[1:])
		}
	}
	return strings.Join(parts, " ")
}

// futuresRoot strips the month-code letter and 2-digit year suffix from a futures
// contract symbol, returning the root product code.
// e.g. "/ZBM26" → "/ZB", "/MCLJ26" → "/MCL"
func futuresRoot(symbol string) string {
	n := len(symbol)
	if n >= 3 && symbol[n-1] >= '0' && symbol[n-1] <= '9' &&
		symbol[n-2] >= '0' && symbol[n-2] <= '9' &&
		symbol[n-3] >= 'A' && symbol[n-3] <= 'Z' {
		return symbol[:n-3]
	}
	return symbol
}

// parsePrice parses a Schwab price string. Most prices are plain decimals, but
// some Treasury futures options use tick notation: "0”13" = 0 + 13/<fracDivisor>.
// Pass fracDivisor=64 for ZB/ZN (64 ticks per point); 32 for everything else.
func parsePrice(s string, fracDivisor int64) (decimal.Decimal, error) {
	if i := strings.Index(s, "''"); i >= 0 {
		wholeStr := s[:i]
		tickStr := s[i+2:]
		whole, err := decimal.NewFromString(wholeStr)
		if err != nil {
			return decimal.Zero, fmt.Errorf("parse tick-notation whole %q: %w", wholeStr, err)
		}
		ticks, err := decimal.NewFromString(tickStr)
		if err != nil {
			return decimal.Zero, fmt.Errorf("parse tick-notation fraction %q: %w", tickStr, err)
		}
		return whole.Add(ticks.Div(decimal.NewFromInt(fracDivisor))), nil
	}
	d, err := decimal.NewFromString(s)
	if err != nil {
		return decimal.Zero, err
	}
	return d, nil
}

// parseQty parses Schwab's signed quantity strings ("+2", "-1") and returns
// the absolute value. Sign is encoded in the Action field.
func parseQty(s string) (decimal.Decimal, error) {
	s = strings.TrimSpace(s)
	s = strings.TrimPrefix(s, "+")
	d, err := decimal.NewFromString(s)
	if err != nil {
		return decimal.Zero, err
	}
	return d.Abs(), nil
}
