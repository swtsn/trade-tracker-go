package service

import (
	"context"
	"time"

	"github.com/shopspring/decimal"
	"trade-tracker-go/internal/domain"
	"trade-tracker-go/internal/strategy"
)

// StrategyClassifier classifies a set of transaction legs into a strategy type.
// *strategy.Classifier satisfies this interface.
type StrategyClassifier interface {
	Classify(legs []strategy.LegShape) domain.StrategyType
}

// TradeChainer creates or extends a chain for a trade and returns the chain ID.
// *ChainService satisfies this interface.
type TradeChainer interface {
	ProcessTrade(ctx context.Context, tradeID string) (string, error)
}

// Importer persists a batch of normalized transactions.
// *ImportService satisfies this interface.
type Importer interface {
	Import(ctx context.Context, txns []domain.Transaction) (*ImportResult, error)
}

// PositionWriter processes trade transactions into position lots.
// Used as the post-import hook path; never called by gRPC handlers.
// *PositionService satisfies this interface.
type PositionWriter interface {
	ProcessTrade(ctx context.Context, tradeID string, txns []domain.Transaction, chainID string) error
}

// PositionReader queries position data.
// Used by the position gRPC handler; never called from the import path.
// *PositionService satisfies this interface.
type PositionReader interface {
	GetPosition(ctx context.Context, accountID, positionID string) (*domain.Position, error)
	ListPositions(ctx context.Context, accountID string, openOnly bool) ([]domain.Position, error)
}

// Analytics computes P&L and performance aggregates.
// All methods are read-only.
// *AnalyticsService satisfies this interface.
type Analytics interface {
	GetSymbolPnL(ctx context.Context, accountID, symbol string, from, to time.Time) (decimal.Decimal, error)
	GetPnLSummary(ctx context.Context, accountID string, from, to time.Time) (*PnLSummary, error)
	GetStrategyPerformance(ctx context.Context, accountID string, from, to time.Time) ([]StrategyStats, error)
}
