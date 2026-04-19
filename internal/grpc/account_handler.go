package grpc

import (
	"context"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"
	pb "trade-tracker-go/gen/tradetracker/v1"
	"trade-tracker-go/internal/domain"
	"trade-tracker-go/internal/service"
)

// AccountHandler implements pb.AccountServiceServer.
// Auth is enforced by a server-level interceptor; see cmd/trade-tracker-server.
type AccountHandler struct {
	pb.UnimplementedAccountServiceServer
	accounts service.AccountReader
}

// NewAccountHandler creates an AccountHandler backed by the given reader.
func NewAccountHandler(accounts service.AccountReader) *AccountHandler {
	return &AccountHandler{accounts: accounts}
}

func (h *AccountHandler) ListAccounts(ctx context.Context, req *pb.ListAccountsRequest) (*pb.ListAccountsResponse, error) {
	_ = req // pagination not yet implemented; reserved fields are accepted and ignored
	accts, err := h.accounts.List(ctx)
	if err != nil {
		return nil, toGRPCError(err)
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
		return nil, toGRPCError(err)
	}
	if a == nil {
		return nil, status.Error(codes.NotFound, "account not found")
	}
	return &pb.GetAccountResponse{Account: accountToProto(a)}, nil
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
