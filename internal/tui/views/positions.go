package views

import (
	"context"
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"

	"github.com/charmbracelet/bubbles/table"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/shopspring/decimal"

	pb "trade-tracker-go/gen/tradetracker/v1"
	"trade-tracker-go/internal/tui/client"
)

type positionsLoadedMsg struct {
	positions []*pb.Position
	err       error
}

type chainDetailLoadedMsg struct {
	posIdx int
	chain  *pb.ChainDetail
	err    error
}

// PositionsView shows the open positions table.
type PositionsView struct {
	client     client.Client
	table      table.Model
	positions  []*pb.Position
	loading    bool
	err        error
	width      int
	height     int
	chains     map[int]*pb.ChainDetail // posIdx → loaded chain
	loadingSet map[int]bool            // posIdx → currently fetching
}

func NewPositionsView(c client.Client) PositionsView {
	return PositionsView{
		client:     c,
		loading:    true,
		chains:     make(map[int]*pb.ChainDetail),
		loadingSet: make(map[int]bool),
	}
}

func (v PositionsView) Update(msg tea.Msg, state SharedState) (PositionsView, tea.Cmd) {
	switch msg := msg.(type) {
	case LoadMsg:
		v.loading = true
		v.err = nil
		v.chains = make(map[int]*pb.ChainDetail)
		v.loadingSet = make(map[int]bool)
		return v, v.load(state)

	case positionsLoadedMsg:
		v.loading = false
		if msg.err != nil {
			v.err = msg.err
			return v, nil
		}
		v.chains = make(map[int]*pb.ChainDetail)
		v.loadingSet = make(map[int]bool)
		v.positions = msg.positions
		v.table = buildPositionsTable(v.positions, v.chains, v.loadingSet, v.width, v.height, true)
		return v, nil

	case chainDetailLoadedMsg:
		delete(v.loadingSet, msg.posIdx)
		if msg.err == nil {
			v.chains[msg.posIdx] = msg.chain
		}
		cursor := v.table.Cursor()
		v.table = buildPositionsTable(v.positions, v.chains, v.loadingSet, v.width, v.height, true)
		v.table.SetCursor(cursor)
		return v, nil

	case tea.KeyMsg:
		if !v.loading && v.err == nil {
			switch msg.String() {
			case "enter":
				if len(v.positions) == 0 {
					return v, nil
				}
				cursor := v.table.Cursor()
				posIdx := positionIdxAtCursor(v.positions, v.chains, v.loadingSet, cursor)
				if posIdx < 0 {
					return v, nil // on a detail row
				}
				if v.positions[posIdx].StrategyType == pb.StrategyType_STRATEGY_TYPE_STOCK {
					return v, nil // no drill-down for stock positions
				}
				if v.chains[posIdx] != nil {
					// Collapse.
					delete(v.chains, posIdx)
					v.table = buildPositionsTable(v.positions, v.chains, v.loadingSet, v.width, v.height, true)
					v.table.SetCursor(visualRowOf(v.positions, v.chains, v.loadingSet, posIdx))
					return v, nil
				}
				if v.loadingSet[posIdx] {
					// Cancel in-flight load.
					delete(v.loadingSet, posIdx)
					v.table = buildPositionsTable(v.positions, v.chains, v.loadingSet, v.width, v.height, true)
					v.table.SetCursor(visualRowOf(v.positions, v.chains, v.loadingSet, posIdx))
					return v, nil
				}
				// Expand: start async load.
				v.loadingSet[posIdx] = true
				v.table = buildPositionsTable(v.positions, v.chains, v.loadingSet, v.width, v.height, true)
				v.table.SetCursor(cursor)
				pos := v.positions[posIdx]
				c := v.client
				accountID, chainID := pos.AccountId, pos.ChainId
				idx := posIdx
				return v, func() tea.Msg {
					chain, err := c.GetChain(context.Background(), accountID, chainID)
					return chainDetailLoadedMsg{posIdx: idx, chain: chain, err: err}
				}

			case "e":
				var cmds []tea.Cmd
				for idx, pos := range v.positions {
					if pos.StrategyType == pb.StrategyType_STRATEGY_TYPE_STOCK {
						continue // stock positions have no chain detail
					}
					if v.chains[idx] != nil || v.loadingSet[idx] {
						continue
					}
					v.loadingSet[idx] = true
					c := v.client
					accountID, chainID := pos.AccountId, pos.ChainId
					i := idx
					cmds = append(cmds, func() tea.Msg {
						chain, err := c.GetChain(context.Background(), accountID, chainID)
						return chainDetailLoadedMsg{posIdx: i, chain: chain, err: err}
					})
				}
				if len(cmds) > 0 {
					cursor := v.table.Cursor()
					v.table = buildPositionsTable(v.positions, v.chains, v.loadingSet, v.width, v.height, true)
					v.table.SetCursor(cursor)
					return v, tea.Batch(cmds...)
				}
				return v, nil

			case "esc":
				if len(v.chains) > 0 || len(v.loadingSet) > 0 {
					cursor := v.table.Cursor()
					v.chains = make(map[int]*pb.ChainDetail)
					v.loadingSet = make(map[int]bool)
					v.table = buildPositionsTable(v.positions, v.chains, v.loadingSet, v.width, v.height, true)
					v.table.SetCursor(cursor)
					return v, nil
				}
			}
			var cmd tea.Cmd
			v.table, cmd = v.table.Update(msg)
			return v, cmd
		}
	}
	return v, nil
}

