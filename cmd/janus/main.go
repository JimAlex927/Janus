package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"runtime"
	"sort"
	"sync"
	"syscall"
	"time"

	"janus/internal/admin"
	"janus/internal/config"
	"janus/internal/limen"
	janusruntime "janus/internal/runtime"
	"janus/internal/store"
	appLogger "janus/pkg/logger"

	"go.uber.org/zap"
)

func main() {
	defaultConfigFie := "configs/janus-admin.example.json"
	switch runtime.GOOS {
	case "windows":
		defaultConfigFie = "configs/janus-admin.example-windows.json"
		fmt.Println("Windows")
	case "linux":
		fmt.Println("Linux")
	case "darwin":
		fmt.Println("macOS")
	default:
		fmt.Println("Other OS:", runtime.GOOS)
	}
	//1、parse the argument in the exe command-----------
	path := flag.String("config", defaultConfigFie, "configuration file")
	check := flag.Bool("check", false, "validate configuration and exit")
	printEffective := flag.Bool("print-effective-config", false, "print normalized configuration without TLS asset paths and exit")
	initTLS := flag.Bool("init-tls", false, "create a private CA and CA-signed server certificate for a TLS limen, then exit")
	tlsLimen := flag.String("tls-limen", "", "TLS limen to initialize (optional when only one is configured)")
	tlsHosts := flag.String("tls-hosts", "", "comma-separated certificate DNS names and IPs (defaults to the limen address)")
	reloadInterval := flag.Duration("reload-interval", time.Second, "poll interval for versioned configuration and TLS files")
	flag.Parse()
	if *initTLS {
		if *check || *printEffective {
			fmt.Fprintln(os.Stderr, "-init-tls cannot be combined with -check or -print-effective-config")
			os.Exit(2)
		}
		result, err := initializeTLS(*path, *tlsLimen, *tlsHosts)
		if err != nil {
			fmt.Fprintf(os.Stderr, "initialize TLS: %v\n", err)
			os.Exit(1)
		}
		fmt.Printf("Created CA %s and server certificate %s for limen %q.\nCA SHA-256: %s\nKeep %s and %s private; distribute only the CA certificate to clients.\n", result.CACertFile, result.ServerCertFile, result.Limen, result.CAFingerprint, result.CAKeyFile, result.ServerKeyFile)
		return
	}
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
	//4、Run : the core logics   核心地方
	if err := run(ctx, *path, *check, *printEffective, *reloadInterval, logger); err != nil {
		logger.Error("janus stopped", zap.Error(err))
		os.Exit(1)
	}
}

