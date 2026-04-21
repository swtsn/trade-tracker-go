package grpc

import (
	"errors"
	"log/slog"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"trade-tracker-go/internal/domain"
)

// toGRPCError maps domain errors to gRPC status errors.
// For codes.Internal, the raw error is NOT forwarded to the client to avoid
// leaking internal details (SQL text, file paths, etc.), but it is logged
// server-side so failures are diagnosable.
func toGRPCError(logger *slog.Logger, err error) error {
	if errors.Is(err, domain.ErrNotFound) {
		return status.Error(codes.NotFound, "not found")
	}
	if errors.Is(err, domain.ErrDuplicate) {
		return status.Error(codes.AlreadyExists, "already exists")
	}
	logger.Error("internal error", "err", err)
	return status.Error(codes.Internal, "internal server error")
}
