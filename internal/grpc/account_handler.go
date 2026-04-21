package grpc

import (
	"context"
	"log/slog"
	"time"

	"github.com/google/uuid"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"
	pb "trade-tracker-go/gen/tradetracker/v1"
	"trade-tracker-go/internal/domain"
	"trade-tracker-go/internal/service"
)

// AccountHandler implements pb.AccountServiceServer.
// Auth is enforced by a server-level interceptor; see cmd/trade-tracker.
type AccountHandler struct {
	pb.UnimplementedAccountServiceServer
	accounts service.AccountReader
	writer   service.AccountWriter
	logger   *slog.Logger
}

// NewAccountHandler creates an AccountHandler backed by the given reader and writer.
func NewAccountHandler(accounts service.AccountReader, writer service.AccountWriter, logger *slog.Logger) *AccountHandler {
	return &AccountHandler{accounts: accounts, writer: writer, logger: logger}
}

func (h *AccountHandler) ListAccounts(ctx context.Context, req *pb.ListAccountsRequest) (*pb.ListAccountsResponse, error) {
	_ = req // pagination not yet implemented; reserved fields are accepted and ignored
	accts, err := h.accounts.List(ctx)
	if err != nil {
		return nil, toGRPCError(h.logger, err)
	}
	resp := &pb.ListAccountsResponse{
		Accounts: make([]*pb.Account, len(accts)),
	}
	for i, a := range accts {
		resp.Accounts[i] = accountToProto(&a)
	}
	return resp, nil
}

func (h *AccountHandler) GetAccount(ctx context.Context, req *pb.GetAccountRequest) (*pb.GetAccountResponse, error) {
	if req.Id == "" {
		return nil, status.Error(codes.InvalidArgument, "id is required")
	}
	a, err := h.accounts.GetByID(ctx, req.Id)
	if err != nil {
		return nil, toGRPCError(h.logger, err)
	}
	if a == nil {
		return nil, status.Error(codes.NotFound, "account not found")
	}
	return &pb.GetAccountResponse{Account: accountToProto(a)}, nil
}

func (h *AccountHandler) CreateAccount(ctx context.Context, req *pb.CreateAccountRequest) (*pb.CreateAccountResponse, error) {
	if req.Broker == "" {
		return nil, status.Error(codes.InvalidArgument, "broker is required")
	}
	if req.AccountNumber == "" {
		return nil, status.Error(codes.InvalidArgument, "account_number is required")
	}
	id, err := uuid.NewV7()
	if err != nil {
		return nil, status.Error(codes.Internal, "generate id")
	}
	account := &domain.Account{
		ID:            id.String(),
		Broker:        req.Broker,
		AccountNumber: req.AccountNumber,
		Name:          req.Name,
		CreatedAt:     time.Now().UTC(),
	}
	if err := h.writer.Create(ctx, account); err != nil {
		return nil, toGRPCError(h.logger, err)
	}
	return &pb.CreateAccountResponse{Account: accountToProto(account)}, nil
}

func (h *AccountHandler) UpdateAccount(ctx context.Context, req *pb.UpdateAccountRequest) (*pb.UpdateAccountResponse, error) {
	if req.Id == "" {
		return nil, status.Error(codes.InvalidArgument, "id is required")
	}
	// Fetch before writing so the response is always consistent with what we wrote,
	// regardless of concurrent renames from other clients.
	a, err := h.accounts.GetByID(ctx, req.Id)
	if err != nil {
		return nil, toGRPCError(h.logger, err)
	}
	if err := h.writer.UpdateName(ctx, req.Id, req.Name); err != nil {
		return nil, toGRPCError(h.logger, err)
	}
	a.Name = req.Name
	return &pb.UpdateAccountResponse{Account: accountToProto(a)}, nil
}

func accountToProto(a *domain.Account) *pb.Account {
	return &pb.Account{
		Id:            a.ID,
		Broker:        a.Broker,
		AccountNumber: a.AccountNumber,
		Name:          a.Name,
		CreatedAt:     timestamppb.New(a.CreatedAt),
	}
}
