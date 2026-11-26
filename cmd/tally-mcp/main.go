package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"net"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"tally-mcp/internal/config"
	"tally-mcp/internal/mcp"
	"tally-mcp/internal/relay"
	"tally-mcp/internal/tally"
	"tally-mcp/internal/tray"
	"tally-mcp/internal/updater"
)

// Build information injected via -ldflags during compilation.
var (
	Version   = "v1.0.0"
	Commit    = "dev"
	BuildDate = "unknown"
)

func main() {
	// Step 0: Attach to parent console on Windows if invoked from cmd/PowerShell
	tray.AttachParentConsole()

	// Clean up any stale .old binary from a previous self-update
	updater.CleanupOldBinary()

	// Step 1: Parse CLI flags
	configFlag := flag.String("config", "", "Path to configuration YAML file")
	tallyURLFlag := flag.String("tally-url", "", "Tally ERP HTTP URL (e.g. http://localhost:9000)")
	listenFlag := flag.String("listen", "", "Listen address for MCP server (e.g. 0.0.0.0:8080)")
	transportFlag := flag.String("transport", "", "Transport mode: sse or stdio")
	apiKeyFlag := flag.String("api-key", "", "API key / Bearer token for local network MCP authentication")
	repoFlag := flag.String("repo", "", "GitHub repository for updates (owner/repo)")
	noTrayFlag := flag.Bool("no-tray", false, "Disable system tray and run in console mode")
	versionFlag := flag.Bool("version", false, "Print version and exit")
	checkUpdateFlag := flag.Bool("check-update", false, "Check for newer GitHub releases and exit")
	updateFlag := flag.Bool("update", false, "Download and apply the latest update, then exit")
	startupFlag := flag.String("startup", "", "Configure startup with Windows: enable, disable, or status")
	firewallFlag := flag.String("firewall", "", "Configure Windows Firewall rule: allow, remove, or status")
	initConfigFlag := flag.Bool("init-config", false, "Generate a default config.yaml in the current directory and exit")
	autoStartTallyFlag := flag.Bool("auto-start-tally", false, "Automatically start Tally ERP/Prime if not running")
	tallyPathFlag := flag.String("tally-path", "", "Path to tally.exe executable (e.g. C:\\Program Files\\TallyPrime\\tally.exe)")
	startTallyFlag := flag.Bool("start-tally", false, "Start Tally immediately if not running and exit")
	readOnlyFlag := flag.Bool("read-only", false, "Disable mutating MCP operations (tally_create_ledger, tally_create_voucher, etc.)")
	login1ERPFlag := flag.Bool("1erp-login", false, "Log in to 1erp.work via browser (or with --1erp-token) to enable cloud relay")
	token1ERPFlag := flag.String("1erp-token", "", "Authenticate to 1erp.work using a device/API token (headless mode)")
	logout1ERPFlag := flag.Bool("1erp-logout", false, "Log out from 1erp.work and remove saved relay credentials")
	status1ERPFlag := flag.Bool("1erp-status", false, "Show 1erp.work login and relay configuration status")
	url1ERPFlag := flag.String("1erp-url", "", "Override 1erp.work server URL (default https://1erp.work)")

	flag.Parse()

	// Handle version
	if *versionFlag {
		fmt.Printf("tally-mcp %s (commit: %s, built: %s)\n", Version, Commit, BuildDate)
		return
	}

	// Handle init-config
	if *initConfigFlag {
		target := "config.yaml"
		if err := config.WriteDefaultConfigFile(target); err != nil {
			log.Fatalf("[ERROR] Failed to write config file: %v", err)
		}
		fmt.Printf("Default configuration file successfully created at %s\n", target)
		return
	}

	// Handle startup configuration
	if *startupFlag != "" {
		switch strings.ToLower(*startupFlag) {
		case "enable", "on", "yes", "true":
			if err := tray.SetAutoStart(true); err != nil {
				log.Fatalf("[ERROR] Failed to enable startup: %v", err)
			}
			fmt.Println("Successfully enabled: Tally MCP will start automatically when Windows boots.")
		case "disable", "off", "no", "false":
			if err := tray.SetAutoStart(false); err != nil {
				log.Fatalf("[ERROR] Failed to disable startup: %v", err)
			}
			fmt.Println("Successfully disabled: Tally MCP will no longer start with Windows.")
		case "status", "check":
			if tray.IsAutoStartEnabled() {
				fmt.Println("Start with Windows: Enabled")
			} else {
				fmt.Println("Start with Windows: Disabled")
			}
		default:
			log.Fatalf("Invalid -startup value '%s': choose enable, disable, or status", *startupFlag)
		}
		return
	}

	// Handle firewall configuration
	if *firewallFlag != "" {
		switch strings.ToLower(*firewallFlag) {
		case "allow", "enable", "add":
			targetPort := 8080
			if *listenFlag != "" {
				if _, pStr, err := net.SplitHostPort(*listenFlag); err == nil {
					if p, err := strconv.Atoi(pStr); err == nil && p > 0 {
						targetPort = p
					}
				}
			}
			fmt.Printf("Configuring Windows Firewall rule for Tally MCP (TCP port range %d-%d)...\n", targetPort, targetPort+10)
			if err := tray.ConfigureFirewallRule(0, targetPort); err != nil {
				log.Fatalf("[ERROR] Failed to configure Windows Firewall: %v", err)
			}
			fmt.Println("Successfully configured: Inbound TCP connections are allowed through Windows Firewall.")
		case "remove", "disable", "delete":
			fmt.Println("Removing Windows Firewall rule for Tally MCP...")
			if err := tray.RemoveFirewallRule(0); err != nil {
				log.Fatalf("[ERROR] Failed to remove Windows Firewall rule: %v", err)
			}
			fmt.Println("Successfully removed: Tally MCP Windows Firewall rule has been deleted.")
		case "status", "check":
			if tray.IsFirewallRuleConfigured() {
				fmt.Println("Windows Firewall: Allowed (TallyMCP inbound rule is active)")
			} else {
				fmt.Println("Windows Firewall: Not Configured (Run with --firewall allow or right-click system tray)")
			}
		default:
			log.Fatalf("Invalid -firewall value '%s': choose allow, remove, or status", *firewallFlag)
		}
		return
	}

	// Step 2: Load configuration
	cfg, configPath, err := config.Load(*configFlag)
	if err != nil {
		log.Printf("[WARN] Error loading configuration: %v (falling back to defaults)", err)
		cfg = config.DefaultConfig()
	}

	// CLI flags override config file
	if *tallyURLFlag != "" {
		cfg.Tally.URL = *tallyURLFlag
	}
	if *listenFlag != "" {
		cfg.Server.ListenAddress = *listenFlag
	}
	if *transportFlag != "" {
		cfg.Server.Transport = *transportFlag
	}
	if *apiKeyFlag != "" {
		cfg.Auth.APIKey = *apiKeyFlag
		cfg.Auth.Enabled = true
	}
	if *repoFlag != "" {
		cfg.Updater.GitHubRepo = *repoFlag
	}
	if *autoStartTallyFlag {
		cfg.Tally.AutoStart = true
	}
	if *tallyPathFlag != "" {
		cfg.Tally.ExePath = *tallyPathFlag
	}
	if *readOnlyFlag {
		cfg.Server.ReadOnly = true
	}
	if *url1ERPFlag != "" {
		cfg.Relay.ServerURL = strings.TrimRight(*url1ERPFlag, "/")
	}

	// Handle 1erp.work login / logout / status flags
	if *login1ERPFlag {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
		defer cancel()
		var (
			creds *relay.Credentials
			err   error
		)
		if *token1ERPFlag != "" {
			fmt.Printf("Verifying token with %s...\n", cfg.Relay.ServerURL)
			creds, err = relay.LoginWithToken(ctx, cfg.Relay.ServerURL, *token1ERPFlag, cfg.Relay.CredentialsFile)
		} else {
			fmt.Printf("Opening browser to log in to %s...\n", cfg.Relay.ServerURL)
			creds, err = relay.LoginWithBrowser(ctx, cfg.Relay.ServerURL, cfg.Relay.CredentialsFile)
		}
		if err != nil {
			log.Fatalf("[ERROR] 1erp.work login failed: %v", err)
		}
		fmt.Printf("Logged in to 1erp.work successfully!\n")
		fmt.Printf("  Account:   %s\n", creds.UserEmail)
		fmt.Printf("  Tenant ID: %s\n", creds.TenantID)
		fmt.Printf("  Device ID: %s (%s)\n", creds.DeviceID, creds.DeviceName)
		fmt.Printf("  Relay:     %s (read_only=%v)\n", creds.ServerURL, !cfg.Relay.AllowWrites)
		return
	}

	if *logout1ERPFlag {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := relay.Logout(ctx, cfg.Relay.CredentialsFile); err != nil {
			log.Fatalf("[ERROR] 1erp.work logout failed: %v", err)
		}
		fmt.Println("Logged out from 1erp.work. Local relay credentials removed.")
		return
	}

	if *status1ERPFlag {
		creds, err := relay.LoadCredentials(cfg.Relay.CredentialsFile)
		if err != nil {
			fmt.Printf("1erp.work Status: Not logged in (%v)\n", err)
			fmt.Println("Run with --1erp-login to authenticate and enable the 1erp.work relay.")
			return
		}
		fmt.Printf("1erp.work Status: Logged in\n")
		fmt.Printf("  Account:      %s\n", creds.UserEmail)
		fmt.Printf("  Tenant ID:    %s\n", creds.TenantID)
		fmt.Printf("  Device ID:    %s (%s)\n", creds.DeviceID, creds.DeviceName)
		fmt.Printf("  Server URL:   %s\n", creds.ServerURL)
		fmt.Printf("  Relay Active: %v (allow_writes=%v)\n", cfg.Relay.Enabled, cfg.Relay.AllowWrites)
		return
	}

	// Handle start-tally flag
	if *startTallyFlag {
		targetPath := *tallyPathFlag
		if targetPath == "" {
			targetPath = cfg.Tally.ExePath
		}
		fmt.Printf("Checking Tally connection at %s...\n", cfg.Tally.URL)
		tallyClient := tally.NewClient(cfg.Tally)
		ctx, cancel := context.WithTimeout(context.Background(), 35*time.Second)
		defer cancel()

		res, err := tallyClient.StartTally(ctx, targetPath, 30*time.Second)
		if err != nil {
			log.Fatalf("[ERROR] Failed to start Tally: %v", err)
		}
		if res.Status == "already_running" {
			fmt.Printf("Tally is already running and responding at %s (%d loaded companies)\n", cfg.Tally.URL, res.CompaniesCount)
		} else {
			fmt.Printf("Tally started successfully! Connected at %s (%d loaded companies)\n", cfg.Tally.URL, res.CompaniesCount)
			if res.ExePath != "" {
				fmt.Printf("Executable: %s\n", res.ExePath)
			}
		}
		return
	}

	// Handle check-update flag
	if *checkUpdateFlag {
		fmt.Printf("Checking for updates from GitHub repo: %s ...\n", cfg.Updater.GitHubRepo)
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()

		info, err := updater.CheckForUpdate(ctx, Version, cfg.Updater.GitHubRepo)
		if err != nil {
			log.Fatalf("[ERROR] Update check failed: %v", err)
		}

		if info.HasUpdate {
			fmt.Printf("A new version is available: %s (Current: %s)\n", info.LatestVersion, Version)
			fmt.Printf("Asset: %s (%d bytes)\n", info.AssetName, info.AssetSize)
			fmt.Printf("Release Notes:\n%s\n", info.ReleaseNotes)
			fmt.Printf("To update, run: %s --update\n", filepath.Base(os.Args[0]))
		} else {
			fmt.Printf("You are running the latest version (%s). No updates available.\n", Version)
		}
		return
	}

	// Handle update flag
	if *updateFlag {
		fmt.Printf("Checking for updates from %s ...\n", cfg.Updater.GitHubRepo)
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
		defer cancel()

		info, err := updater.CheckForUpdate(ctx, Version, cfg.Updater.GitHubRepo)
		if err != nil {
			log.Fatalf("[ERROR] Update check failed: %v", err)
		}

		if !info.HasUpdate {
			fmt.Printf("Already running the latest version (%s). Nothing to update.\n", Version)
			return
		}

		fmt.Printf("Downloading and applying update %s (%s)...\n", info.LatestVersion, info.AssetName)
		err = updater.ApplyUpdate(ctx, info, func(downloaded, total int64) {
			if total > 0 {
				fmt.Printf("\rDownloading: %d%% (%d / %d bytes)", (downloaded*100)/total, downloaded, total)
			} else {
				fmt.Printf("\rDownloading: %d bytes", downloaded)
			}
		})
		fmt.Println()

		if err != nil {
			log.Fatalf("[ERROR] Failed to install update: %v", err)
		}

		fmt.Printf("Successfully updated to %s! Please restart %s.\n", info.LatestVersion, filepath.Base(os.Args[0]))
		return
	}

	// If no config file existed, write a default one in the working directory for convenience
	if configPath == "" {
		_ = config.WriteDefaultConfigFile("config.yaml")
	}

	// Step 3: Bind network listener for MCP Server (with automatic port conflict resolution)
	var (
		listener     net.Listener
		activePort   int
		portChanged  bool
		originalPort int
	)

	if cfg.Server.Transport != "stdio" {
		_, originalPort = mcp.ParseHostAndPort(cfg.Server.ListenAddress)

		var err error
		listener, _, activePort, portChanged, err = mcp.BindAvailableListener(cfg.Server.ListenAddress)
		if err != nil {
			log.Fatalf("[FATAL] Failed to bind network port: %v", err)
		}

		printBanner(cfg, configPath, activePort, portChanged, originalPort)
	}

	// Step 4: Initialize Tally Client (auto-update loop starts once context & server are wired)
	tallyClient := tally.NewClient(cfg.Tally)

	// Quick check of Tally connection
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		status, err := tallyClient.Ping(ctx)
		if err != nil {
			log.Printf("[WARN] Tally connection check: Tally ERP is not responding at %s.", cfg.Tally.URL)
			if cfg.Tally.AutoStart {
				log.Printf("[INFO] Auto-start is enabled. Attempting to launch Tally ERP / TallyPrime...")
				startCtx, startCancel := context.WithTimeout(context.Background(), 30*time.Second)
				defer startCancel()
				res, startErr := tallyClient.EnsureRunning(startCtx, 30*time.Second)
				if startErr != nil {
					log.Printf("[WARN] Auto-start failed: %v", startErr)
				} else if res.Connected {
					log.Printf("[INFO] Tally auto-started successfully! (%d loaded companies found)", res.CompaniesCount)
				}
			} else {
				exe, findErr := tallyClient.FindExecutable("")
				if findErr == nil && exe != "" {
					log.Printf("[INFO] Discovered Tally executable at: %s (Right-click tray -> 'Start Tally', pass --auto-start-tally, or call 'tally_start' tool)", exe)
				} else {
					log.Printf("[INFO] Please ensure Tally is open and server port 9000 is enabled.")
				}
			}
		} else {
			log.Printf("[INFO] Connected to Tally ERP! (%d loaded companies found)", status.CompaniesCount)
		}
	}()

	// Step 6: Initialize MCP Server
	srv := mcp.NewServer(cfg, tallyClient, Version)

	// Step 7: Handle OS signals for graceful shutdown
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	var restartRequested atomic.Bool

	// Step 7a: Background auto-updater (checks every 5 minutes by default, auto-downloads, installs & restarts)
	autoUpdater := updater.NewAutoUpdater(updater.AutoConfig{
		CurrentVersion: Version,
		Repo:           cfg.Updater.GitHubRepo,
		CheckOnStartup: cfg.Updater.CheckOnStartup,
		Interval:       time.Duration(cfg.Updater.CheckIntervalSeconds) * time.Second,
		AutoApply:      cfg.Updater.AutoApply,
		OnAvailableFn: func(info *updater.UpdateInfo) {
			log.Println("==================================================================")
			log.Printf("🎉 A NEW VERSION IS AVAILABLE: %s (current: %s)", info.LatestVersion, Version)
			log.Printf("   Run '%s --update' to apply the update.", filepath.Base(os.Args[0]))
			log.Println("==================================================================")
		},
		OnRestartFn: func(info *updater.UpdateInfo) error {
			restartRequested.Store(true)
			if err := updater.PrepareRestart(cfg.Server.Transport == "stdio"); err != nil {
				restartRequested.Store(false)
				return err
			}
			cancel()
			return nil
		},
	})
	go autoUpdater.Run(ctx)

	// Step 7b: Start 1erp.work outbound relay if enabled (only connects when logged in)
	if cfg.Relay.Enabled {
		allowRelayWrites := cfg.Relay.AllowWrites && !cfg.Server.ReadOnly
		relayClient := relay.NewClient(cfg.Relay.CredentialsFile, allowRelayWrites, Version, srv.ServeRelayMCP)
		go func() {
			if err := relayClient.Run(ctx); err != nil {
				log.Printf("[WARN] 1ERP Relay stopped: %v", err)
			}
		}()
	}

	serverDone := make(chan struct{})
	serverErrChan := make(chan error, 1)
	go func() {
		defer close(serverDone)
		if listener != nil {
			if err := srv.RunSSEWithListener(ctx, listener); err != nil {
				serverErrChan <- err
			}
		} else {
			if err := srv.Run(ctx); err != nil {
				serverErrChan <- err
			}
		}
	}()

	// If headless mode is requested or stdio transport is used, run without tray
	if *noTrayFlag || cfg.Server.Transport == "stdio" {
		select {
		case err := <-serverErrChan:
			log.Fatalf("[FATAL] Server terminated with error: %v", err)
		case <-ctx.Done():
			select {
			case <-serverDone:
			case <-time.After(3 * time.Second):
			}
			log.Println("[INFO] Server exited cleanly.")
		}
		if restartRequested.Load() {
			if err := updater.CompleteRestart(); err != nil {
				log.Fatalf("[FATAL] Auto-restart failed: %v", err)
			}
		}
		return
	}

	// Step 8: Run System Tray (runs on main thread on Windows; blocks until exit)
	var (
		lastUpdateInfo *updater.UpdateInfo
		updateMu       sync.Mutex
	)

	dashPort := activePort
	if dashPort == 0 {
		dashPort = 8080
	}
	dashURL := fmt.Sprintf("http://localhost:%d", dashPort)

	trayCfg := tray.Config{
		Version:      Version,
		Port:         activePort,
		PortChanged:  portChanged,
		OriginalPort: originalPort,
		ListenURL:    dashURL,
		TallyURL:     cfg.Tally.URL,
		ConfigPath:   configPath,
		GitHubRepo:   cfg.Updater.GitHubRepo,
		OnStartTally: func(c context.Context) (string, string, error) {
			res, err := tallyClient.StartTally(c, "", 30*time.Second)
			if err != nil {
				return "error", err.Error(), err
			}
			return res.Status, res.Message, nil
		},
		OnCheckUpdate: func(c context.Context) (bool, string, string, error) {
			info, err := updater.CheckForUpdate(c, Version, cfg.Updater.GitHubRepo)
			if err != nil {
				return false, "", "", err
			}
			updateMu.Lock()
			lastUpdateInfo = info
			updateMu.Unlock()
			return info.HasUpdate, info.LatestVersion, info.ReleaseNotes, nil
		},
		OnApplyUpdate: func(c context.Context, progressFn func(downloaded, total int64)) error {
			updateMu.Lock()
			info := lastUpdateInfo
			updateMu.Unlock()

			if info == nil || !info.HasUpdate {
				var err error
				info, err = updater.CheckForUpdate(c, Version, cfg.Updater.GitHubRepo)
				if err != nil {
					return err
				}
				if !info.HasUpdate {
					return fmt.Errorf("already running the latest version (%s)", Version)
				}
			}
			return updater.ApplyUpdate(c, info, progressFn)
		},
		OnRestart: func() error {
			restartRequested.Store(true)
			if err := updater.PrepareRestart(false); err != nil {
				restartRequested.Store(false)
				return err
			}
			cancel()
			return nil
		},
		OnQuit: func() {
			cancel()
		},
	}

	if err := tray.Run(ctx, trayCfg); err != nil {
		log.Printf("[WARN] System tray error: %v", err)
	}

	// Wait for server to finish cleanup
	select {
	case err := <-serverErrChan:
		log.Fatalf("[FATAL] Server terminated with error: %v", err)
	case <-serverDone:
	case <-time.After(3 * time.Second):
	}
	log.Println("[INFO] Server exited cleanly.")
	if restartRequested.Load() {
		if err := updater.CompleteRestart(); err != nil {
			log.Fatalf("[FATAL] Auto-restart failed: %v", err)
		}
	}
}