func run(ctx context.Context, path string, check, printEffective bool, reloadInterval time.Duration, logger *zap.Logger) error {
	// Open and validate the Janus configuration. Versioned TLS paths are
	// resolved relative to this file by LoadFile.
	//加载配置 返回反序列化对象  配置文件的hash
	c, startupHash, err := config.LoadFileSnapshot(path)
	if err != nil {
		return err
	}
	// Check mode constructs each Limen without opening listeners. This validates
	// protocol compatibility, TLS assets, and HTTP/3 fallback settings before
	// the process starts accepting traffic.
	if check || printEffective {
		for name, binding := range c.LimenBindings() {
			if _, err := limen.NewBinding(name, binding, http.NotFoundHandler(), c.Settings); err != nil {
				return err
			}
		}
		if printEffective {
			encoder := json.NewEncoder(os.Stdout)
			encoder.SetIndent("", "  ")
			if err := encoder.Encode(c.EffectiveView()); err != nil {
				return fmt.Errorf("print effective configuration: %w", err)
			}
			return nil
		}
		logger.Info("configuration valid")
		return nil
	}
	// Runtime owns the stable handler, active generation, and shared transport.
	//  关键地方： 开启运行时
	requestRuntime, err := janusruntime.New(c, logger)
	if err != nil {
		return err
	}
	defer requestRuntime.Close()
	// Each Limen owns one inbound address and may expose TCP HTTP/1/HTTP/2 plus
	// an optional UDP HTTP/3 socket, while all of them share the runtime handler.
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

	//=======================For Administration============================
	var adminState *admin.State
	var adminServer *http.Server
	var adminListener net.Listener
	var configLibrary *store.Store
	if address := c.Settings.Admin.Address; address != "" {
		configLibrary = openConfigLibrary(filepath.Join(filepath.Dir(path), "janus-configs.db"), path, c, logger)
		adminState = admin.NewState()
		adminServer = &http.Server{
			Handler: admin.NewHandlerWithOptions(admin.Options{
				State: adminState, Metrics: requestRuntime.Metrics(), Health: requestRuntime.HealthSnapshot,
				Current: requestRuntime.ConfigSnapshot, Revision: requestRuntime.Revision,
				Discovery:      requestRuntime.DiscoverySnapshot,
				RegistryHealth: requestRuntime.RegistryHealthConfig,
				Publish: func(candidate config.Config, revision uint64) error {
					return publishConfig(path, requestRuntime, candidate, revision)
				},
				PublishStored: func(running, onDisk config.Config, revision uint64) error {
					return publishStoredConfig(path, requestRuntime, running, onDisk, revision)
				},
				Subscribe:  requestRuntime.Subscribe,
				Library:    configLibrary,
				SaveActive: func(updated config.Config) error { return writeConfigAtomically(path, updated) },
			}),
			ReadHeaderTimeout: c.Settings.Server.ReadHeaderTimeout.Duration(),
			WriteTimeout:      c.Settings.Server.WriteTimeout.Duration(),
			IdleTimeout:       c.Settings.Server.IdleTimeout.Duration(),
			MaxHeaderBytes:    int(c.Settings.Server.MaxHeaderBytes),
		}
		//只是绑定端口
		adminListener, err = net.Listen("tcp", address)
		if err != nil {
			closeListeners(listeners)
			closeServers(servers)
			return fmt.Errorf("admin listener: %w", err)
		}
		defer func() { _ = adminServer.Close() }()
		if configLibrary != nil {
			defer func() { _ = configLibrary.Close() }()
		}
	}
	//=============================For various Limen protocol bindings ===================
	for _, name := range names {
		/*
			关键地方 创建一个Limen对象 用的还是全局process的 runtime。 里面有http1和2 的server  以及http3的server  还有tls的server
		*/
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
		//用了net/http的监听端口 说明给http1/2 用的
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
			//如果http3被启用了 要单独开启http3的服务器  packetListeners是给h3的监听端口
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
	var reloadWG sync.WaitGroup
	defer func() {
		cancelReload()
		reloadWG.Wait()
	}()
	if c.Version == config.CurrentConfigVersion {
		//构建文件重载器
		routeReloader, err := janusruntime.NewFileReloader(requestRuntime, path, reloadInterval, logger, startupHash)
		if err != nil {
			closeListeners(listeners)
			closePacketListeners(packetListeners)
			closeServers(servers)
			return err
		}
		//tls的证书重载器
		certificateReloader, err := limen.NewCertificateReloader(bindings, serversByName, reloadInterval, logger)
		if err != nil {
			closeListeners(listeners)
			closePacketListeners(packetListeners)
			closeServers(servers)
			return err
		}
		//启动两个重载器 配置文件重载 和tls证书重载
		reloadWG.Add(2)
		go func() {
			defer reloadWG.Done()
			_ = routeReloader.Run(reloadCtx)
		}()
		go func() {
			defer reloadWG.Done()
			_ = certificateReloader.Run(reloadCtx)
		}()
	}
	done := make(chan error, len(servers)+1)
	for i, server := range servers {
		//这里是启动limen里面http的服务器那一块
		go func(i int, server *limen.Limen) { done <- server.Serve(listeners[i]) }(i, server)
	}
	//开启管理员服务
	if adminServer != nil {
		go func() { done <- adminServer.Serve(adminListener) }()
	}
	if adminState != nil {
		adminState.SetReady(true)
	}
	//Graceful shutdown！
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

// openConfigLibrary opens the SQLite named-configuration library and seeds
// it with the startup snapshot when empty, so the console always shows the
// running configuration as one entry. When the library already holds history
// but no record matches the startup file (for example the process last ran
// with a different configuration), the startup snapshot is created and
// published so the active marker reflects what the gateway actually runs
// instead of a stale leftover. A failure only disables the library
// endpoints; the gateway itself keeps serving.
func openConfigLibrary(dbPath, configPath string, startup config.Config, logger *zap.Logger) *store.Store {
	library, err := store.Open(dbPath)
	if err != nil {
		logger.Warn("configuration library unavailable", zap.Error(err))
		return nil
	}
	// The runtime snapshot resolves relative TLS paths for file access. Store
	// the paths as they should appear in the startup JSON instead, otherwise a
	// config loaded from "configs/janus.json" can turn "../.local/..." into
	// ".local/..." in the console and write it back relative to the wrong dir.
	startup = configPathsForStartupFile(startup, configPath)
	records, err := library.List()
	if err != nil {
		logger.Warn("configuration library unavailable", zap.Error(err))
		_ = library.Close()
		return nil
	}
	if len(records) == 0 {
		record, err := library.Create("线上生效配置", startup)
		if err != nil {
			logger.Warn("configuration library seed failed", zap.Error(err))
		} else if err := library.Publish(record.ID); err != nil {
			logger.Warn("configuration library seed failed", zap.Error(err))
		}
	} else {
		matched, err := library.ReconcileActive(startup)
		if err != nil {
			logger.Warn("configuration library reconciliation failed", zap.Error(err))
		} else if !matched {
			logger.Warn("configuration library active record does not match startup file; seeding startup snapshot")
			record, err := library.Create("启动配置", startup)
			if err != nil {
				logger.Warn("configuration library startup seed failed", zap.Error(err))
			} else if err := library.Publish(record.ID); err != nil {
				logger.Warn("configuration library startup publish failed", zap.Error(err))
			}
		}
	}
	return library
}

func configPathsForStartupFile(c config.Config, configPath string) config.Config {
	configAbs, err := filepath.Abs(configPath)
	if err != nil {
		return c
	}
	configDir := filepath.Dir(configAbs)
	limens := make(map[string]config.LimenConfig, len(c.Limens))
	for name, binding := range c.Limens {
		if binding.TLS != nil {
			tls := *binding.TLS
			for _, path := range []*string{&tls.CertFile, &tls.KeyFile, &tls.ClientCAFile} {
				if *path == "" || filepath.IsAbs(*path) {
					continue
				}
				assetAbs, err := filepath.Abs(*path)
				if err != nil {
					continue
				}
				relative, err := filepath.Rel(configDir, assetAbs)
				if err == nil {
					*path = filepath.ToSlash(relative)
				}
			}
			binding.TLS = &tls
		}
		limens[name] = binding
	}
	c.Limens = limens
	return c
}

// publishConfig keeps the active file and Runtime generation aligned. Runtime
// validates and builds the candidate, writes the file, and only then activates
// the generation while holding its replacement boundary. A write failure never
// exposes the candidate to requests and cannot roll back a later publish.
func publishConfig(path string, r *janusruntime.Runtime, candidate config.Config, expectedRevision uint64) error {
	previous := r.ConfigSnapshot()
	if candidate.Settings.Admin.PasswordHash == "" {
		candidate.Settings.Admin.PasswordHash = previous.Settings.Admin.PasswordHash
	}
	return r.ReplaceAndPersist(candidate, expectedRevision, func(next config.Config) error {
		if err := writeConfigAtomically(path, next); err != nil {
			return fmt.Errorf("persist configuration: %w", err)
		}
		return nil
	})
}

func publishStoredConfig(path string, r *janusruntime.Runtime, running, onDisk config.Config, expectedRevision uint64) error {
	if onDisk.Settings.Admin.PasswordHash == "" {
		onDisk.Settings.Admin.PasswordHash = r.ConfigSnapshot().Settings.Admin.PasswordHash
	}
	// Validate TLS assets exactly as the next process will resolve them. A
	// syntactically valid mTLS path must not turn a successful publication into
	// a gateway that cannot start after its required restart.
	encoded, err := json.Marshal(onDisk)
	if err != nil {
		return fmt.Errorf("encode next-start configuration: %w", err)
	}
	checked, err := config.LoadFileBytes(path, encoded)
	if err != nil {
		return fmt.Errorf("validate next-start configuration: %w", err)
	}
	for name, binding := range checked.LimenBindings() {
		if _, err := limen.NewBinding(name, binding, http.NotFoundHandler(), checked.Settings); err != nil {
			return fmt.Errorf("validate next-start limen %q: %w", name, err)
		}
	}
	return r.ReplaceAndPersistAs(running, onDisk, expectedRevision, func(next config.Config) error {
		if err := writeConfigAtomically(path, next); err != nil {
			return fmt.Errorf("persist configuration: %w", err)
		}
		return nil
	})
}

func writeConfigAtomically(path string, c config.Config) error {
	directory := filepath.Dir(path)
	temp, err := os.CreateTemp(directory, ".janus-config-*")
	if err != nil {
		return err
	}
	tempName := temp.Name()
	defer func() { _ = os.Remove(tempName) }()
	// Preserve the existing mode before writing and syncing. Applying chmod after
	// fsync would leave a crash window where the replacement content is durable
	// but its access policy is not.
	if info, statErr := os.Stat(path); statErr == nil {
		if err := temp.Chmod(info.Mode().Perm()); err != nil {
			_ = temp.Close()
			return fmt.Errorf("preserve configuration mode: %w", err)
		}
	} else if !errors.Is(statErr, os.ErrNotExist) {
		_ = temp.Close()
		return fmt.Errorf("stat existing configuration: %w", statErr)
	}
	encoder := json.NewEncoder(temp)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(c); err != nil {
		_ = temp.Close()
		return err
	}
	if err := temp.Sync(); err != nil {
		_ = temp.Close()
		return err
	}
	if err := temp.Close(); err != nil {
		return err
	}
	if err := os.Rename(tempName, path); err != nil {
		return err
	}
	// The file's contents are durable after temp.Sync, but on Unix the rename
	// itself is only durable once the containing directory metadata is flushed
	// too. Windows does not support syncing a directory handle: File.Sync on
	// such a handle returns ERROR_ACCESS_DENIED even though the rename succeeded.
	// The file sync above is the strongest portable durability step available on
	// Windows, so do not turn a successful configuration replacement into a
	// persistence error there.
	if runtime.GOOS == "windows" {
		return nil
	}
	directoryFile, err := os.Open(directory)
	if err != nil {
		return fmt.Errorf("open configuration directory for sync: %w", err)
	}
	if err := directoryFile.Sync(); err != nil {
		_ = directoryFile.Close()
		return fmt.Errorf("sync configuration directory: %w", err)
	}
	if err := directoryFile.Close(); err != nil {
		return fmt.Errorf("close configuration directory: %w", err)
	}
	return nil
}
