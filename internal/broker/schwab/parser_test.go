package schwab_test

import (
	"os"
	"strings"
	"testing"
	"time"

	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"trade-tracker-go/internal/broker/schwab"
	"trade-tracker-go/internal/domain"
)

const (
	accountID = "acct-test-002"
	// preamble is the two-line header that every Schwab export starts with.
	preamble      = "Account Trade History\n,Exec Time,Spread,Side,Qty,Pos Effect,Symbol,Exp,Strike,Type,Price,Net Price,Order Type\n"
	realDataFile1 = "../../../data/2026-04-06-AccountStatement-029.csv"
	realDataFile2 = "../../../data/2026-04-06-AccountStatement-128.csv"
)

func parse(t *testing.T, csv string) []domain.Transaction {
	t.Helper()
	p := &schwab.Parser{}
	txs, err := p.Parse(strings.NewReader(preamble+csv), accountID)
	require.NoError(t, err)
	return txs
}

func TestParser_SingleEquityOption_SellToOpen(t *testing.T) {
	rows := ",3/24/26 12:34:32,SINGLE,SELL,-1,TO OPEN,MSFT,17 APR 26,397.5,CALL,2.34,2.34,LMT\n"
	txs := parse(t, rows)
	require.Len(t, txs, 1)
	tx := txs[0]

	assert.Equal(t, schwab.Broker, tx.Broker)
	assert.Equal(t, accountID, tx.AccountID)
	assert.Equal(t, domain.ActionSTO, tx.Action)
	assert.Equal(t, domain.PositionEffectOpening, tx.PositionEffect)
	assert.True(t, decimal.NewFromInt(1).Equal(tx.Quantity))
	assert.True(t, decimal.NewFromFloat(2.34).Equal(tx.FillPrice))
	assert.True(t, decimal.Zero.Equal(tx.Fees), "Schwab exports have no fee data")

	inst := tx.Instrument
	assert.Equal(t, "MSFT", inst.Symbol)
	assert.Equal(t, domain.AssetClassEquityOption, inst.AssetClass)
	require.NotNil(t, inst.Option)
	assert.Equal(t, domain.OptionTypeCall, inst.Option.OptionType)
	assert.True(t, decimal.NewFromFloat(397.5).Equal(inst.Option.Strike))
	assert.True(t, decimal.NewFromInt(100).Equal(inst.Option.Multiplier))
	assert.Equal(t, time.Date(2026, 4, 17, 0, 0, 0, 0, time.UTC), inst.Option.Expiration)
}

func TestParser_IronCondor_FourLegs(t *testing.T) {
	rows := ",4/6/26 08:35:42,IRON CONDOR,BUY,+1,TO CLOSE,SPX,15 MAY 26,7050,CALL,10.30,3.85,LMT\n" +
		",,,SELL,-1,TO CLOSE,SPX,15 MAY 26,7070,CALL,8.65,DEBIT,\n" +
		",,,BUY,+1,TO CLOSE,SPX,15 MAY 26,6090,PUT,51.10,,\n" +
		",,,SELL,-1,TO CLOSE,SPX,15 MAY 26,6070,PUT,48.90,,\n"

	txs := parse(t, rows)
	require.Len(t, txs, 4)

	// All legs share one TradeID.
	tradeID := txs[0].TradeID
	for _, tx := range txs {
		assert.Equal(t, tradeID, tx.TradeID)
	}

	// Actions.
	assert.Equal(t, domain.ActionBTC, txs[0].Action)
	assert.Equal(t, domain.ActionSTC, txs[1].Action)
	assert.Equal(t, domain.ActionBTC, txs[2].Action)
	assert.Equal(t, domain.ActionSTC, txs[3].Action)

	// All closing.
	for _, tx := range txs {
		assert.Equal(t, domain.PositionEffectClosing, tx.PositionEffect)
	}

	// Instruments.
	assert.Equal(t, "SPX", txs[0].Instrument.Symbol)
	assert.Equal(t, domain.AssetClassEquityOption, txs[0].Instrument.AssetClass)
}

