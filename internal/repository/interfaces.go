// Package repository defines the repository interface layer for accessing domain data.
package repository

import (
	"context"
	"time"

	"github.com/shopspring/decimal"
	"trade-tracker-go/internal/domain"
)

// ListTradesOptions specifies filter and pagination parameters for trade queries.
type ListTradesOptions struct {
	Limit          int
	Offset         int
	Symbol         string    // filter by underlying_symbol; empty = all
	ExecutedAfter  time.Time // filter executed_at >= value; zero = no lower bound
	ExecutedBefore time.Time // filter executed_at <= value; zero = no upper bound
}

// AccountRepository provides access to account data.
type AccountRepository interface {
	Create(ctx context.Context, account *domain.Account) error
	GetByID(ctx context.Context, id string) (*domain.Account, error)
	List(ctx context.Context) ([]domain.Account, error)
	// UpdateName sets the nickname for an existing account.
	// Returns domain.ErrNotFound if the account does not exist.
	UpdateName(ctx context.Context, id, name string) error
}

// InstrumentRepository provides access to instrument data.
type InstrumentRepository interface {
	Upsert(ctx context.Context, instrument *domain.Instrument) error
	GetByID(ctx context.Context, id string) (*domain.Instrument, error)
}

// BrokerTxKey uniquely identifies a broker transaction for deduplication purposes.
type BrokerTxKey struct {
	BrokerTxID string
	Broker     string
	AccountID  string
}

// TransactionRepository provides access to transaction data.
type TransactionRepository interface {
	Create(ctx context.Context, tx *domain.Transaction) error
	GetByID(ctx context.Context, id string) (*domain.Transaction, error)
	ListByTrade(ctx context.Context, tradeID string) ([]domain.Transaction, error)
	// ListByTradeIDs fetches transactions for multiple trades in a single query,
	// returning them grouped by trade ID. Missing trade IDs produce no entry in the map.
	ListByTradeIDs(ctx context.Context, tradeIDs []string) (map[string][]domain.Transaction, error)
	ListByAccountAndTimeRange(ctx context.Context, accountID string, from, to time.Time) ([]domain.Transaction, error)
	ExistsByBrokerTxID(ctx context.Context, brokerTxID, broker, accountID string) (bool, error)
	// FilterExistingBrokerTxIDs returns the subset of keys that already exist in the DB.
	// Callers can subtract this set from the full input to identify new transactions.
	FilterExistingBrokerTxIDs(ctx context.Context, keys []BrokerTxKey) (map[BrokerTxKey]bool, error)
}

// TradeRepository provides access to trade data.
type TradeRepository interface {
	Create(ctx context.Context, trade *domain.Trade) error
	// GetByID returns the trade with its Transactions slice populated.
	GetByID(ctx context.Context, id string) (*domain.Trade, error)
	// GetByIDAndAccount returns the trade only if it belongs to the given account.
	// Returns domain.ErrNotFound when the trade does not exist or belongs to a different account.
	GetByIDAndAccount(ctx context.Context, accountID, id string) (*domain.Trade, error)
	// ListByAccount returns trades with empty Transactions slices; use GetByID for full detail.
	ListByAccount(ctx context.Context, accountID string, opts ListTradesOptions) ([]domain.Trade, int, error)
	// ListByAccountWithTransactions is like ListByAccount but populates each trade's
	// Transactions slice using a single batch query (no N+1).
	ListByAccountWithTransactions(ctx context.Context, accountID string, opts ListTradesOptions) ([]domain.Trade, int, error)
}

// TradeReader is the read-only subset of TradeRepository used by the trade gRPC handler.
// sqlite.tradeRepo satisfies this interface via Repos.Trades.
type TradeReader interface {
	GetByIDAndAccount(ctx context.Context, accountID, id string) (*domain.Trade, error)
	ListByAccountWithTransactions(ctx context.Context, accountID string, opts ListTradesOptions) ([]domain.Trade, int, error)
}

