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

// fakeAccountWriter is a test double for service.AccountWriter.
type fakeAccountWriter struct {
	created []domain.Account
	err     error
	names   map[string]string // id -> updated name
}

func (f *fakeAccountWriter) Create(_ context.Context, account *domain.Account) error {
	if f.err != nil {
		return f.err
	}
	f.created = append(f.created, *account)
	return nil
}

func (f *fakeAccountWriter) UpdateName(_ context.Context, id, name string) error {
	if f.err != nil {
		return f.err
	}
	if f.names == nil {
		f.names = make(map[string]string)
	}
	f.names[id] = name
	return nil
}

func newHandler(reader *fakeAccountReader) *grpchandler.AccountHandler {
	return grpchandler.NewAccountHandler(reader, &fakeAccountWriter{}, testLogger)
}

func TestListAccounts(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	fake := &fakeAccountReader{
		accounts: []domain.Account{
			{ID: "a1", Broker: "tastytrade", AccountNumber: "12345", Name: "Main", CreatedAt: now},
			{ID: "a2", Broker: "schwab", AccountNumber: "67890", Name: "IRA", CreatedAt: now},
		},
	}
	h := newHandler(fake)

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
	h := newHandler(&fakeAccountReader{})

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
	h := newHandler(fake)

	resp, err := h.GetAccount(context.Background(), &pb.GetAccountRequest{Id: "a1"})
	require.NoError(t, err)
	assert.Equal(t, "a1", resp.Account.Id)
	assert.Equal(t, "tastytrade", resp.Account.Broker)
	assert.Equal(t, "12345", resp.Account.AccountNumber)
	assert.Equal(t, "Main", resp.Account.Name)
	assert.Equal(t, now.Unix(), resp.Account.CreatedAt.AsTime().Unix())
}

func TestGetAccount_NotFound(t *testing.T) {
	h := newHandler(&fakeAccountReader{})

	_, err := h.GetAccount(context.Background(), &pb.GetAccountRequest{Id: "missing"})
	require.Error(t, err)
	assert.Equal(t, codes.NotFound, status.Code(err))
}

func TestGetAccount_MissingID(t *testing.T) {
	h := newHandler(&fakeAccountReader{})

	_, err := h.GetAccount(context.Background(), &pb.GetAccountRequest{})
	require.Error(t, err)
	assert.Equal(t, codes.InvalidArgument, status.Code(err))
}

func TestCreateAccount(t *testing.T) {
	reader := &fakeAccountReader{}
	writer := &fakeAccountWriter{}
	h := grpchandler.NewAccountHandler(reader, writer, testLogger)

	resp, err := h.CreateAccount(context.Background(), &pb.CreateAccountRequest{
		Broker:        "tastytrade",
		AccountNumber: "12345",
		Name:          "Main",
	})
	require.NoError(t, err)
	assert.NotEmpty(t, resp.Account.Id)
	assert.Equal(t, "tastytrade", resp.Account.Broker)
	assert.Equal(t, "12345", resp.Account.AccountNumber)
	assert.Equal(t, "Main", resp.Account.Name)
	assert.NotNil(t, resp.Account.CreatedAt)

	require.Len(t, writer.created, 1)
	assert.Equal(t, "tastytrade", writer.created[0].Broker)
}

func TestCreateAccount_MissingBroker(t *testing.T) {
	h := grpchandler.NewAccountHandler(&fakeAccountReader{}, &fakeAccountWriter{}, testLogger)

	_, err := h.CreateAccount(context.Background(), &pb.CreateAccountRequest{AccountNumber: "12345"})
	require.Error(t, err)
	assert.Equal(t, codes.InvalidArgument, status.Code(err))
}

func TestCreateAccount_MissingAccountNumber(t *testing.T) {
	h := grpchandler.NewAccountHandler(&fakeAccountReader{}, &fakeAccountWriter{}, testLogger)

	_, err := h.CreateAccount(context.Background(), &pb.CreateAccountRequest{Broker: "tastytrade"})
	require.Error(t, err)
	assert.Equal(t, codes.InvalidArgument, status.Code(err))
}

func TestCreateAccount_Duplicate(t *testing.T) {
	writer := &fakeAccountWriter{err: domain.ErrDuplicate}
	h := grpchandler.NewAccountHandler(&fakeAccountReader{}, writer, testLogger)

	_, err := h.CreateAccount(context.Background(), &pb.CreateAccountRequest{
		Broker: "tastytrade", AccountNumber: "12345",
	})
	require.Error(t, err)
	assert.Equal(t, codes.AlreadyExists, status.Code(err))
}

func TestUpdateAccount(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	reader := &fakeAccountReader{
		accounts: []domain.Account{
			{ID: "a1", Broker: "tastytrade", AccountNumber: "12345", Name: "Old", CreatedAt: now},
		},
	}
	writer := &fakeAccountWriter{}
	h := grpchandler.NewAccountHandler(reader, writer, testLogger)

	resp, err := h.UpdateAccount(context.Background(), &pb.UpdateAccountRequest{Id: "a1", Name: "New"})
	require.NoError(t, err)
	assert.Equal(t, "a1", resp.Account.Id)
	assert.Equal(t, "New", resp.Account.Name)
	assert.Equal(t, "New", writer.names["a1"])
}

func TestUpdateAccount_MissingID(t *testing.T) {
	h := grpchandler.NewAccountHandler(&fakeAccountReader{}, &fakeAccountWriter{}, testLogger)

	_, err := h.UpdateAccount(context.Background(), &pb.UpdateAccountRequest{})
	require.Error(t, err)
	assert.Equal(t, codes.InvalidArgument, status.Code(err))
}

func TestUpdateAccount_NotFound(t *testing.T) {
	writer := &fakeAccountWriter{err: domain.ErrNotFound}
	h := grpchandler.NewAccountHandler(&fakeAccountReader{}, writer, testLogger)

	_, err := h.UpdateAccount(context.Background(), &pb.UpdateAccountRequest{Id: "missing", Name: "X"})
	require.Error(t, err)
	assert.Equal(t, codes.NotFound, status.Code(err))
}
