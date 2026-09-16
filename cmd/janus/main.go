package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"janus/internal/config"
	"janus/internal/gateway"
	appLogger "janus/pkg/logger"

	"go.uber.org/zap"
)

func main() {

	//1、parse the argument in the exe command-----------
	path := flag.String("config", "configs/janus.json", "configuration file")
	check := flag.Bool("check", false, "validate configuration and exit")
	flag.Parse()
	//2、logger init---------------------------------
	logger, cleanup, err := appLogger.New(appLogger.DefaultConfig())
	if err != nil {
		fmt.Fprintf(os.Stderr, "initialize logger: %v\n", err)
		os.Exit(1)
	}
	defer func() { _ = cleanup() }()
	//3、graceful exit preparation.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	//4、Run : the core logics
	if err := run(ctx, *path, *check, logger); err != nil {
		logger.Error("janus stopped", zap.Error(err))
		os.Exit(1)
	}
}

func run(ctx context.Context, path string, check bool, logger *zap.Logger) error {
	//open the janus json config file
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	c, err := config.Load(f)
	f.Close()
	if err != nil {
		return err
	}
	//check is used for what? TODO
	if check {
		logger.Info("configuration valid")
		return nil
	}
	// construct gateway ,gateway is constructed with routers, router contain reverse handler.
	gatewayWithinHandlers, err := gateway.New(c, logger)
	if err != nil {
		return err
	}
	defer gatewayWithinHandlers.Close()
	//Start the server
	srv := gateway.NewServer(c.Listen, gatewayWithinHandlers)
	//listen the specified network  and address.
	ln, err := net.Listen("tcp", c.Listen)
	if err != nil {
		return err
	}
	logger.Info("janus listening", zap.String("address", ln.Addr().String()))
	done := make(chan error, 1)
	//bind the server to the net listening
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
