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
	"sort"
	"syscall"
	"time"

	"janus/internal/admin"
	"janus/internal/config"
	"janus/internal/limen"
	janusruntime "janus/internal/runtime"
	appLogger "janus/pkg/logger"

	"go.uber.org/zap"
)

func main() {

	//1、parse the argument in the exe command-----------
	path := flag.String("config", "configs/janus.json", "configuration file")
	check := flag.Bool("check", false, "validate configuration and exit")
	reloadInterval := flag.Duration("reload-interval", time.Second, "poll interval for versioned configuration and TLS files")
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
	if err := run(ctx, *path, *check, *reloadInterval, logger); err != nil {
		logger.Error("janus stopped", zap.Error(err))
		os.Exit(1)
	}
}

func run(ctx context.Context, path string, check bool, reloadInterval time.Duration, logger *zap.Logger) error {
	// Open and validate the Janus configuration. Versioned TLS paths are
	// resolved relative to this file by LoadFile.
	c, err := config.LoadFile(path)
	if err != nil {
		return err
	}
	//check is used for what? TODO
	if check {
		for name, binding := range c.LimenBindings() {
			if _, err := limen.NewBinding(name, binding, http.NotFoundHandler(), c.Settings); err != nil {
				return err
			}
		}
		logger.Info("configuration valid")
		return nil
	}
	// Runtime owns the stable handler, active generation, and shared transport.
	requestRuntime, err := janusruntime.New(c, logger)
	if err != nil {
		return err
	}
	defer requestRuntime.Close()
	bindings := c.LimenBindings()
	names := make([]string, 0, len(bindings))
	for name := range bindings {
		names = append(names, name)
	}
	sort.Strings(names)
	servers := make([]*limen.Limen, 0, len(names))
	serversByName := make(map[string]*limen.Limen, len(names))
	listeners := make([]net.Listener, 0, len(names))
	packetListeners := make([]net.PacketConn, 0, len(names))
	var adminState *admin.State
	var adminServer *http.Server
	var adminListener net.Listener
	if address := c.Settings.Admin.Address; address != "" {
		adminState = admin.NewState()
		adminServer = &http.Server{
			Handler:           admin.NewHandlerWithMetrics(adminState, requestRuntime.Metrics(), requestRuntime.HealthSnapshot),
			ReadHeaderTimeout: c.Settings.Server.ReadHeaderTimeout.Duration(),
			WriteTimeout:      c.Settings.Server.WriteTimeout.Duration(),
			IdleTimeout:       c.Settings.Server.IdleTimeout.Duration(),
			MaxHeaderBytes:    int(c.Settings.Server.MaxHeaderBytes),
		}
		adminListener, err = net.Listen("tcp", address)
		if err != nil {
			closeListeners(listeners)
			closeServers(servers)
			return fmt.Errorf("admin listener: %w", err)
		}
		defer func() { _ = adminServer.Close() }()
	}
	for _, name := range names {
		protocolLimen, err := limen.NewBinding(name, bindings[name], requestRuntime, c.Settings)
		if err != nil {
			if adminListener != nil {
				_ = adminServer.Close()
			}
			closeListeners(listeners)
			closePacketListeners(packetListeners)
			closeServers(servers)
			return err
		}
		ln, err := protocolLimen.Listen()
		if err != nil {
			if adminListener != nil {
				_ = adminServer.Close()
			}
			closeListeners(listeners)
			closePacketListeners(packetListeners)
			closeServers(servers)
			return fmt.Errorf("limen %q: %w", name, err)
		}
		servers = append(servers, protocolLimen)
		serversByName[name] = protocolLimen
		listeners = append(listeners, ln)
		logger.Info("janus listening", zap.String("limen", name), zap.String("address", ln.Addr().String()))
		if protocolLimen.HTTP3Enabled() {
			packet, err := protocolLimen.ListenPacket()
			if err != nil {
				if adminListener != nil {
					_ = adminServer.Close()
				}
				closeListeners(listeners)
				closePacketListeners(packetListeners)
				closeServers(servers)
				return fmt.Errorf("limen %q HTTP/3: %w", name, err)
			}
			packetListeners = append(packetListeners, packet)
			logger.Info("janus listening", zap.String("limen", name), zap.String("protocol", "http3"), zap.String("address", packet.LocalAddr().String()))
		}
	}
	reloadCtx, cancelReload := context.WithCancel(ctx)
	defer cancelReload()
	if c.Version == config.CurrentConfigVersion {
		routeReloader, err := janusruntime.NewFileReloader(requestRuntime, path, reloadInterval, logger)
		if err != nil {
			closeListeners(listeners)
			closePacketListeners(packetListeners)
			closeServers(servers)
			return err
		}
		certificateReloader, err := limen.NewCertificateReloader(bindings, serversByName, reloadInterval, logger)
		if err != nil {
			closeListeners(listeners)
			closePacketListeners(packetListeners)
			closeServers(servers)
			return err
		}
		go func() { _ = routeReloader.Run(reloadCtx) }()
		go func() { _ = certificateReloader.Run(reloadCtx) }()
	}
	done := make(chan error, len(servers)+1)
	for i, server := range servers {
		go func(i int, server *limen.Limen) { done <- server.Serve(listeners[i]) }(i, server)
	}
	if adminServer != nil {
		go func() { done <- adminServer.Serve(adminListener) }()
	}
	if adminState != nil {
		adminState.SetReady(true)
	}
	select {
	case err := <-done:
		cancelReload()
		if adminState != nil {
			adminState.SetReady(false)
		}
		if adminServer != nil {
			_ = adminServer.Close()
		}
		closeServers(servers)
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	case <-ctx.Done():
		logger.Info("draining requests")
		drainStarted := time.Now()
		defer func() { requestRuntime.Metrics().RecordDrainDuration(time.Since(drainStarted)) }()
		if adminState != nil {
			adminState.SetReady(false)
		}
		requestRuntime.StopAccepting()
		// Do not derive this from the already-cancelled signal context.
		drain, cancel := context.WithTimeout(context.Background(), c.Settings.Shutdown.DrainTimeout.Duration())
		defer cancel()
		if delay := c.Settings.Shutdown.LoadBalancerRemovalDelay.Duration(); delay > 0 {
			logger.Info("waiting for load balancer removal", zap.Duration("delay", delay))
			timer := time.NewTimer(delay)
			select {
			case <-timer.C:
			case <-drain.Done():
				if !timer.Stop() {
					select {
					case <-timer.C:
					default:
					}
				}
			}
		}
		drainErrors := make(chan error, len(servers))
		for _, server := range servers {
			go func(server *limen.Limen) { drainErrors <- server.Shutdown(drain) }(server)
		}
		if adminServer != nil {
			go func() { drainErrors <- adminServer.Shutdown(drain) }()
		}
		for range servers {
			if err := <-drainErrors; err != nil {
				if adminServer != nil {
					_ = adminServer.Close()
				}
				closeServers(servers)
				return fmt.Errorf("drain: %w", err)
			}
		}
		if adminServer != nil {
			if err := <-drainErrors; err != nil {
				closeServers(servers)
				return fmt.Errorf("admin drain: %w", err)
			}
		}
		return nil
	}
}

func closeListeners(listeners []net.Listener) {
	for _, listener := range listeners {
		_ = listener.Close()
	}
}

func closePacketListeners(listeners []net.PacketConn) {
	for _, listener := range listeners {
		_ = listener.Close()
	}
}

func closeServers(servers []*limen.Limen) {
	for _, server := range servers {
		_ = server.Close()
	}
}
