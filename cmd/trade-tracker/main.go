package main

import (
	"context"
	"fmt"
	"log"
	"net"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/alecthomas/kong"
	"google.golang.org/grpc"

	pb "trade-tracker-go/gen/tradetracker/v1"
	grpchandler "trade-tracker-go/internal/grpc"
	"trade-tracker-go/internal/repository/sqlite"
	"trade-tracker-go/internal/service"
	"trade-tracker-go/internal/strategy"
)

var cli struct {
	DB   string `help:"Path to SQLite database file." default:"trade-tracker.db" env:"TRADE_TRACKER_DB"`
	Addr string `help:"gRPC listen address."          default:"localhost:50051"  env:"TRADE_TRACKER_ADDR"`
}

// drainTimeout is the maximum time to wait for in-flight RPCs to finish before
// forcing a stop. Prevents a stuck RPC (e.g. SQLite WAL contention) from
// blocking process exit indefinitely.
const drainTimeout = 15 * time.Second

func main() {
	// kong exits automatically on --help or parse error; return value not needed.
	kong.Parse(&cli)

	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run() error {
	// Open DB and run migrations.
	repos, err := sqlite.OpenRepos(cli.DB)
	if err != nil {
		return fmt.Errorf("open db: %w", err)
	}
	defer func() {
		if cerr := repos.Close(); cerr != nil {
			log.Printf("close db: %v", cerr)
		}
	}()

	// Wire services.
	chainSvc := service.NewChainService(repos.Chains, repos.Trades, repos.Transactions)
	positionSvc := service.NewPositionService(repos.Positions)
	importSvc := service.NewImportService(
		repos.Trades,
		repos.Transactions,
		repos.Instruments,
		strategy.NewClassifier(),
		chainSvc,
		service.PostImportHook{
			Name: "position",
			Run:  positionSvc.ProcessTrade,
		},
	)
	// AnalyticsService uses the raw *sql.DB for multi-step read queries.
	// All handlers share the single connection enforced by MaxOpenConns(1) in db.go;
	// a slow analytics query will block other handlers for its duration.
	analyticsSvc := service.NewAnalyticsService(repos.DB())

	// Limit transport-layer receive size to 2 MiB — above any handler's documented
	// limit (ImportHandler caps CSV at 1 MiB) but well below gRPC's 4 MiB default.
	srv := grpc.NewServer(grpc.MaxRecvMsgSize(2 << 20))
	pb.RegisterAccountServiceServer(srv, grpchandler.NewAccountHandler(repos.Accounts, repos.Accounts))
	pb.RegisterImportServiceServer(srv, grpchandler.NewImportHandler(importSvc))
	pb.RegisterTradeServiceServer(srv, grpchandler.NewTradeHandler(repos.Trades))
	pb.RegisterPositionServiceServer(srv, grpchandler.NewPositionHandler(positionSvc))
	pb.RegisterChainServiceServer(srv, grpchandler.NewChainHandler(chainSvc))
	pb.RegisterAnalyticsServiceServer(srv, grpchandler.NewAnalyticsHandler(analyticsSvc))

	// Register signal handler before binding the port so no signal is missed.
	stop := make(chan os.Signal, 1)
	signal.Notify(stop, syscall.SIGINT, syscall.SIGTERM)

	lis, err := net.Listen("tcp", cli.Addr)
	if err != nil {
		return fmt.Errorf("listen %s: %w", cli.Addr, err)
	}

	go func() {
		<-stop
		log.Println("shutting down")
		ctx, cancel := context.WithTimeout(context.Background(), drainTimeout)
		defer cancel()
		done := make(chan struct{})
		go func() { srv.GracefulStop(); close(done) }()
		select {
		case <-done:
		case <-ctx.Done():
			log.Println("drain timeout exceeded; forcing stop")
			srv.Stop()
		}
	}()

	log.Printf("serving on %s", cli.Addr)
	return srv.Serve(lis)
}
