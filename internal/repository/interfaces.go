package repository

import (
	"context"
	"time"

	"github.com/shopspring/decimal"
	"trade-tracker-go/internal/domain"
)

type ListTradesOptions struct {
	Limit      int
	Offset     int
	OpenOnly   bool
	ClosedOnly bool
}

type AccountRepository interface {
	Create(ctx context.Context, account *domain.Account) error
	GetByID(ctx context.Context, id string) (*domain.Account, error)
	List(ctx context.Context) ([]domain.Account, error)
}

type InstrumentRepository interface {
	Upsert(ctx context.Context, instrument *domain.Instrument) error
	GetByID(ctx context.Context, id string) (*domain.Instrument, error)
}

type TransactionRepository interface {
	Create(ctx context.Context, tx *domain.Transaction) error
	GetByID(ctx context.Context, id string) (*domain.Transaction, error)
	ListByTrade(ctx context.Context, tradeID string) ([]domain.Transaction, error)
	ListByAccountAndTimeRange(ctx context.Context, accountID string, from, to time.Time) ([]domain.Transaction, error)
	ExistsByBrokerTxID(ctx context.Context, brokerTxID, broker, accountID string) (bool, error)
}

type TradeRepository interface {
	Create(ctx context.Context, trade *domain.Trade) error
	// GetByID returns the trade with its Transactions slice populated.
	GetByID(ctx context.Context, id string) (*domain.Trade, error)
	// ListByAccount returns trades with empty Transactions slices; use GetByID for full detail.
	// OpenOnly and ClosedOnly in opts are mutually exclusive; both true returns an error.
	ListByAccount(ctx context.Context, accountID string, opts ListTradesOptions) ([]domain.Trade, int, error)
	UpdateStrategy(ctx context.Context, id string, strategy domain.StrategyType) error
	UpdateClosedAt(ctx context.Context, id string, closedAt time.Time) error
}

type PositionRepository interface {
	UpsertPosition(ctx context.Context, position *domain.Position) error
	GetPosition(ctx context.Context, accountID, instrumentID string) (*domain.Position, error)
	ListOpenPositions(ctx context.Context, accountID string) ([]domain.Position, error)

	CreateLot(ctx context.Context, lot *domain.PositionLot) error
	GetLot(ctx context.Context, id string) (*domain.PositionLot, error)
	ListOpenLotsByInstrument(ctx context.Context, accountID, instrumentID string) ([]domain.PositionLot, error)
	// CloseLot atomically records a lot_closings entry and updates the lot's remaining_quantity.
	// Pass a non-nil closedAt when the lot is fully closed.
	CloseLot(ctx context.Context, closing *domain.LotClosing, remaining decimal.Decimal, closedAt *time.Time) error
	ListLotClosings(ctx context.Context, lotID string) ([]domain.LotClosing, error)
}

type ChainRepository interface {
	CreateChain(ctx context.Context, chain *domain.Chain) error
	// GetChainByID returns the chain with its Links slice populated.
	GetChainByID(ctx context.Context, id string) (*domain.Chain, error)
	// ListChainsByAccount returns chains with empty Links slices; use GetChainByID for full detail.
	ListChainsByAccount(ctx context.Context, accountID string, openOnly bool) ([]domain.Chain, error)
	UpdateChainClosed(ctx context.Context, id string, closedAt time.Time) error

	CreateChainLink(ctx context.Context, link *domain.ChainLink) error
	ListChainLinks(ctx context.Context, chainID string) ([]domain.ChainLink, error)
}