func printBanner(cfg *config.Config, configPath string, activePort int, portChanged bool, originalPort int) {
	fmt.Println(`
  ╔═══════════════════════════════════════════════════════════════════╗
  ║                      TALLY ERP MCP SERVER                         ║
  ║             Bridging AI Agents to Tally ERP & TallyPrime          ║
  ║                   Open Source (MIT License)                       ║
  ╚═══════════════════════════════════════════════════════════════════╝`)
	fmt.Printf("  • Version:        %s\n", Version)
	fmt.Printf("  • Tally URL:      %s\n", cfg.Tally.URL)
	fmt.Printf("  • MCP Transport:  %s\n", cfg.Server.Transport)
	if portChanged {
		fmt.Printf("  • Listen Address: port %d (auto-switched; port %d was busy)\n", activePort, originalPort)
	} else {
		fmt.Printf("  • Listen Address: %s\n", cfg.Server.ListenAddress)
	}
	if configPath != "" {
		fmt.Printf("  • Config File:    %s\n", configPath)
	} else {
		fmt.Printf("  • Config File:    Loaded defaults\n")
	}

	if cfg.Auth.Enabled {
		fmt.Printf("  • Auth Security:  Enabled (API key required)\n")
	} else {
		fmt.Printf("  • Auth Security:  Disabled (Open LAN access)\n")
	}
	if cfg.Tally.AutoStart {
		fmt.Printf("  • Auto-Start:     Enabled (Auto-launches Tally if stopped)\n")
	}
	if tray.IsFirewallRuleConfigured() {
		fmt.Printf("  • Win Firewall:   Allowed (Inbound TCP rule active)\n")
	} else {
		fmt.Printf("  • Win Firewall:   Not configured (Use tray menu -> 'Allow through Windows Firewall' or run --firewall allow)\n")
	}
	fmt.Printf("  • GitHub Repo:    %s\n", cfg.Updater.GitHubRepo)

	// Display local network IP addresses
	localIPs := getLocalIPs()
	if len(localIPs) > 0 {
		port := strconv.Itoa(activePort)
		if activePort == 0 {
			port = "8080"
		}
		fmt.Println("\n  LAN MCP Endpoints:")
		for _, ip := range localIPs {
			fmt.Printf("    -> Streamable HTTP: http://%s:%s/mcp  (Spec 2025-03-26)\n", ip, port)
			fmt.Printf("    -> Legacy SSE:      http://%s:%s/sse\n", ip, port)
		}
	}
	fmt.Println()
}

func getLocalIPs() []string {
	var ips []string
	addrs, err := net.InterfaceAddrs()
	if err != nil {
		return ips
	}
	for _, a := range addrs {
		if ipnet, ok := a.(*net.IPNet); ok && !ipnet.IP.IsLoopback() {
			if ipnet.IP.To4() != nil {
				ips = append(ips, ipnet.IP.String())
			}
		}
	}
	return ips
}
