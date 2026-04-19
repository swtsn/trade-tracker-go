package grpc_test

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	pb "trade-tracker-go/gen/tradetracker/v1"
	"trade-tracker-go/internal/domain"
	grpchandler "trade-tracker-go/internal/grpc"
)

// fakeAccountReader is a test double for service.AccountReader.
type fakeAccountReader struct {
	accounts []domain.Account
	err      error
}

func (f *fakeAccountReader) List(_ context.Context) ([]domain.Account, error) {
	return f.accounts, f.err
}

func (f *fakeAccountReader) GetByID(_ context.Context, id string) (*domain.Account, error) {
	if f.err != nil {
		return nil, f.err
	}
	for _, a := range f.accounts {
		if a.ID == id {
			return &a, nil
		}
	}
	return nil, domain.ErrNotFound
}

func TestListAccounts(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	fake := &fakeAccountReader{
		accounts: []domain.Account{
			{ID: "a1", Broker: "tastytrade", AccountNumber: "12345", Name: "Main", CreatedAt: now},
			{ID: "a2", Broker: "schwab", AccountNumber: "67890", Name: "IRA", CreatedAt: now},
		},
	}
	h := grpchandler.NewAccountHandler(fake)

	resp, err := h.ListAccounts(context.Background(), &pb.ListAccountsRequest{})
	require.NoError(t, err)
	require.Len(t, resp.Accounts, 2)

	assert.Equal(t, "a1", resp.Accounts[0].Id)
	assert.Equal(t, "tastytrade", resp.Accounts[0].Broker)
	assert.Equal(t, "12345", resp.Accounts[0].AccountNumber)
	assert.Equal(t, "Main", resp.Accounts[0].Name)
	assert.Equal(t, now.Unix(), resp.Accounts[0].CreatedAt.AsTime().Unix())

	assert.Equal(t, "a2", resp.Accounts[1].Id)
}

func TestListAccounts_Empty(t *testing.T) {
	h := grpchandler.NewAccountHandler(&fakeAccountReader{})

	resp, err := h.ListAccounts(context.Background(), &pb.ListAccountsRequest{})
	require.NoError(t, err)
	assert.NotNil(t, resp.Accounts)
	assert.Empty(t, resp.Accounts)
}

func TestGetAccount_Found(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	fake := &fakeAccountReader{
		accounts: []domain.Account{
			{ID: "a1", Broker: "tastytrade", AccountNumber: "12345", Name: "Main", CreatedAt: now},
		},
	}
	h := grpchandler.NewAccountHandler(fake)

	resp, err := h.GetAccount(context.Background(), &pb.GetAccountRequest{Id: "a1"})
	require.NoError(t, err)
	assert.Equal(t, "a1", resp.Account.Id)
	assert.Equal(t, "tastytrade", resp.Account.Broker)
	assert.Equal(t, "12345", resp.Account.AccountNumber)
	assert.Equal(t, "Main", resp.Account.Name)
	assert.Equal(t, now.Unix(), resp.Account.CreatedAt.AsTime().Unix())
}

func TestGetAccount_NotFound(t *testing.T) {
	h := grpchandler.NewAccountHandler(&fakeAccountReader{})

	_, err := h.GetAccount(context.Background(), &pb.GetAccountRequest{Id: "missing"})
	require.Error(t, err)
	assert.Equal(t, codes.NotFound, status.Code(err))
}

func TestGetAccount_MissingID(t *testing.T) {
	h := grpchandler.NewAccountHandler(&fakeAccountReader{})

	_, err := h.GetAccount(context.Background(), &pb.GetAccountRequest{})
	require.Error(t, err)
	assert.Equal(t, codes.InvalidArgument, status.Code(err))
}
