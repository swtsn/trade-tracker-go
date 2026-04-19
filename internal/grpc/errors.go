package grpc

import (
	"errors"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"trade-tracker-go/internal/domain"
)

// toGRPCError maps domain errors to gRPC status errors.
// For codes.Internal, the raw error is NOT forwarded to the client to avoid
// leaking internal details (SQL text, file paths, etc.). TODO: log the original
// error server-side once a logger is wired into the handler layer.
func toGRPCError(err error) error {
	if errors.Is(err, domain.ErrNotFound) {
		return status.Error(codes.NotFound, err.Error())
	}
	if errors.Is(err, domain.ErrDuplicate) {
		return status.Error(codes.AlreadyExists, err.Error())
	}
	return status.Error(codes.Internal, "internal server error")
}
