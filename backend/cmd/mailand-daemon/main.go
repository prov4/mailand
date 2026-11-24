// Command mailand-daemon is the headless mailand backend.
package main

import (
	"context"
	"flag"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"

	"mailand/backend/internal/config"
	"mailand/backend/internal/rpc"
)

const shutdownTimeout = 5 * time.Second

func main() {
	if handled, code := runSubcommand(os.Args[1:]); handled {
		os.Exit(code)
	}
	socket := flag.String("socket", config.SocketPath(), "Unix socket path to listen on")
	flag.Parse()
	log := slog.New(slog.NewTextHandler(os.Stderr, nil))

	if err := run(log, *socket); err != nil {
		log.Error("fatal", "err", err)
		os.Exit(1)
	}
}

func run(log *slog.Logger, socket string) error {
	lis, err := rpc.ListenUnix(socket)
	if err != nil {
		return err
	}
	defer os.Remove(socket)

	srv := rpc.NewServer()
	g := srv.Register()

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	errc := make(chan error, 1)
	go func() { errc <- g.Serve(lis) }()
	log.Info("listening", "socket", socket)

	select {
	case err := <-errc:
		return err
	case <-ctx.Done():
	}

	log.Info("shutting down")
	srv.Close() // end event streams so GracefulStop can finish
	done := make(chan struct{})
	go func() { g.GracefulStop(); close(done) }()
	select {
	case <-done:
	case <-time.After(shutdownTimeout):
		log.Warn("graceful stop timed out, forcing")
		g.Stop()
	}
	return nil
}
