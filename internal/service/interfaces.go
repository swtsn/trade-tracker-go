package service

import (
	"context"
	"time"

	"github.com/shopspring/decimal"
	"trade-tracker-go/internal/domain"
	"trade-tracker-go/internal/strategy"
)

// AccountReader queries account data.
// Used by the account gRPC handler.
// *sqlite.AccountRepo satisfies this interface via repos.Accounts.
type AccountReader interface {
	GetByID(ctx context.Context, id string) (*domain.Account, error)
	List(ctx context.Context) ([]domain.Account, error)
}

// AccountWriter mutates account data.
// Used by the account gRPC handler for create and update operations.
// *sqlite.accountRepo satisfies this interface via repos.Accounts.
type AccountWriter interface {
	Create(ctx context.Context, account *domain.Account) error
	UpdateName(ctx context.Context, id, name string) error
}

// StrategyClassifier classifies a set of transaction legs into a strategy type.
// *strategy.Classifier satisfies this interface.
type StrategyClassifier interface {
	Classify(legs []strategy.LegShape) domain.StrategyType
}

// TradeChainer creates or extends a chain for a trade and returns the chain ID and
// the strategy type burned in at chain creation.
// *ChainService satisfies this interface.
type TradeChainer interface {
	ProcessTrade(ctx context.Context, tradeID string) (string, domain.StrategyType, error)
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
	ProcessTrade(ctx context.Context, tradeID string, txns []domain.Transaction, chainID string, strategyType domain.StrategyType) error
}

// PositionReader queries position data.
// Used by the position gRPC handler; never called from the import path.
// *PositionService satisfies this interface.
type PositionReader interface {
	GetPosition(ctx context.Context, accountID, positionID string) (*domain.Position, error)
	// ListPositions returns positions for an account ordered by opened_at.
	// openOnly and closedOnly are mutually exclusive; both false returns all positions.
	ListPositions(ctx context.Context, accountID string, openOnly, closedOnly bool) ([]domain.Position, error)
}

// ChainReader queries chain data for the gRPC handler.
// *ChainService satisfies this interface.
type ChainReader interface {
	GetChainDetail(ctx context.Context, accountID, chainID string) (*domain.ChainDetail, error)
}

// Analytics computes P&L and performance aggregates.
// All methods are read-only.
// *AnalyticsService satisfies this interface.
type Analytics interface {
	GetSymbolPnL(ctx context.Context, accountID, symbol string, from, to time.Time) (decimal.Decimal, error)
	GetPnLSummary(ctx context.Context, accountID string, from, to time.Time) (*PnLSummary, error)
	GetStrategyPerformance(ctx context.Context, accountID string, from, to time.Time) ([]StrategyStats, error)
}
