package tastytrade_test

import (
	"os"
	"strings"
	"testing"
	"time"

	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"trade-tracker-go/internal/broker/tastytrade"
	"trade-tracker-go/internal/domain"
)

const (
	accountID    = "acct-test-001"
	realDataFile = "../../../data/tastytrade_transactions_history_x5WX10211_260101_to_260406.csv"
)

// header is the Tastytrade CSV header row.
const header = "Date,Type,Sub Type,Action,Symbol,Instrument Type,Description,Value,Quantity,Average Price,Commissions,Fees,Multiplier,Root Symbol,Underlying Symbol,Expiration Date,Strike Price,Call or Put,Order #,Total,Currency\n"

func parse(t *testing.T, csv string) []domain.Transaction {
	t.Helper()
	p := &tastytrade.Parser{}
	txs, err := p.Parse(strings.NewReader(header+csv), accountID)
	require.NoError(t, err)
	return txs
}

func TestParser_EquityOption_BTC(t *testing.T) {
	row := "2026-04-02T06:46:35-0700,Trade,Buy to Close,BUY_TO_CLOSE,UAL   260417C00110000,Equity Option,Bought 2 UAL 04/17/26 Call 110.00 @ 0.62,-124.00,2,-62.00,0.00,-0.25,100,UAL,UAL,4/17/26,110,CALL,451551137,-124.25,USD\n"
	txs := parse(t, row)
	require.Len(t, txs, 1)
	tx := txs[0]

	assert.Equal(t, tastytrade.Broker, tx.Broker)
	assert.Equal(t, accountID, tx.AccountID)
	assert.Equal(t, domain.ActionBTC, tx.Action)
	assert.Equal(t, domain.PositionEffectClosing, tx.PositionEffect)
	assert.Equal(t, "451551137", tx.BrokerOrderID)
	assert.True(t, decimal.NewFromInt(2).Equal(tx.Quantity))
	// Average Price in Tastytrade CSV is per-contract (62.00); parser normalizes to per-share (0.62).
	assert.True(t, decimal.NewFromFloat(0.62).Equal(tx.FillPrice))

	// Fees: -(0.00 + -0.25) = 0.25
	assert.True(t, decimal.NewFromFloat(0.25).Equal(tx.Fees))

	inst := tx.Instrument
	assert.Equal(t, "UAL", inst.Symbol)
	assert.Equal(t, domain.AssetClassEquityOption, inst.AssetClass)
	require.NotNil(t, inst.Option)
	assert.Equal(t, domain.OptionTypeCall, inst.Option.OptionType)
	assert.True(t, decimal.NewFromInt(110).Equal(inst.Option.Strike))
	assert.True(t, decimal.NewFromInt(100).Equal(inst.Option.Multiplier))
	assert.Equal(t, time.Date(2026, 4, 17, 0, 0, 0, 0, time.UTC), inst.Option.Expiration)
}

func TestParser_EquityOption_STO(t *testing.T) {
	row := "2026-04-01T09:16:26-0700,Trade,Sell to Open,SELL_TO_OPEN,IWM   260515P00230000,Equity Option,Sold 2 IWM 05/15/26 Put 230.00 @ 3.23,646.00,2,323.00,-2.00,-0.26,100,IWM,IWM,5/15/26,230,PUT,451325787,643.74,USD\n"
	txs := parse(t, row)
	require.Len(t, txs, 1)
	tx := txs[0]

	assert.Equal(t, domain.ActionSTO, tx.Action)
	assert.Equal(t, domain.PositionEffectOpening, tx.PositionEffect)
	assert.Equal(t, "IWM", tx.Instrument.Symbol)
	assert.Equal(t, domain.OptionTypePut, tx.Instrument.Option.OptionType)
	assert.True(t, decimal.NewFromInt(230).Equal(tx.Instrument.Option.Strike))

	// Fees: -(-2.00 + -0.26) = 2.26
	assert.True(t, decimal.NewFromFloat(2.26).Equal(tx.Fees))
}

func TestParser_FutureOption(t *testing.T) {
	row := "2026-03-30T08:51:16-0700,Trade,Sell to Open,SELL_TO_OPEN,./MESM6EXK6  260529P5800,Future Option,Sold 1 /MESM6 EXK6 05/29/26 Put 5800.00 @ 82.5,412.50,1,412.50,-0.75,-0.46,1,\"./MESM6EXK6  \",/MESM6,5/29/26,5800,PUT,450551994,411.29,USD\n"
	txs := parse(t, row)
	require.Len(t, txs, 1)
	tx := txs[0]

	assert.Equal(t, domain.ActionSTO, tx.Action)
	inst := tx.Instrument
	assert.Equal(t, "/MESM6", inst.Symbol)
	assert.Equal(t, domain.AssetClassFutureOption, inst.AssetClass)
	require.NotNil(t, inst.Option)
	assert.Equal(t, domain.OptionTypePut, inst.Option.OptionType)
	assert.True(t, decimal.NewFromInt(5800).Equal(inst.Option.Strike))
	assert.True(t, decimal.NewFromInt(1).Equal(inst.Option.Multiplier))
	assert.Equal(t, time.Date(2026, 5, 29, 0, 0, 0, 0, time.UTC), inst.Option.Expiration)
}