func (v PositionsView) View() string {
	if v.loading {
		return "Loading positions..."
	}
	if v.err != nil {
		return errorViewStyle.Render(fmt.Sprintf("Error: %v", v.err))
	}
	return tableStyle.Render(v.table.View())
}

func (v *PositionsView) Resize(w, h int) {
	v.width = w
	v.height = h
	if len(v.positions) > 0 {
		cursor := v.table.Cursor()
		v.table = buildPositionsTable(v.positions, v.chains, v.loadingSet, w, h, true)
		v.table.SetCursor(cursor)
	}
}

func (v PositionsView) load(state SharedState) tea.Cmd {
	ids := accountIDs(state)
	c := v.client
	return func() tea.Msg {
		var all []*pb.Position
		var lastErr error
		for _, id := range ids {
			resp, err := c.ListPositions(context.Background(), id, pb.PositionStatus_POSITION_STATUS_OPEN)
			if err != nil {
				lastErr = err
				continue
			}
			all = append(all, resp...)
		}
		if len(all) == 0 && lastErr != nil {
			return positionsLoadedMsg{err: lastErr}
		}
		return positionsLoadedMsg{positions: all}
	}
}

// symbolLess orders symbols with "/" (futures) before all others, then alphabetically.
func symbolLess(a, b string) bool {
	aFut := strings.HasPrefix(a, "/")
	bFut := strings.HasPrefix(b, "/")
	if aFut != bFut {
		return aFut
	}
	return a < b
}

// posDisplayOrder returns sorted indices into positions (futures first, then alpha).
func posDisplayOrder(positions []*pb.Position) []int {
	indices := make([]int, len(positions))
	for i := range indices {
		indices[i] = i
	}
	sort.SliceStable(indices, func(a, b int) bool {
		return symbolLess(positions[indices[a]].UnderlyingSymbol, positions[indices[b]].UnderlyingSymbol)
	})
	return indices
}

// visualRowOf returns the visual table row index for a given position index after
// detail rows are accounted for. Used to restore cursor after collapsing a detail.
func visualRowOf(positions []*pb.Position, chains map[int]*pb.ChainDetail, loadingSet map[int]bool, targetIdx int) int {
	row := 0
	for _, idx := range posDisplayOrder(positions) {
		if idx == targetIdx {
			return row
		}
		row++
		if chain := chains[idx]; chain != nil {
			row += 2 + len(computeOpenLegs(chain.Events))
		} else if loadingSet[idx] {
			row++
		}
	}
	return 0
}

// positionIdxAtCursor maps a table cursor back to the original position index,
// accounting for all injected detail rows. Returns -1 if cursor is on a detail row.
func positionIdxAtCursor(positions []*pb.Position, chains map[int]*pb.ChainDetail, loadingSet map[int]bool, cursor int) int {
	row := 0
	for _, idx := range posDisplayOrder(positions) {
		if row == cursor {
			return idx
		}
		row++
		if chain := chains[idx]; chain != nil {
			row += 2 + len(computeOpenLegs(chain.Events))
		} else if loadingSet[idx] {
			row++
		}
	}
	return -1
}

