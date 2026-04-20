package tui

// ViewID identifies which top-level view is active.
type ViewID int

const (
	ViewAccounts  ViewID = iota
	ViewPositions        // open positions
	ViewHistory          // closed positions
	ViewTrades
	ViewAnalytics
	ViewImport
	viewCount
)