func TestParser_Equity_BuyToOpen(t *testing.T) {
	row := "2026-03-26T09:27:20-0700,Trade,Buy to Open,BUY_TO_OPEN,NEHI,Equity,Bought 50 NEHI @ 32.52,\"-1,625.99\",50,-32.52,0.00,-0.04,,,,,,,449808348,\"-1,626.03\",USD\n"
	txs := parse(t, row)
	require.Len(t, txs, 1)
	tx := txs[0]

	assert.Equal(t, domain.ActionBTO, tx.Action)
	assert.Equal(t, domain.PositionEffectOpening, tx.PositionEffect)
	assert.Equal(t, "NEHI", tx.Instrument.Symbol)
	assert.Equal(t, domain.AssetClassEquity, tx.Instrument.AssetClass)
	assert.Nil(t, tx.Instrument.Option)
	assert.True(t, decimal.NewFromInt(50).Equal(tx.Quantity))
	// FillPrice is absolute value: |-32.52| = 32.52
	assert.True(t, decimal.NewFromFloat(32.52).Equal(tx.FillPrice))
}

func TestParser_Future_Sell(t *testing.T) {
	// subType "Sell" does not contain "to open", so the ActionSell/Closing fallback is used.
	// This confirms the existing long-close path is unaffected by the subType disambiguation.
	row := "2025-10-14T07:20:03-0700,Trade,Sell,SELL,/MCLZ5,Future,Sold 1 /MCLZ5 @ 57.92,0.00,1,0.00,-0.75,-0.82,,,,,,,413432751,-1.57,USD\n"
	txs := parse(t, row)
	require.Len(t, txs, 1)
	tx := txs[0]

	assert.Equal(t, domain.ActionSell, tx.Action)
	assert.Equal(t, domain.PositionEffectClosing, tx.PositionEffect)
	assert.Equal(t, "/MCLZ5", tx.Instrument.Symbol)
	assert.Equal(t, domain.AssetClassFuture, tx.Instrument.AssetClass)
	assert.Nil(t, tx.Instrument.Option)
	assert.NotNil(t, tx.Instrument.Future)
	assert.True(t, decimal.NewFromInt(1).Equal(tx.Quantity))
	assert.True(t, decimal.NewFromFloat(0.75+0.82).Equal(tx.Fees))
}

func TestParser_Future_Buy(t *testing.T) {
	row := "2025-10-13T09:15:00-0700,Trade,Buy,BUY,/MCLZ5,Future,Bought 1 /MCLZ5 @ 57.10,0.00,1,0.00,-0.75,-0.82,,,,,,,413432750,-1.57,USD\n"
	txs := parse(t, row)
	require.Len(t, txs, 1)
	tx := txs[0]

	assert.Equal(t, domain.ActionBuy, tx.Action)
	assert.Equal(t, domain.PositionEffectOpening, tx.PositionEffect)
	assert.Equal(t, "/MCLZ5", tx.Instrument.Symbol)
	assert.Equal(t, domain.AssetClassFuture, tx.Instrument.AssetClass)
}

func TestParser_Future_SellToOpen(t *testing.T) {
	// Short future: subType "Sell to Open" → ActionSTO, Opening.
	row := "2025-10-14T07:20:03-0700,Trade,Sell to Open,SELL,/MCLZ5,Future,Sold 1 /MCLZ5 @ 57.92,0.00,1,0.00,-0.75,-0.82,,,,,,,413432751,-1.57,USD\n"
	txs := parse(t, row)
	require.Len(t, txs, 1)
	tx := txs[0]

	assert.Equal(t, domain.ActionSTO, tx.Action)
	assert.Equal(t, domain.PositionEffectOpening, tx.PositionEffect)
	assert.Equal(t, "/MCLZ5", tx.Instrument.Symbol)
	assert.Equal(t, domain.AssetClassFuture, tx.Instrument.AssetClass)
}

func TestParser_Future_BuyToClose(t *testing.T) {
	// Cover short future: subType "Buy to Close" → ActionBTC, Closing.
	row := "2025-10-15T09:15:00-0700,Trade,Buy to Close,BUY,/MCLZ5,Future,Bought 1 /MCLZ5 @ 56.50,0.00,1,0.00,-0.75,-0.82,,,,,,,413432752,-1.57,USD\n"
	txs := parse(t, row)
	require.Len(t, txs, 1)
	tx := txs[0]

	assert.Equal(t, domain.ActionBTC, tx.Action)
	assert.Equal(t, domain.PositionEffectClosing, tx.PositionEffect)
	assert.Equal(t, "/MCLZ5", tx.Instrument.Symbol)
	assert.Equal(t, domain.AssetClassFuture, tx.Instrument.AssetClass)
}

