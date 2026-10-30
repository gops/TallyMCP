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
	"syscall"
	"time"

	"tally-mcp/internal/config"
	"tally-mcp/internal/mcp"
	"tally-mcp/internal/tally"
	"tally-mcp/internal/updater"
)

// Build information injected via -ldflags during compilation.
var (
	Version   = "v1.0.0"
	Commit    = "dev"
	BuildDate = "unknown"
)

func main() {
	// Step 0: Clean up any stale .old binary from a previous self-update
	updater.CleanupOldBinary()

	// Step 1: Parse CLI flags
	configFlag := flag.String("config", "", "Path to configuration YAML file")
	tallyURLFlag := flag.String("tally-url", "", "Tally ERP HTTP URL (e.g. http://localhost:9000)")
	listenFlag := flag.String("listen", "", "Listen address for MCP server (e.g. 0.0.0.0:8080)")
	transportFlag := flag.String("transport", "", "Transport mode: sse or stdio")
	apiKeyFlag := flag.String("api-key", "", "API key / Bearer token for local network MCP authentication")
	repoFlag := flag.String("repo", "", "GitHub repository for updates (owner/repo)")
	versionFlag := flag.Bool("version", false, "Print version and exit")
	checkUpdateFlag := flag.Bool("check-update", false, "Check for newer GitHub releases and exit")
	updateFlag := flag.Bool("update", false, "Download and apply the latest update, then exit")
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

	// Step 3: Print Banner (unless running in stdio mode)
	if cfg.Server.Transport != "stdio" {
		printBanner(cfg, configPath)
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

	if err := srv.Run(ctx); err != nil {
		log.Fatalf("[FATAL] Server terminated with error: %v", err)
	}
	log.Println("[INFO] Server exited cleanly.")
}

func printBanner(cfg *config.Config, configPath string) {
	fmt.Println(`
  ╔═══════════════════════════════════════════════════════════════════╗
  ║                      TALLY ERP MCP SERVER                         ║
  ║             Bridging AI Agents to Tally ERP & TallyPrime          ║
  ║                   Open Source (MIT License)                       ║
  ╚═══════════════════════════════════════════════════════════════════╝`)
	fmt.Printf("  • Version:        %s\n", Version)
	fmt.Printf("  • Tally URL:      %s\n", cfg.Tally.URL)
	fmt.Printf("  • MCP Transport:  %s\n", cfg.Server.Transport)
	fmt.Printf("  • Listen Address: %s\n", cfg.Server.ListenAddress)
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
	fmt.Printf("  • GitHub Repo:    %s\n", cfg.Updater.GitHubRepo)

	// Display local network IP addresses
	localIPs := getLocalIPs()
	if len(localIPs) > 0 {
		_, port, err := net.SplitHostPort(cfg.Server.ListenAddress)
		if err != nil {
			port = "8080"
		}
		fmt.Println("\n  LAN MCP SSE Endpoints:")
		for _, ip := range localIPs {
			fmt.Printf("    -> http://%s:%s/sse\n", ip, port)
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
