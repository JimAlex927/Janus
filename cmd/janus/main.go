package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"janus/internal/config"
	"janus/internal/gateway"
)

func main() {

	//----------1、parse the argument in the exe command-----------
	path := flag.String("config", "configs/janus.json", "configuration file")
	check := flag.Bool("check", false, "validate configuration and exit")
	flag.Parse()
	//--------------------------------------------------------------
	logger := slog.New(slog.NewJSONHandler(os.Stderr, nil))
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := run(ctx, *path, *check, logger); err != nil {
		logger.Error("janus stopped", "error", err)
		os.Exit(1)
	}
}

func run(ctx context.Context, path string, check bool, logger *slog.Logger) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	c, err := config.Load(f)
	f.Close()
	if err != nil {
		return err
	}
	if check {
		logger.Info("configuration valid")
		return nil
	}
	g, err := gateway.New(c, logger)
	if err != nil {
		return err
	}
	defer g.Close()
	srv := gateway.NewServer(c.Listen, g)
	ln, err := net.Listen("tcp", c.Listen)
	if err != nil {
		return err
	}
	logger.Info("janus listening", "address", ln.Addr().String())
	done := make(chan error, 1)
	go func() { done <- srv.Serve(ln) }()
	select {
	case err := <-done:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	case <-ctx.Done():
		logger.Info("draining requests")
		// Do not derive this from the already-cancelled signal context.
		drain, cancel := context.WithTimeout(context.Background(), 35*time.Second)
		defer cancel()
		if err := srv.Shutdown(drain); err != nil {
			srv.Close()
			return fmt.Errorf("drain: %w", err)
		}
		return nil
	}
}