func TestParser_Stock_BuyToOpen(t *testing.T) {
	rows := ",3/3/26 08:58:30,STOCK,BUY,+7,TO OPEN,SLV,,,ETF,74.825,74.825,LMT\n"
	txs := parse(t, rows)
	require.Len(t, txs, 1)
	tx := txs[0]

	assert.Equal(t, domain.ActionBTO, tx.Action)
	assert.Equal(t, domain.PositionEffectOpening, tx.PositionEffect)
	assert.Equal(t, "SLV", tx.Instrument.Symbol)
	assert.Equal(t, domain.AssetClassEquity, tx.Instrument.AssetClass)
	assert.Nil(t, tx.Instrument.Option)
	assert.True(t, decimal.NewFromInt(7).Equal(tx.Quantity))
	assert.True(t, decimal.NewFromFloat(74.825).Equal(tx.FillPrice))
}

func TestParser_Future_BuyToOpen(t *testing.T) {
	rows := ",3/30/26 09:50:51,FUTURE,BUY,+1,TO OPEN,/VXMJ26,APR 26,,FUTURE,27.88,27.88,LMT\n"
	txs := parse(t, rows)
	require.Len(t, txs, 1)
	tx := txs[0]

	assert.Equal(t, domain.ActionBTO, tx.Action)
	inst := tx.Instrument
	assert.Equal(t, "/VXMJ26", inst.Symbol)
	assert.Equal(t, domain.AssetClassFuture, inst.AssetClass)
	assert.Nil(t, inst.Option)
	require.NotNil(t, inst.Future)
	assert.Equal(t, time.Date(2026, 4, 1, 0, 0, 0, 0, time.UTC), inst.Future.ExpiryMonth)
}

func TestParser_FutureOption(t *testing.T) {
	rows := ",4/6/26 08:38:00,IRON CONDOR,BUY,+2,TO CLOSE,/NGK26 1/10000 27 APR 26 (Financial) (MAY 26),/LNEK26,3.9,CALL,.011,.031,LMT\n"
	txs := parse(t, rows)
	require.Len(t, txs, 1)
	tx := txs[0]

	inst := tx.Instrument
	assert.Equal(t, "/NGK26", inst.Symbol)
	assert.Equal(t, domain.AssetClassFutureOption, inst.AssetClass)
	require.NotNil(t, inst.Option)
	assert.Equal(t, domain.OptionTypeCall, inst.Option.OptionType)
	assert.True(t, decimal.NewFromFloat(3.9).Equal(inst.Option.Strike))
	// Multiplier is unknown for /NG; stored as 1 pending post-processing.
	assert.True(t, decimal.NewFromInt(1).Equal(inst.Option.Multiplier))
	// FuturesRoot identifies the product family for contract_specs lookup.
	assert.Equal(t, "/NG", inst.FuturesRoot)
	assert.Equal(t, time.Date(2026, 4, 27, 0, 0, 0, 0, time.UTC), inst.Option.Expiration)
}

func TestParser_TwoSeparateOrders_DifferentTradeIDs(t *testing.T) {
	rows := ",4/6/26 08:35:42,IRON CONDOR,BUY,+1,TO CLOSE,SPX,15 MAY 26,7050,CALL,10.30,3.85,LMT\n" +
		",4/6/26 08:34:32,VERTICAL,BUY,+1,TO CLOSE,GS,17 APR 26,835,PUT,15.41,4.06,LMT\n" +
		",,,SELL,-1,TO CLOSE,GS,17 APR 26,820,PUT,11.35,DEBIT,\n"

	txs := parse(t, rows)
	require.Len(t, txs, 3)

	// First trade: 1 leg (SPX single); second trade: 2 legs (GS vertical).
	assert.NotEqual(t, txs[0].TradeID, txs[1].TradeID)
	assert.Equal(t, txs[1].TradeID, txs[2].TradeID)
}

func TestParser_MixedOpenClose_SameTrade(t *testing.T) {
	// Calendars have one OPEN and one CLOSE leg in the same order.
	rows := ",3/30/26 08:58:02,CALENDAR,SELL,-5,TO OPEN,SOFI,15 MAY 26,20,PUT,4.84,.17,LMT\n" +
		",,,BUY,+5,TO CLOSE,SOFI,17 APR 26,20,PUT,4.67,CREDIT,\n"

	txs := parse(t, rows)
	require.Len(t, txs, 2)
	assert.Equal(t, txs[0].TradeID, txs[1].TradeID)
	assert.Equal(t, domain.PositionEffectOpening, txs[0].PositionEffect)
	assert.Equal(t, domain.PositionEffectClosing, txs[1].PositionEffect)
}

