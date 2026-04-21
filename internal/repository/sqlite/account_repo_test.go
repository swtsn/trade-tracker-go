package sqlite_test

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"trade-tracker-go/internal/domain"
)

func TestAccountRepository(t *testing.T) {
	ctx := context.Background()
	repos := openTestDB(t)

	t.Run("create and get by id", func(t *testing.T) {
		acc := &domain.Account{
			ID:            uuid.New().String(),
			Broker:        "tastytrade",
			AccountNumber: "ACC-001",
			Name:          "My Account",
			CreatedAt:     time.Now().UTC().Truncate(time.Second),
		}
		require.NoError(t, repos.Accounts.Create(ctx, acc))

		got, err := repos.Accounts.GetByID(ctx, acc.ID)
		require.NoError(t, err)
		assert.Equal(t, acc.ID, got.ID)
		assert.Equal(t, acc.Broker, got.Broker)
		assert.Equal(t, acc.AccountNumber, got.AccountNumber)
		assert.Equal(t, acc.Name, got.Name)
		assert.Equal(t, acc.CreatedAt, got.CreatedAt.UTC())
	})

	t.Run("get by id not found", func(t *testing.T) {
		_, err := repos.Accounts.GetByID(ctx, "nonexistent")
		assert.ErrorIs(t, err, domain.ErrNotFound)
	})

	t.Run("list", func(t *testing.T) {
		// Clear via fresh db
		r2 := openTestDB(t)
		a1 := &domain.Account{ID: uuid.New().String(), Broker: "schwab", AccountNumber: "A1", Name: "One", CreatedAt: time.Now().UTC().Truncate(time.Second)}
		a2 := &domain.Account{ID: uuid.New().String(), Broker: "tastytrade", AccountNumber: "A2", Name: "Two", CreatedAt: time.Now().UTC().Truncate(time.Second).Add(time.Second)}
		require.NoError(t, r2.Accounts.Create(ctx, a1))
		require.NoError(t, r2.Accounts.Create(ctx, a2))

		list, err := r2.Accounts.List(ctx)
		require.NoError(t, err)
		assert.Len(t, list, 2)
	})

	t.Run("duplicate id returns ErrDuplicate", func(t *testing.T) {
		r2 := openTestDB(t)
		acc := &domain.Account{ID: uuid.New().String(), Broker: "b", AccountNumber: "X", Name: "Y", CreatedAt: time.Now().UTC()}
		require.NoError(t, r2.Accounts.Create(ctx, acc))
		err := r2.Accounts.Create(ctx, acc)
		assert.ErrorIs(t, err, domain.ErrDuplicate)
	})

	t.Run("duplicate broker+account_number returns ErrDuplicate", func(t *testing.T) {
		r2 := openTestDB(t)
		a1 := &domain.Account{ID: uuid.New().String(), Broker: "tastytrade", AccountNumber: "DUP-001", Name: "First", CreatedAt: time.Now().UTC()}
		a2 := &domain.Account{ID: uuid.New().String(), Broker: "tastytrade", AccountNumber: "DUP-001", Name: "Second", CreatedAt: time.Now().UTC()}
		require.NoError(t, r2.Accounts.Create(ctx, a1))
		err := r2.Accounts.Create(ctx, a2)
		assert.ErrorIs(t, err, domain.ErrDuplicate)
	})

	t.Run("update name", func(t *testing.T) {
		r2 := openTestDB(t)
		acc := &domain.Account{ID: uuid.New().String(), Broker: "tastytrade", AccountNumber: "U1", Name: "Old", CreatedAt: time.Now().UTC().Truncate(time.Second)}
		require.NoError(t, r2.Accounts.Create(ctx, acc))

		require.NoError(t, r2.Accounts.UpdateName(ctx, acc.ID, "New"))

		got, err := r2.Accounts.GetByID(ctx, acc.ID)
		require.NoError(t, err)
		assert.Equal(t, "New", got.Name)
	})

	t.Run("update name not found", func(t *testing.T) {
		r2 := openTestDB(t)
		err := r2.Accounts.UpdateName(ctx, "nonexistent", "X")
		assert.ErrorIs(t, err, domain.ErrNotFound)
	})
}