func TestParser_Equity_SellToOpen(t *testing.T) {
	// Short equity: subType "Sell to Open" with bare SELL action → ActionSTO, Opening.
	// This is the primary parser path for equity short-sells.
	row := "2026-04-10T09:30:00-0700,Trade,Sell to Open,SELL,TSLA,Equity,Sold 10 TSLA @ 200.00,-2000.00,10,-200.00,0.00,-0.05,,,,,,,499000001,-2000.05,USD\n"
	txs := parse(t, row)
	require.Len(t, txs, 1)
	tx := txs[0]

	assert.Equal(t, domain.ActionSTO, tx.Action)
	assert.Equal(t, domain.PositionEffectOpening, tx.PositionEffect)
	assert.Equal(t, "TSLA", tx.Instrument.Symbol)
	assert.Equal(t, domain.AssetClassEquity, tx.Instrument.AssetClass)
	assert.True(t, decimal.NewFromFloat(10).Equal(tx.Quantity))
	assert.True(t, decimal.NewFromFloat(200).Equal(tx.FillPrice))
}

func TestParser_MultiLegSameTradeID(t *testing.T) {
	// 4-leg iron condor: all legs share Order # 451325787
	rows := "2026-04-01T09:16:26-0700,Trade,Buy to Open,BUY_TO_OPEN,IWM   260515P00220000,Equity Option,Bought 2 IWM 05/15/26 Put 220.00 @ 1.94,-388.00,2,-194.00,-2.00,-0.25,100,IWM,IWM,5/15/26,220,PUT,451325787,-390.25,USD\n" +
		"2026-04-01T09:16:26-0700,Trade,Buy to Open,BUY_TO_OPEN,IWM   260515C00280000,Equity Option,Bought 2 IWM 05/15/26 Call 280.00 @ 0.79,-158.00,2,-79.00,-2.00,-0.25,100,IWM,IWM,5/15/26,280,CALL,451325787,-160.25,USD\n" +
		"2026-04-01T09:16:26-0700,Trade,Sell to Open,SELL_TO_OPEN,IWM   260515P00230000,Equity Option,Sold 2 IWM 05/15/26 Put 230.00 @ 3.23,646.00,2,323.00,-2.00,-0.26,100,IWM,IWM,5/15/26,230,PUT,451325787,643.74,USD\n" +
		"2026-04-01T09:16:26-0700,Trade,Sell to Open,SELL_TO_OPEN,IWM   260515C00270000,Equity Option,Sold 2 IWM 05/15/26 Call 270.00 @ 2.41,482.00,2,241.00,-2.00,-0.26,100,IWM,IWM,5/15/26,270,CALL,451325787,479.74,USD\n"

	txs := parse(t, rows)
	require.Len(t, txs, 4)

	tradeID := txs[0].TradeID
	for _, tx := range txs {
		assert.Equal(t, tradeID, tx.TradeID, "all legs must share the same TradeID")
	}
}

func TestParser_DifferentOrdersDifferentTradeIDs(t *testing.T) {
	rows := "2026-04-02T06:46:35-0700,Trade,Sell to Close,SELL_TO_CLOSE,UAL   260417C00115000,Equity Option,Sold 2 UAL 04/17/26 Call 115.00 @ 0.36,72.00,2,36.00,0.00,-0.27,100,UAL,UAL,4/17/26,115,CALL,451551137,71.73,USD\n" +
		"2026-04-01T09:16:26-0700,Trade,Sell to Open,SELL_TO_OPEN,IWM   260515P00230000,Equity Option,Sold 2 IWM 05/15/26 Put 230.00 @ 3.23,646.00,2,323.00,-2.00,-0.26,100,IWM,IWM,5/15/26,230,PUT,451325787,643.74,USD\n"

	txs := parse(t, rows)
	require.Len(t, txs, 2)
	assert.NotEqual(t, txs[0].TradeID, txs[1].TradeID)
}

func TestParser_SkipsMoneyMovement(t *testing.T) {
	rows := "2026-03-31T14:00:00-0700,Money Movement,Dividend,,SVOL,Equity,SIMPLIFY EXCHANGE TRADED FUNDS,36.95,0,,--,0.00,,,,,,,,36.95,USD\n" +
		"2026-03-28T11:57:24-0700,Money Movement,Balance Adjustment,,,,Regulatory fee adjustment,-0.03,0,,--,0.00,,,,,,,,-0.03,USD\n"

	txs := parse(t, rows)
	assert.Empty(t, txs, "Money Movement rows must be skipped")
}

