package views

import (
	"fmt"
	"strconv"
	"time"

	"github.com/charmbracelet/bubbles/table"
	"github.com/charmbracelet/lipgloss"
)

func defaultTableStyles() table.Styles {
	s := table.DefaultStyles()
	s.Header = s.Header.
		BorderStyle(lipgloss.NormalBorder()).
		BorderForeground(lipgloss.Color("8")).
		BorderBottom(true).
		Bold(true)
	s.Selected = s.Selected.
		Foreground(lipgloss.Color("15")).
		Background(lipgloss.Color("4")).
		Bold(false)
	return s
}

// parseDecimalOrZero parses a decimal string (as returned by the server) or
// returns 0 on any error.
func parseDecimalOrZero(s string) float64 {
	if s == "" {
		return 0
	}
	v, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return 0
	}
	return v
}

var (
	pnlPositiveStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("10"))
	pnlNegativeStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("9"))
)

// formatPnl formats a decimal string as a coloured currency value.
func formatPnl(s string) string {
	v := parseDecimalOrZero(s)
	formatted := fmt.Sprintf("$%.2f", v)
	if v > 0 {
		return pnlPositiveStyle.Render(formatted)
	}
	if v < 0 {
		return pnlNegativeStyle.Render(formatted)
	}
	return formatted
}

// formatTS formats a time.Time for display. Returns "—" for the zero value.
func formatTS(ts time.Time) string {
	if ts.IsZero() {
		return "—"
	}
	return ts.Format("2006-01-02")
}

// strategyLabels maps proto enum name strings to short display labels.
var strategyLabels = map[string]string{
	"STRATEGY_TYPE_UNSPECIFIED":            "—",
	"STRATEGY_TYPE_UNKNOWN":                "?",
	"STRATEGY_TYPE_IRON_BUTTERFLY":         "IBfly",
	"STRATEGY_TYPE_IRON_CONDOR":            "IC",
	"STRATEGY_TYPE_BROKEN_HEART_BUTTERFLY": "BHBfly",
	"STRATEGY_TYPE_BUTTERFLY":              "Bfly",
	"STRATEGY_TYPE_BROKEN_WING_BUTTERFLY":  "BWBfly",
	"STRATEGY_TYPE_COVERED_CALL":           "CC",
	"STRATEGY_TYPE_RATIO":                  "Ratio",
	"STRATEGY_TYPE_BACK_RATIO":             "BkRatio",
	"STRATEGY_TYPE_STRADDLE":               "Straddle",
	"STRATEGY_TYPE_STRANGLE":               "Strangle",
	"STRATEGY_TYPE_VERTICAL":               "Vert",
	"STRATEGY_TYPE_CALENDAR":               "Cal",
	"STRATEGY_TYPE_DIAGONAL":               "Diag",
	"STRATEGY_TYPE_SINGLE":                 "Single",
	"STRATEGY_TYPE_STOCK":                  "Stock",
	"STRATEGY_TYPE_FUTURE":                 "Future",
}

// strategyLabel returns a short display string for a strategy type enum value.
func strategyLabel(name string) string {
	if l, ok := strategyLabels[name]; ok {
		return l
	}
	return name
}