func TestParser_BrokerTxIDDeterministic(t *testing.T) {
	rows := ",3/24/26 12:34:32,SINGLE,SELL,-1,TO OPEN,MSFT,17 APR 26,397.5,CALL,2.34,2.34,LMT\n"

	p := &schwab.Parser{}
	txs1, err := p.Parse(strings.NewReader(preamble+rows), accountID)
	require.NoError(t, err)
	txs2, err := p.Parse(strings.NewReader(preamble+rows), accountID)
	require.NoError(t, err)

	require.Len(t, txs1, 1)
	require.Len(t, txs2, 1)
	assert.Equal(t, txs1[0].BrokerTxID, txs2[0].BrokerTxID)
	assert.Equal(t, txs1[0].TradeID, txs2[0].TradeID)
}

func TestParser_QtyAlwaysPositive(t *testing.T) {
	rows := ",3/23/26 08:38:57,STOCK,SELL,-200,TO CLOSE,JD,,,STOCK,27.645,27.645,LMT\n"
	txs := parse(t, rows)
	require.Len(t, txs, 1)
	assert.True(t, txs[0].Quantity.IsPositive(), "quantity must always be positive; sign is in Action")
}

func testRealFile(t *testing.T, path, acctID string) {
	t.Helper()
	f, err := os.Open(path)
	if os.IsNotExist(err) {
		t.Skip("real data file not present")
	}
	require.NoError(t, err)
	defer func() { _ = f.Close() }()

	p := &schwab.Parser{}
	txs, err := p.Parse(f, acctID)
	require.NoError(t, err)
	assert.NotEmpty(t, txs)

	tradeIDs := make(map[string]struct{})
	brokerTxIDs := make(map[string]struct{})
	for i, tx := range txs {
		assert.NotEmpty(t, tx.ID, "tx[%d] missing ID", i)
		assert.NotEmpty(t, tx.TradeID, "tx[%d] missing TradeID", i)
		assert.NotEmpty(t, tx.BrokerTxID, "tx[%d] missing BrokerTxID", i)
		assert.NotEmpty(t, tx.Instrument.Symbol, "tx[%d] missing Instrument.Symbol", i)
		assert.False(t, tx.ExecutedAt.IsZero(), "tx[%d] zero ExecutedAt", i)
		assert.True(t, tx.Quantity.IsPositive(), "tx[%d] non-positive Quantity", i)

		brokerTxIDs[tx.BrokerTxID] = struct{}{}
		tradeIDs[tx.TradeID] = struct{}{}
	}

	assert.Equal(t, len(txs), len(brokerTxIDs), "duplicate BrokerTxIDs detected")
	t.Logf("parsed %d transactions across %d trades", len(txs), len(tradeIDs))
}

func TestParser_RealFile_029(t *testing.T) { testRealFile(t, realDataFile1, "acct-029") }
func TestParser_RealFile_128(t *testing.T) { testRealFile(t, realDataFile2, "acct-128") }

const headerOnly = ",Exec Time,Spread,Side,Qty,Pos Effect,Symbol,Exp,Strike,Type,Price,Net Price,Order Type\n"

func TestParser_AcceptsHeaderOnly(t *testing.T) {
	row := ",3/24/26 12:34:32,SINGLE,SELL,-1,TO OPEN,MSFT,17 APR 26,397.5,CALL,2.34,2.34,LMT\n"
	p := &schwab.Parser{}
	txs, err := p.Parse(strings.NewReader(headerOnly+row), accountID)
	require.NoError(t, err)
	assert.Len(t, txs, 1)
}

func TestParser_RejectsUnrecognizedFirstLine(t *testing.T) {
	p := &schwab.Parser{}
	_, err := p.Parse(strings.NewReader("Account History\n"+headerOnly), accountID)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unexpected first line")
}

func TestParser_RejectsWrongHeader(t *testing.T) {
	p := &schwab.Parser{}
	_, err := p.Parse(strings.NewReader("Account Trade History\n,Exec Time,Spread,Side,Qty,Pos Effect,Symbol,Exp,Strike,Type,Price,Net Price,Order Type,Extra Column\n"), accountID)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unexpected header")
}

func TestParser_AcceptsBOM(t *testing.T) {
	p := &schwab.Parser{}
	row := ",3/24/26 12:34:32,SINGLE,SELL,-1,TO OPEN,MSFT,17 APR 26,397.5,CALL,2.34,2.34,LMT\n"
	_, err := p.Parse(strings.NewReader("\ufeff"+preamble+row), accountID)
	require.NoError(t, err)
}