// buildPositionsTable builds a unified table for all positions (options, futures, stock).
// Stock positions (StrategyType == STOCK) show Qty and WAC cost basis; options show cost
// basis (P&L credit/debit). showOpen=false switches to the history layout.
func buildPositionsTable(positions []*pb.Position, chains map[int]*pb.ChainDetail, loadingSet map[int]bool, w, h int, showOpen bool) table.Model {
	var cols []table.Column
	if showOpen {
		cols = []table.Column{
			{Title: "Symbol", Width: 14},
			{Title: "Strategy", Width: 10},
			{Title: "Qty", Width: 8},
			{Title: "Cost Basis", Width: pnlColumnWidth},
			{Title: "Total Basis", Width: pnlColumnWidth},
			{Title: "Opened", Width: 12},
		}
	} else {
		cols = []table.Column{
			{Title: "Symbol", Width: 14},
			{Title: "Strategy", Width: 10},
			{Title: "Realized P&L", Width: pnlColumnWidth},
			{Title: "Opened", Width: 12},
			{Title: "Closed", Width: 12},
			{Title: "Days", Width: 6},
		}
	}

	// extra builds a detail sub-row for chain expansion (open view, 6 columns).
	extra := func(vals ...string) table.Row { return table.Row(vals) }

	var rows []table.Row

	for _, idx := range posDisplayOrder(positions) {
		p := positions[idx]
		isStock := p.StrategyType == pb.StrategyType_STRATEGY_TYPE_STOCK

		symbol := p.UnderlyingSymbol
		if p.ChainAttributionGap {
			symbol = "[!] " + symbol
		}
		openedAt := "—"
		if p.OpenedAt != nil {
			openedAt = formatTS(p.OpenedAt.AsTime())
		}

		var row table.Row
		if showOpen {
			var qty, costBasis, totalBasis string
			if isStock {
				qty = p.NetQuantity
				costBasis = formatCurrency(p.AvgCostPerShare)
				netQty, _ := decimal.NewFromString(p.NetQuantity)
				avgCost, _ := decimal.NewFromString(p.AvgCostPerShare)
				totalBasis = formatCurrency(netQty.Mul(avgCost).String())
			} else {
				qty = " "
				costBasis = formatPnl(p.CostBasis)
				totalBasis = " "
			}
			row = table.Row{symbol, strategyLabel(p.StrategyType.String()), qty, costBasis, totalBasis, openedAt}
		} else {
			closedAt := "—"
			daysOpen := "—"
			if p.ClosedAt != nil {
				closedAt = formatTS(p.ClosedAt.AsTime())
				if p.OpenedAt != nil {
					days := int(p.ClosedAt.AsTime().Sub(p.OpenedAt.AsTime()).Hours() / 24)
					daysOpen = strconv.Itoa(days)
				}
			}
			row = table.Row{symbol, strategyLabel(p.StrategyType.String()), formatPnl(p.RealizedPnl), openedAt, closedAt, daysOpen}
		}
		rows = append(rows, row)

		if showOpen && !isStock {
			if chain := chains[idx]; chain != nil {
				// Columns: Symbol(14) | Strategy(10) | Qty(8) | CostBasis(20) | TotalBasis(20) | Opened(12)
				// Expiry goes to CostBasis (width 20) not Qty (width 8) so dates aren't truncated.
				rows = append(rows, extra("  Qty  Symbol", "Strike", "", "Expiry", "", ""))
				rows = append(rows, extra(
					"  "+strings.Repeat("─", 12),
					strings.Repeat("─", 8),
					"",
					strings.Repeat("─", 10),
					"", "",
				))
				for _, leg := range computeOpenLegs(chain.Events) {
					rows = append(rows, extra(leg[0], leg[1], "", leg[2], "", ""))
				}
			} else if loadingSet[idx] {
				rows = append(rows, extra("  Loading...", "", "", "", "", ""))
			}
		}
	}

	t := table.New(
		table.WithColumns(cols),
		table.WithRows(rows),
		table.WithFocused(true),
		table.WithHeight(h-4),
	)
	t.SetStyles(defaultTableStyles())
	return t
}

// computeOpenLegs derives current open lots from chain events by netting quantities.
// Returns one display row per open lot: [qty+symbol, strike+type, expiry].
func computeOpenLegs(events []*pb.ChainEvent) [][3]string {
	type key struct{ symbol, strike, optType, expiry string }
	net := map[key]float64{}
	instMap := map[key]struct{ underlying, strike, optType, expiry string }{}

	for _, ev := range events {
		for _, leg := range ev.Legs {
			if leg.Instrument == nil {
				continue
			}
			inst := leg.Instrument
			k := key{symbol: inst.Symbol}
			strike, optType, expiry := "", "", ""

			if inst.Option != nil {
				strike = formatStrike(inst.Option.Strike)
				switch inst.Option.OptionType {
				case pb.OptionType_OPTION_TYPE_CALL:
					optType = "C"
				case pb.OptionType_OPTION_TYPE_PUT:
					optType = "P"
				}
				if inst.Option.Expiration != nil {
					expiry = inst.Option.Expiration.AsTime().Format("01/02/2006")
				}
				k.strike = inst.Option.Strike
				k.optType = optType
				k.expiry = expiry
			}
			instMap[k] = struct{ underlying, strike, optType, expiry string }{inst.Symbol, strike, optType, expiry}

			qty, _ := strconv.ParseFloat(leg.Quantity, 64)
			net[k] += legSign(leg.Action) * qty
		}
	}

	var rows [][3]string
	for k, q := range net {
		if math.Abs(q) < 0.001 {
			continue
		}
		info := instMap[k]
		col1 := fmt.Sprintf("  %+g %s", q, info.underlying)
		col2 := info.strike + info.optType
		col3 := info.expiry
		rows = append(rows, [3]string{col1, col2, col3})
	}

	sort.Slice(rows, func(i, j int) bool {
		return rows[i][1]+rows[i][2] > rows[j][1]+rows[j][2]
	})
	return rows
}

// legSign returns +1 for actions that increase net position, -1 for those that decrease it.
func legSign(a pb.Action) float64 {
	switch a {
	case pb.Action_ACTION_BTO, pb.Action_ACTION_BTC, pb.Action_ACTION_BUY, pb.Action_ACTION_ASSIGNMENT:
		return 1
	default: // STO, STC, SELL, EXPIRATION, EXERCISE
		return -1
	}
}

// formatStrike strips trailing decimal zeros: "425.00" → "425", "4500.50" → "4500.50".
func formatStrike(s string) string {
	v, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return s
	}
	if v == math.Trunc(v) {
		return fmt.Sprintf("%d", int(v))
	}
	return fmt.Sprintf("%.2f", v)
}