func TestParser_SkipsEmptyAction(t *testing.T) {
	// Assignment removal row has no Action.
	row := "2026-03-17T14:00:00-0700,Receive Deliver,Assignment,,CHWY  260320P00030000,Equity Option,Removal of option due to assignment,0.00,3,0.00,--,0.00,100,CHWY,CHWY,3/20/26,30,PUT,,0.00,USD\n"
	txs := parse(t, row)
	assert.Empty(t, txs)
}

func TestParser_Expiration(t *testing.T) {
	// Expiration rows have BUY_TO_CLOSE / SELL_TO_CLOSE with zero price.
	row := "2026-03-20T13:00:00-0700,Receive Deliver,Expiration,SELL_TO_CLOSE,AAPL  260320C00295000,Equity Option,Removal of 1.0 AAPL 03/20/26 Call 295.00 due to expiration.,0.00,1,0.00,--,0.00,100,AAPL,AAPL,3/20/26,295,CALL,,0.00,USD\n"
	txs := parse(t, row)
	require.Len(t, txs, 1)
	tx := txs[0]

	assert.Equal(t, domain.ActionSTC, tx.Action)
	assert.Equal(t, domain.PositionEffectClosing, tx.PositionEffect)
	assert.True(t, decimal.Zero.Equal(tx.FillPrice))
	assert.True(t, decimal.Zero.Equal(tx.Fees))
}

func TestParser_FeesWithDash(t *testing.T) {
	// "--" in Commissions means no commission; fees = -(0 + 0) = 0.
	row := "2026-03-31T14:00:00-0700,Receive Deliver,Dividend,BUY_TO_OPEN,SVOL,Equity,Received 2.41503 Long SVOL via Dividend,0.00,2.41503,0.00,--,0.00,,,,,,,,0.00,USD\n"
	txs := parse(t, row)
	require.Len(t, txs, 1)
	assert.True(t, decimal.Zero.Equal(txs[0].Fees))
}

func TestParser_BrokerTxIDDeterministic(t *testing.T) {
	row := "2026-04-02T06:46:35-0700,Trade,Buy to Close,BUY_TO_CLOSE,UAL   260417C00110000,Equity Option,Bought 2 UAL 04/17/26 Call 110.00 @ 0.62,-124.00,2,-62.00,0.00,-0.25,100,UAL,UAL,4/17/26,110,CALL,451551137,-124.25,USD\n"

	p := &tastytrade.Parser{}
	txs1, err := p.Parse(strings.NewReader(header+row), accountID)
	require.NoError(t, err)
	txs2, err := p.Parse(strings.NewReader(header+row), accountID)
	require.NoError(t, err)

	require.Len(t, txs1, 1)
	require.Len(t, txs2, 1)
	assert.Equal(t, txs1[0].BrokerTxID, txs2[0].BrokerTxID)
	assert.Equal(t, txs1[0].TradeID, txs2[0].TradeID)
}

func TestParser_RealFile(t *testing.T) {
	f, err := os.Open(realDataFile)
	if os.IsNotExist(err) {
		t.Skip("real data file not present")
	}
	require.NoError(t, err)
	defer func() { _ = f.Close() }()

	p := &tastytrade.Parser{}
	txs, err := p.Parse(f, accountID)
	require.NoError(t, err)

	assert.NotEmpty(t, txs)

	// Every transaction must have required fields populated.
	tradeIDs := make(map[string]struct{})
	brokerTxIDs := make(map[string]struct{})
	for i, tx := range txs {
		assert.NotEmpty(t, tx.ID, "tx[%d] missing ID", i)
		assert.NotEmpty(t, tx.TradeID, "tx[%d] missing TradeID", i)
		assert.NotEmpty(t, tx.BrokerTxID, "tx[%d] missing BrokerTxID", i)
		assert.NotEmpty(t, tx.Broker, "tx[%d] missing Broker", i)
		assert.NotEmpty(t, tx.AccountID, "tx[%d] missing AccountID", i)
		assert.NotEmpty(t, tx.Instrument.Symbol, "tx[%d] missing Instrument.Symbol", i)
		assert.False(t, tx.ExecutedAt.IsZero(), "tx[%d] zero ExecutedAt", i)
		assert.True(t, tx.Quantity.IsPositive(), "tx[%d] non-positive Quantity", i)
		assert.False(t, tx.Fees.IsNegative(), "tx[%d] negative Fees", i)

		brokerTxIDs[tx.BrokerTxID] = struct{}{}
		tradeIDs[tx.TradeID] = struct{}{}
	}

	// BrokerTxIDs must be unique across the file.
	assert.Equal(t, len(txs), len(brokerTxIDs), "duplicate BrokerTxIDs detected")

	t.Logf("parsed %d transactions across %d trades", len(txs), len(tradeIDs))
}
