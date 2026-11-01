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
	"syscall"
	"time"

	"tally-mcp/internal/config"
	"tally-mcp/internal/mcp"
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
		originalPort = 8080
		if _, portStr, err := net.SplitHostPort(cfg.Server.ListenAddress); err == nil {
			if p, err := strconv.Atoi(portStr); err == nil {
				originalPort = p
			}
		}

		var err error
		listener, _, activePort, portChanged, err = mcp.BindAvailableListener(cfg.Server.ListenAddress)
		if err != nil {
			log.Fatalf("[FATAL] Failed to bind network port: %v", err)
		}

		printBanner(cfg, configPath, activePort, portChanged, originalPort)
	}

	// Step 4: Check for updates on startup if enabled
	if cfg.Updater.CheckOnStartup && cfg.Updater.GitHubRepo != "owner/tally-mcp" {
		go func() {
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			info, err := updater.CheckForUpdate(ctx, Version, cfg.Updater.GitHubRepo)
			if err == nil && info != nil && info.HasUpdate {
				if cfg.Updater.AutoApply {
					log.Printf("[INFO] AutoApply is enabled. Upgrading to %s...", info.LatestVersion)
					if err := updater.ApplyUpdate(ctx, info, nil); err != nil {
						log.Printf("[WARN] Auto-update failed: %v", err)
					} else {
						log.Printf("[INFO] Successfully updated to %s. Please restart the server.", info.LatestVersion)
					}
				} else {
					log.Println("==================================================================")
					log.Printf("🎉 A NEW VERSION IS AVAILABLE: %s (current: %s)", info.LatestVersion, Version)
					log.Printf("   Run '%s --update' to apply the update.", filepath.Base(os.Args[0]))
					log.Println("==================================================================")
				}
			}
		}()
	}

	// Step 5: Initialize Tally Client
	tallyClient := tally.NewClient(cfg.Tally)

	// Quick check of Tally connection
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		status, err := tallyClient.Ping(ctx)
		if err != nil {
			log.Printf("[WARN] Tally connection check: Tally ERP is not responding at %s. Please ensure Tally is open and server port 9000 is enabled.", cfg.Tally.URL)
		} else {
			log.Printf("[INFO] Connected to Tally ERP! (%d loaded companies found)", status.CompaniesCount)
		}
	}()

	// Step 6: Initialize MCP Server
	srv := mcp.NewServer(cfg, tallyClient, Version)

	// Step 7: Handle OS signals for graceful shutdown
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	serverErrChan := make(chan error, 1)
	go func() {
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
			log.Println("[INFO] Server exited cleanly.")
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
	case <-time.After(1 * time.Second):
	}
	log.Println("[INFO] Server exited cleanly.")
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