// PositionRepository provides access to position and lot data.
type PositionRepository interface {
	// CreatePosition inserts a new position row.
	CreatePosition(ctx context.Context, position *domain.Position) error
	// UpdatePosition updates mutable fields: cost_basis, realized_pnl, strategy_type, updated_at, closed_at.
	UpdatePosition(ctx context.Context, position *domain.Position) error
	// GetPositionByTradeID finds a position by its originating_trade_id.
	// Returns domain.ErrNotFound if no position exists.
	GetPositionByTradeID(ctx context.Context, accountID, originatingTradeID string) (*domain.Position, error)
	// GetPositionByChainID finds a position (open or closed) by its chain_id.
	// Returns domain.ErrNotFound if no position exists for that chain.
	GetPositionByChainID(ctx context.Context, accountID, chainID string) (*domain.Position, error)
	// GetPositionByID returns a position by its ID.
	// Returns domain.ErrNotFound if no position exists with that ID.
	GetPositionByID(ctx context.Context, id string) (*domain.Position, error)
	// GetPositionByIDAndAccount returns a position by its ID only if it belongs to the given account.
	// Returns domain.ErrNotFound if no position exists with that ID and account combination.
	// Use this in preference to GetPositionByID when an accountID is available, to enforce
	// ownership at the SQL level rather than in the service layer.
	GetPositionByIDAndAccount(ctx context.Context, accountID, id string) (*domain.Position, error)
	// ListPositions returns positions for an account ordered by opened_at.
	// openOnly and closedOnly are mutually exclusive; both false returns all positions.
	ListPositions(ctx context.Context, accountID string, openOnly, closedOnly bool) ([]domain.Position, error)

	// CreateLot inserts a new position lot.
	CreateLot(ctx context.Context, lot *domain.PositionLot) error
	// GetLot retrieves a position lot by ID, including instrument details.
	// Returns domain.ErrNotFound if the lot does not exist.
	GetLot(ctx context.Context, id string) (*domain.PositionLot, error)
	// ListOpenLotsByInstrument returns open lots for account+instrument, FIFO ordered (oldest first).
	ListOpenLotsByInstrument(ctx context.Context, accountID, instrumentID string) ([]domain.PositionLot, error)
	// ListOpenLotsByTrade returns open lots opened by the given trade, FIFO ordered.
	ListOpenLotsByTrade(ctx context.Context, accountID, tradeID string) ([]domain.PositionLot, error)
	// ListOpenLotsByChain returns all open lots whose chain_id matches, FIFO ordered.
	// Used to determine whether a chained position spanning multiple trades is fully closed.
	ListOpenLotsByChain(ctx context.Context, accountID, chainID string) ([]domain.PositionLot, error)
	// CloseLot atomically records a lot_closings entry and updates the lot's remaining_quantity.
	// Pass a non-nil closedAt when the lot is fully closed.
	CloseLot(ctx context.Context, closing *domain.LotClosing, remaining decimal.Decimal, closedAt *time.Time) error
	// CloseAndUpdatePosition atomically records a lot closing and updates the associated position's
	// realized P&L in a single transaction, preventing divergence if the process crashes between
	// the two writes. pos must be pre-resolved by the caller. pnlDelta is added to pos.RealizedPnL;
	// if no open lots remain for pos.ChainID (or pos.OriginatingTradeID when ChainID is empty),
	// pos.ClosedAt is stamped. pos is mutated in place.
	CloseAndUpdatePosition(ctx context.Context, closing *domain.LotClosing, remaining decimal.Decimal, lotClosedAt *time.Time, pos *domain.Position, pnlDelta decimal.Decimal, updatedAt time.Time) error
	// ListLotClosings retrieves all closing events for a lot, ordered by closed_at.
	ListLotClosings(ctx context.Context, lotID string) ([]domain.LotClosing, error)
}

// ContractSpecRepository provides read access to futures contract specifications.
// Rows are seeded by migration and treated as write-once reference data.
type ContractSpecRepository interface {
	// Get returns the spec string for a futures root symbol (e.g. "/NG" → "1/10000").
	// Returns domain.ErrNotFound if the root symbol is not registered.
	Get(ctx context.Context, rootSymbol string) (string, error)
}

// ChainRepository provides access to chain and chain link data.
type ChainRepository interface {
	CreateChain(ctx context.Context, chain *domain.Chain) error
	// GetChainByID returns the chain with its Links slice populated.
	GetChainByID(ctx context.Context, id string) (*domain.Chain, error)
	// GetChainByTradeID returns the chain that owns the given trade, checking
	// chains.original_trade_id, chain_links.closing_trade_id, and
	// chain_links.opening_trade_id. Used as the idempotency gate in chain
	// detection. Returns domain.ErrNotFound when no chain owns the trade.
	GetChainByTradeID(ctx context.Context, tradeID string) (*domain.Chain, error)
	// ListChainsByAccount returns chains with empty Links slices; use GetChainByID for full detail.
	ListChainsByAccount(ctx context.Context, accountID string, openOnly bool) ([]domain.Chain, error)
	UpdateChainClosed(ctx context.Context, id string, closedAt time.Time) error

	// GetStockChainBySymbol returns the durable stock chain for (accountID, symbol).
	// Stock chains are keyed by (account, symbol) and never closed at the chain level.
	// Returns domain.ErrNotFound when no stock chain exists for the symbol yet.
	GetStockChainBySymbol(ctx context.Context, accountID, symbol string) (*domain.Chain, error)

	// GetOpenChainForInstrument returns the open chain in the account that has a net
	// positive opening balance for the given instrument (derived from transaction arithmetic).
	// Used to attribute a closing transaction to its originating chain.
	// Returns domain.ErrNotFound when no open chain holds the instrument.
	GetOpenChainForInstrument(ctx context.Context, accountID, instrumentID string) (*domain.Chain, error)

	// ChainIsOpen reports whether any instrument in the chain has a net opening
	// quantity greater than zero across all of the chain's trades (transaction arithmetic).
	ChainIsOpen(ctx context.Context, chainID string) (bool, error)

	CreateChainLink(ctx context.Context, link *domain.ChainLink) error
	ListChainLinks(ctx context.Context, chainID string) ([]domain.ChainLink, error)
	// GetChainPnL returns the net realized P&L for the chain computed from transaction data:
	// sum of (fill_price × quantity × multiplier × direction_sign − fees) across all trades.
	GetChainPnL(ctx context.Context, chainID string) (decimal.Decimal, error)
}
