# Tally MCP Server (Model Context Protocol)

[![License: MIT](https://img.shields.io/badge/License-MIT-blue.svg)](https://opensource.org/licenses/MIT)
[![Go Report Card](https://goreportcard.com/badge/github.com/gops/TallyMCP)](https://goreportcard.com/report/github.com/gops/TallyMCP)
[![Release](https://img.shields.io/github/v/release/gops/TallyMCP)](https://github.com/gops/TallyMCP/releases)

> [!WARNING]
> **Disclaimer**: This is AI-generated code and can have errors or mistakes. Use at your own risk. Always verify and test thoroughly before running against production Tally accounting data.

A high-performance, **self-updating Windows executable** written in Go that acts as a bridge between **Tally ERP 9 / TallyPrime** and AI assistants (Claude, Cursor, custom agents) across your **Local Network (LAN)** using the standard **Model Context Protocol (MCP)**.

```
┌─────────────────────────────────┐           ┌────────────────────────────────────────┐           ┌────────────────────────────────────┐
│   Tally ERP 9 / TallyPrime      │           │          tally-mcp.exe                 │           │        AI Clients on LAN           │
│   (Windows Host, Port 9000)     │ ◄───────► │       (Self-Updating Server)           │ ◄───────► │  (Claude Desktop, Cursor, Agents)  │
│      XML / TDL Server           │ HTTP XML  │  - Tally XML Engine                    │  MCP SSE  │  Endpoint:                         │
│                                 │           │  - GitHub Release Self-Updater         │  Port     │  http://<server-ip>:8080/sse       │
└─────────────────────────────────┘           │  - Optional Bearer Token Auth          │  8080     │                                    │
                                              └────────────────────────────────────────┘           └────────────────────────────────────┘
```

---

## Key Features

- ⚡ **Standalone Windows System Tray App**: Zero external runtime dependencies. Runs silently in the **Windows System Tray (Notification Area)** without an intrusive command prompt / cmd window. Right-click the tray icon to view active port status, open the web dashboard, edit `config.yaml`, toggle **Allow through Windows Firewall**, toggle **Start with Windows**, check for updates, or exit. (Use `--no-tray` for headless/server mode).
- 🛡️ **One-Click Windows Firewall Configuration**: Easily allow inbound connections (TCP port range `8080-8090`) directly from the system tray menu (`🛡️ Allow through Windows Firewall` with a dynamic checkmark) or via CLI (`--firewall allow`). Automatically prompts for UAC elevation if needed, eliminating network friction for LAN users and remote AI agents.
- 🔌 **Automatic Port Conflict Resolution**: If port `8080` is occupied by another app or running service, Tally MCP automatically discovers and binds the next available port (e.g. `8081`), displays the active port in the system tray menu and tooltips, and routes dashboard clicks accordingly.
- 🚀 **Start with Windows**: Seamlessly runs on system startup with a single click from the System Tray menu (shows a checkmark when enabled) or via CLI (`--startup enable`). Stored cleanly in standard user registry (`HKCU\Software\Microsoft\Windows\CurrentVersion\Run`), requiring no admin elevation.
- 🔄 **Automated Self-Updater**:
  - Automatically queries GitHub Releases for newer releases on startup.
  - Non-intrusive notification in console and via MCP tools.
  - Safe Windows binary replacement in-place (renames locked `.exe` and cleans up `.old` on next start).
  - Can be triggered manually via `--update` or via the `system_apply_update` MCP tool.
- 🌐 **Dual Transport Local Network MCP Server**:
  - Exposes **Streamable HTTP** (`/mcp`) according to the modern MCP specification (2025-03-26) alongside legacy **HTTP Server-Sent Events (SSE)** (`/sse`).
  - Seamless out-of-the-box compatibility with Cursor, Windsurf, Claude Desktop, VS Code, and custom AI agents without requiring stdio bridges (`mcp-remote`).
  - Any authorized PC, Mac, or tablet on your local network can connect their AI agents.
  - Optional API Key / Bearer token security (`Authorization: Bearer <key>` or `?token=<key>`).
  - Full CORS enabled for browser-based AI dashboards and clients.
- 📊 **Deep Tally ERP 9 & TallyPrime Integration**:
  - Check active connection and company details (`tally_status`, `tally_list_companies`).
  - Account ledgers with opening & closing balances and contact info (`tally_list_ledgers`, `tally_get_ledger`).
  - Inventory & Stock summary (`tally_get_stock_summary`).
  - Vouchers & transactions with date filters and automatic LLM pagination (`tally_list_vouchers`).
  - Deep Voucher Drilldown with full debit/credit legs, inventory items, batch details, bill allocations, and GST tax breakdown (`tally_get_voucher`).
  - Real-time Trial Balance report (`tally_get_trial_balance`).
  - Dedicated Financial Reports: Profit & Loss statement (`tally_get_profit_and_loss`), Balance Sheet (`tally_get_balance_sheet`), Bills Receivable & Payable aging analysis (`tally_get_outstanding_receivables`, `tally_get_outstanding_payables`), and Day Book entries (`tally_get_daybook`).
  - Advanced raw XML / TDL query executor (`tally_execute_xml`).
  - Ledger creation (`tally_create_ledger`).
- 📜 **100% Open Source**: Permissive [MIT License](./LICENSE).

---

## 1. Quick Setup in Tally ERP / TallyPrime

Before starting `tally-mcp.exe`, enable the HTTP Server in Tally:

### For TallyPrime:
1. Press `F1` (Help) > **Settings** > **Connectivity**.
2. Set **TallyPrime is acting as**: `Both` (or `Server`).
3. Set **Port**: `9000` (default).
4. Restart TallyPrime.

### For Tally ERP 9:
1. Press `F12` (Configure) > **Advanced Configuration**.
2. Set **Tally is acting as**: `Both` (or `Server`).
3. Set **Port**: `9000`.
4. Press `Enter` to accept and restart Tally.

---

## 2. Installation & Running

### Option A: Download Pre-compiled Binary (Recommended)
1. Go to **[Releases](https://github.com/gops/TallyMCP/releases)** on GitHub.
2. Download `tally-mcp-windows-amd64.zip` (or `.exe`).
3. Extract to any folder on the Windows PC (e.g. `C:\TallyMCP\`).
4. Double-click `tally-mcp.exe` or run from PowerShell / Command Prompt:
   ```cmd
   tally-mcp.exe
   ```

Upon first launch, `tally-mcp.exe` automatically generates a commented `config.yaml` in its directory and displays your LAN connection URLs:
```
  ╔═══════════════════════════════════════════════════════════════════╗
  ║                      TALLY ERP MCP SERVER                         ║
  ║             Bridging AI Agents to Tally ERP & TallyPrime          ║
  ║                   Open Source (MIT License)                       ║
  ╚═══════════════════════════════════════════════════════════════════╝
  • Version:        v1.0.0
  • Tally URL:      http://localhost:9000
  • MCP Transport:  sse
  • Listen Address: 0.0.0.0:8080
  • Config File:    config.yaml
  • Auth Security:  Disabled (Open LAN access)
  • GitHub Repo:    gops/TallyMCP

  LAN MCP Endpoints:
    -> Streamable HTTP: http://192.168.1.50:8080/mcp  (Spec 2025-03-26)
    -> Legacy SSE:      http://192.168.1.50:8080/sse
```

---

## 3. Connecting AI Clients (Cursor, Claude Desktop, Windsurf, Custom Agents)

### A. Streamable HTTP (Recommended for Cursor, Windsurf, & Modern MCP Clients)
In Cursor, Windsurf, or your MCP client's configuration:

```json
{
  "mcpServers": {
    "tally": {
      "url": "http://192.168.1.50:8080/mcp"
    }
  }
}
```

If you enabled authentication in `config.yaml`:
```json
{
  "mcpServers": {
    "tally": {
      "url": "http://192.168.1.50:8080/mcp",
      "headers": {
        "Authorization": "Bearer your-secret-api-key"
      }
    }
  }
}
```
*(Or append `?token=your-secret-api-key` to the URL).*

### B. Legacy SSE (Claude Desktop)
Add the server to your Claude Desktop config (`%APPDATA%\Claude\claude_desktop_config.json` on Windows or `~/Library/Application Support/Claude/claude_desktop_config.json` on macOS):

```json
{
  "mcpServers": {
    "tally": {
      "url": "http://192.168.1.50:8080/sse"
    }
  }
}
```
*(Replace `192.168.1.50` with the IP address of your Windows computer running tally-mcp).*

If you enabled authentication in `config.yaml`:
```json
{
  "mcpServers": {
    "tally": {
      "url": "http://192.168.1.50:8080/sse?token=your-secret-api-key"
    }
  }
}
```

---

## 4. MCP Tools Reference

| MCP Tool Name | Description | Key Arguments |
|---|---|---|
| `tally_status` | Test Tally connectivity, server health, and loaded companies | *none* |
| `tally_list_companies` | List all open / loaded companies in Tally | *none* |
| `tally_list_ledgers` | Fetch ledgers with balances, contact details, and GSTIN | `group` (e.g. `"Sundry Debtors"`) |
| `tally_get_ledger` | Fetch complete ledger profile and address | `name` *(required)* |
| `tally_list_vouchers` | Search vouchers with pagination and context window safety (`has_more`, `next_offset`) | `voucher_type`, `from_date`, `to_date`, `limit` (default: 50, max: 250), `offset` (default: 0) |
| `tally_get_voucher` | Full voucher drilldown: all ledger legs, inventory quantities, bill allocations, and tax split | `voucher_no` or `master_id` *(required)*, `voucher_type`, `date` |
| `tally_get_stock_summary`| Inspect inventory quantities, units, and valuations | `group` (optional filter) |
| `tally_get_trial_balance` | Fetch formatted Trial Balance statement | `from_date`, `to_date` |
| `tally_get_profit_and_loss` | Fetch Profit & Loss statement (Revenue, COGS, Gross Profit, Expenses, Net Profit) | `from_date`, `to_date` |
| `tally_get_balance_sheet` | Fetch Balance Sheet report (Liabilities vs Assets, Capital, Loans, Debtors, Creditors) | `as_on_date`, `from_date` |
| `tally_get_outstanding_receivables` | Fetch Bills Receivable bill-by-bill aging report (who owes us money) | `party`, `from_date`, `to_date` |
| `tally_get_outstanding_payables` | Fetch Bills Payable bill-by-bill aging report (who we owe money to) | `party`, `from_date`, `to_date` |
| `tally_get_daybook` | Fetch Day Book report listing all transactions entered on a specific date | `date` *(YYYYMMDD or YYYY-MM-DD)* |
| `tally_execute_xml` | Execute raw TDL or XML envelopes against Tally | `xml_content` *(required)* |
| `tally_create_ledger` | Create a new ledger master in Tally | `name`, `parent`, `opening_balance` |
| `system_check_update` | Query GitHub Releases for available updates | *none* |
| `system_apply_update` | Download and self-update the running binary | *none* |

---

## 5. Self-Updating Mechanism

The binary includes a robust updater designed specifically for Windows executable locking constraints:

1. **Automatic Startup Check**:
   On launch, the binary contacts `https://api.github.com/repos/{owner}/{repo}/releases/latest` to check if a higher semantic version tag exists.
2. **Safe Windows Binary Swap**:
   When updating:
   - The new release binary is downloaded to a temporary file on the same disk drive.
   - The running executable `tally-mcp.exe` is renamed to `tally-mcp.exe.old` (allowed by Windows NTFS).
   - The new binary is moved to `tally-mcp.exe`.
   - On subsequent restarts, `tally-mcp.exe` purges the `.old` file.
3. **CLI Update Commands**:
   - Check if an update is available:
     ```cmd
     tally-mcp.exe --check-update
     ```
   - Immediately download and apply the update:
     ```cmd
     tally-mcp.exe --update
     ```
   - Manage automatic startup with Windows:
     ```cmd
     tally-mcp.exe --startup enable    # Start automatically on Windows boot
     tally-mcp.exe --startup disable   # Disable automatic startup
     tally-mcp.exe --startup status    # Check startup status
     ```
   - Manage Windows Firewall rule for LAN connections:
     ```cmd
     tally-mcp.exe --firewall allow    # Allow inbound TCP port range (8080-8090)
     tally-mcp.exe --firewall remove   # Remove TallyMCP Windows Firewall rule
     tally-mcp.exe --firewall status   # Check if firewall rule is active
     ```

---

## 6. Configuration Reference (`config.yaml`)

```yaml
server:
  # Network address to listen on ("0.0.0.0:8080" allows any machine on LAN)
  listen_address: "0.0.0.0:8080"
  # Transport mode: "sse" (HTTP Server-Sent Events) or "stdio" (Standard I/O)
  transport: "sse"

tally:
  # Address where Tally ERP / TallyPrime is running
  url: "http://localhost:9000"
  timeout_seconds: 30
  # Leave empty to use whichever company is currently open in Tally
  default_company: ""

auth:
  # Set to true to require authentication from LAN clients
  enabled: false
  # Secret token required from MCP clients (via Bearer token or ?token=...)
  api_key: ""

updater:
  # Your GitHub repository (owner/repo) hosting the releases
  github_repo: "gops/TallyMCP"
  # Check for new version when the server starts
  check_on_startup: true
  # Automatically download and install new version on startup
  auto_apply: false

log:
  level: "info"
```

### Environment Variable Overrides
All parameters can also be configured via environment variables:
- `TALLY_URL`: e.g. `http://localhost:9000`
- `TALLY_MCP_LISTEN`: e.g. `0.0.0.0:8080`
- `TALLY_MCP_API_KEY`: e.g. `secret-token`
- `TALLY_MCP_GITHUB_REPO`: e.g. `your-username/tally-mcp`

---

## 7. Automated GitHub Releases (CI/CD)

The repository includes a ready-to-use GitHub Actions workflow [`.github/workflows/release.yml`](./.github/workflows/release.yml).

To publish a new release:
1. Commit and push your code to GitHub.
2. Push a git version tag:
   ```bash
   git tag v1.0.1
   git push origin v1.0.1
   ```
3. GitHub Actions will automatically:
   - Compile standalone Windows binaries (`amd64` and `arm64`).
   - Generate SHA-256 `checksums.txt`.
   - Publish a new GitHub Release with attached `.zip` and `.exe` assets.
   - Any running `tally-mcp.exe` instances will detect the new version and can update with `--update`!

---

## 8. Building from Source

Requirements: [Go 1.24+](https://go.dev/)

```bash
# Clone the repository
git clone https://github.com/gops/TallyMCP.git
cd tally-mcp

# Run unit tests
go test -v ./...

# Build Windows .exe
make build
# Binary will be generated at bin/tally-mcp.exe
```

---

## Disclaimer

This is AI-generated code and can have errors or mistakes. Use at your own risk. It is provided "as is" without warranty of any kind. Always test queries, actions, and reports against a test or backup Tally environment before deploying or using it with production financial books.

---

## License

This project is licensed under the [MIT License](./LICENSE) - free for personal, commercial, and enterprise use.
