package sqlite

import (
	"database/sql"
	"fmt"

	"trade-tracker-go/internal/repository"
)

// Repos bundles all repository implementations backed by a single SQLite database.
type Repos struct {
	Accounts      repository.AccountRepository
	Instruments   repository.InstrumentRepository
	Transactions  repository.TransactionRepository
	Trades        repository.TradeRepository
	Positions     repository.PositionRepository
	Chains        repository.ChainRepository
	ContractSpecs repository.ContractSpecRepository

	db *sql.DB
}

// OpenRepos opens a SQLite database and returns all repositories.
func OpenRepos(path string) (*Repos, error) {
	db, err := Open(path)
	if err != nil {
		return nil, fmt.Errorf("open repos: %w", err)
	}
	return &Repos{
		Accounts:      NewAccountRepository(db),
		Instruments:   NewInstrumentRepository(db),
		Transactions:  NewTransactionRepository(db),
		Trades:        NewTradeRepository(db),
		Positions:     NewPositionRepository(db),
		Chains:        NewChainRepository(db),
		ContractSpecs: NewContractSpecRepository(db),
		db:            db,
	}, nil
}

// Close closes the underlying database connection.
func (r *Repos) Close() error {
	return r.db.Close()
}
