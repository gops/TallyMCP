# Tally MCP Server

[![License: MIT](https://img.shields.io/badge/License-MIT-blue.svg)](https://opensource.org/licenses/MIT)
[![Go Report Card](https://goreportcard.com/badge/github.com/gops/TallyMCP)](https://goreportcard.com/report/github.com/gops/TallyMCP)
[![Release](https://img.shields.io/github/v/release/gops/TallyMCP)](https://github.com/gops/TallyMCP/releases)

> [!WARNING]
> **Disclaimer**: This is AI-generated code and can have errors or mistakes. Use at your own risk. Always verify and test thoroughly before running against production Tally accounting data.

Connect **Tally ERP 9** and **TallyPrime** directly to your favorite AI assistants (**Claude Desktop**, **Cursor**, **Windsurf**, or custom AI agents) over your local network (LAN) using the standard **Model Context Protocol (MCP)**.

Ask questions about your accounts in plain English, inspect ledgers and stock levels, review financial statements, and generate PDF invoices on demand—all from your AI chat window.

```
┌─────────────────────────────────┐           ┌────────────────────────────────────────┐           ┌────────────────────────────────────┐
│   Tally ERP 9 / TallyPrime      │           │          tally-mcp.exe                 │           │        AI Assistants on LAN        │
│   (Windows Host, Port 9000)     │ ◄───────► │       (Runs in Windows Tray)           │ ◄───────► │  (Claude Desktop, Cursor, Agents)  │
│      Accounting Data            │ HTTP XML  │  - Zero-overload query queue           │  MCP SSE  │  Ask questions in plain English    │
│                                 │           │  - 1-click Firewall & Autostart        │  Port     │  Inspect ledgers, P&L, stock       │
└─────────────────────────────────┘           │  - Fast in-memory cache                │  8080     │  Generate PDF invoices             │
                                              └────────────────────────────────────────┘           └────────────────────────────────────┘
```

> 💡 **Looking for technical architecture, internal design, or developer setup?**  
> Check out the [ARCHITECTURE.md](./ARCHITECTURE.md) document for concurrency controls, XML/TDL protocols, token optimization, and building from source.

---

## What Can You Do With Tally MCP?

Chat with your accounting data directly using natural language:

- 💬 *"Give me a 30-second financial health summary of the company."*
- 💬 *"Who are our top 5 overdue debtors and how much do they owe?"*
- 💬 *"Show me all sales vouchers created this week over ₹50,000."*
- 💬 *"What is our current stock quantity and valuation for Item X?"*
- 💬 *"Generate a PDF of the Balance Sheet as of today and give me the download link."*
- 💬 *"Show the Profit & Loss statement for Q3 with major expense breakdowns."*

---

## Key Features

- 🖥️ **Silent Windows System Tray App**: Runs quietly in the **Windows Notification Area** without an intrusive Command Prompt (`cmd.exe`) window. Right-click the tray icon to view active port status, open the web dashboard, edit configuration, or check for updates.
- 🛡️ **1-Click Windows Firewall Setup**: Allow inbound connections across your office Wi-Fi or LAN with a single click from the tray menu (`🛡️ Allow through Windows Firewall`) or CLI (`--firewall allow`). Automatically handles Windows UAC elevation.
- 🔌 **Automatic Port Conflict Resolution**: If port `8080` is busy, Tally MCP automatically discovers and binds the next free port (e.g. `8081`) and updates tray indicators and dashboard links accordingly.
- 🚀 **Start with Windows**: Toggle automatic startup on Windows boot directly from the tray menu (`🚀 Start with Windows`) or CLI (`--startup enable`). Runs safely under your user profile without requiring admin privileges.
- 🌐 **LAN & AI Client Compatibility**: Connect from any Mac, PC, or tablet on your local network. Supports both modern **Streamable HTTP** (`/mcp`) for Cursor and Windsurf and legacy **SSE** (`/sse`) for Claude Desktop.
- 📊 **Deep Financial & Accounting Reports**:
  - Executive financial snapshot in $< 350$ tokens (`tally_get_financial_summary`).
  - Profit & Loss statement (`tally_get_profit_and_loss`).
  - Balance Sheet (`tally_get_balance_sheet`).
  - Bills Receivable & Payable aging analysis (`tally_get_outstanding_receivables`, `tally_get_outstanding_payables`).
  - Day Book transaction entries (`tally_get_daybook`).
  - Real-time Trial Balance (`tally_get_trial_balance`).
  - Deep Voucher Drilldown with debits/credits, inventory items, batch details, and tax breakdowns (`tally_get_voucher`).
- 📄 **On-Demand PDF Generation & Delivery**: Ask your AI to export any financial report or voucher invoice straight to PDF (`tally_generate_pdf`). Automatically saved locally and downloadable via HTTP (`/files/`).
- ⚡ **Optimized for AI Speed & Safety**: Protects Tally from freezing using a single-query serializer and cooldown buffer. Master records (ledgers, stock items) are cached in memory for sub-millisecond responses.
- 🔄 **Automatic Self-Updater**: Automatically notifies you when a newer version is released on GitHub. Update seamlessly in-place from the tray menu or CLI (`--update`).
- 📜 **100% Free & Open Source**: Permissive [MIT License](./LICENSE).

---

## 1. Quick Setup in Tally

Before starting Tally MCP, enable the built-in HTTP server in your Tally software:

### For TallyPrime:
1. Press `F1` (Help) > **Settings** > **Connectivity**.
2. Set **TallyPrime is acting as**: `Both` (or `Server`).
3. Set **Port**: `9000` (default).
4. Restart TallyPrime and open your desired company.

### For Tally ERP 9:
1. Press `F12` (Configure) > **Advanced Configuration**.
2. Set **Tally is acting as**: `Both` (or `Server`).
3. Set **Port**: `9000`.
4. Press `Enter` to accept and restart Tally ERP 9.

---

## 2. Installation & Running

### Option A: Download Pre-compiled Binary (Recommended)
1. Go to **[Releases](https://github.com/gops/TallyMCP/releases)** on GitHub.
2. Download `tally-mcp-windows-amd64.zip`.
3. Extract the folder onto your Windows PC (e.g. `C:\TallyMCP\`).
4. Double-click `tally-mcp.exe`.

### System Tray Quick Actions
Once launched, `tally-mcp.exe` sits quietly in your Windows Notification Area (bottom-right near the clock):

| Tray Menu Option | What It Does |
|---|---|
| **🟢 Tally MCP: Online (Port 8080)** | Shows connection status and active bound port. |
| **🌐 Open Web Dashboard** | Opens the server status and health page in your browser. |
| **⚙️ Open config.yaml** | Opens your configuration file in Notepad for quick editing. |
| **🛡️ Allow through Windows Firewall** | Adds firewall rule allowing other LAN devices to connect. |
| **🚀 Start with Windows** | Configures Tally MCP to launch automatically when Windows boots. |
| **🔄 Check for Updates** | Checks GitHub Releases for a newer version. |
| **🚪 Exit** | Shuts down the Tally MCP server. |

> 💡 **Headless / Server Mode**: If you are running on a server without a GUI or prefer running from the command line, launch with `tally-mcp.exe --no-tray`.

---

## 3. Connecting Your AI Assistant

### A. Cursor & Windsurf (Streamable HTTP)
Add Tally MCP to your AI editor settings (`.cursor/mcp.json` or MCP settings pane):

```json
{
  "mcpServers": {
    "tally": {
      "url": "http://192.168.1.50:8080/mcp"
    }
  }
}
```
*(Replace `192.168.1.50` with the local IP address of the Windows computer running Tally MCP. If running on the same machine, use `localhost`).*

If you enabled security authentication in `config.yaml`:
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

---

### B. Claude Desktop (SSE)
Add Tally MCP to your Claude Desktop configuration file:
- **Windows**: `%APPDATA%\Claude\claude_desktop_config.json`
- **macOS**: `~/Library/Application Support/Claude/claude_desktop_config.json`

```json
{
  "mcpServers": {
    "tally": {
      "url": "http://192.168.1.50:8080/sse"
    }
  }
}
```

If you enabled security authentication in `config.yaml`:
```json
{
  "mcpServers": {
    "tally": {
      "url": "http://192.168.1.50:8080/sse?token=your-secret-api-key"
    }
  }
}
```

Restart Claude Desktop, and you will see the hammer icon with Tally's accounting tools ready to use!

---

## 4. Available AI Tools Reference

When connected, your AI assistant can automatically call these tools to fulfill your requests:

| Tool Name | What Your AI Uses It For | Example Prompt |
|---|---|---|
| `tally_status` | Verifies Tally connection health and open companies | *"Is Tally currently open and connected?"* |
| `tally_list_companies` | Lists all active companies loaded in Tally | *"Which companies are open in Tally right now?"* |
| `tally_get_financial_summary` | Provides a quick KPI overview (Cash, Bank, Debtors, Creditors, Revenue, Profit) in < 350 tokens | *"Give me a high-level summary of our financials."* |
| `tally_list_ledgers` | Searches and lists accounts (customers, vendors, banks, expenses) with balances | *"List all Sundry Debtors located in Maharashtra."* |
| `tally_get_ledger` | Retrieves complete ledger details (address, credit limits, GST numbers, balance) | *"Show full contact and account info for ABC Corp."* |
| `tally_list_vouchers` | Searches transactions by date range and voucher type (Sales, Purchase, Payment, Receipt) | *"Show all sales vouchers entered in October 2026."* |
| `tally_get_voucher` | Full transaction drilldown (all debit/credit legs, inventory items, batch numbers, tax breakdown) | *"Give me the full breakdown of Invoice #INV-1042."* |
| `tally_get_stock_summary` | Checks inventory quantities, units, and stock valuations | *"What is our available stock for 100mm Pipes?"* |
| `tally_get_trial_balance` | Retrieves the Trial Balance report with debits and credits | *"Fetch the current Trial Balance statement."* |
| `tally_get_profit_and_loss` | Retrieves the Profit & Loss statement (Revenue, Gross Profit, Expenses, Net Profit) | *"What is our net profit for the current financial year?"* |
| `tally_get_balance_sheet` | Retrieves the Balance Sheet (Capital, Liabilities, Assets, Loans) | *"Show our current Balance Sheet as of today."* |
| `tally_get_outstanding_receivables` | Generates the aging schedule of pending customer bills (who owes us money) | *"Show overdue bills receivable older than 30 days."* |
| `tally_get_outstanding_payables` | Generates the aging schedule of pending vendor bills (who we owe money to) | *"What payments are due to our vendors this week?"* |
| `tally_get_daybook` | Shows all accounting transactions entered on a specific day | *"Show all Day Book entries for yesterday."* |
| `tally_generate_pdf` | Instructs Tally to export any financial statement or invoice to PDF and returns a download link | *"Export a PDF invoice for voucher #INV-1042."* |
| `tally_create_ledger` | Creates a new account ledger in Tally | *"Create a new Sundry Debtor named Global Logistics."* |
| `tally_execute_xml` | Runs custom raw XML / TDL queries against Tally | *(For power users and custom TDL scripts)* |

---

## 5. Configuration (`config.yaml`)

On its first run, `tally-mcp.exe` generates a default `config.yaml` in its folder:

```yaml
server:
  # Network address to listen on ("0.0.0.0:8080" allows connections across your LAN)
  listen_address: "0.0.0.0:8080"
  transport: "sse"

tally:
  # Address where Tally is running (default port is 9000)
  url: "http://localhost:9000"
  timeout_seconds: 30
  # Target company name (leave empty to use whichever company is open in Tally)
  default_company: ""
  # Maximum incoming queries to buffer when Tally is busy
  max_queue_size: 100
  # Cooldown throttle in milliseconds between queries (protects Tally from freezing)
  throttle_ms: 15
  # In-memory cache duration for ledger and stock names
  cache_ttl_seconds: 30

auth:
  # Set to true if you want to require a password/token from AI clients
  enabled: false
  # Your secret API key
  api_key: ""

updater:
  # GitHub repository for release updates
  github_repo: "gops/TallyMCP"
  # Check for updates on startup
  check_on_startup: true

log:
  level: "info"
```

You can also override settings via environment variables:
- `TALLY_URL`: e.g. `http://localhost:9000`
- `TALLY_MCP_LISTEN`: e.g. `0.0.0.0:8080`
- `TALLY_MCP_API_KEY`: e.g. `your-secret-key`

---

## 6. Command-Line Options

While most users interact via the System Tray icon, you can also use CLI flags in PowerShell or Command Prompt:

```cmd
tally-mcp.exe [flags]
```

| Flag / Command | Description |
|---|---|
| `--no-tray` | Run in headless console mode (ideal for servers or Windows services) |
| `--config <path>` | Specify a custom path to `config.yaml` |
| `--check-update` | Check GitHub Releases for available updates and exit |
| `--update` | Download and apply the latest release binary in-place |
| `--startup enable` | Register Tally MCP to launch automatically on Windows boot |
| `--startup disable` | Remove automatic launch on Windows boot |
| `--startup status` | Check whether autostart is currently enabled |
| `--firewall allow` | Add Windows Defender Firewall rule for port range `8080-8090` |
| `--firewall remove` | Remove Tally MCP firewall rule |
| `--firewall status` | Check if the firewall rule is currently active |
| `--version` | Display current version and build information |

---

## 7. Troubleshooting & FAQs

### Q: My AI assistant says "Connection refused" or cannot reach the server.
1. **Check if Tally MCP is running**: Look for the icon in your Windows Notification Area.
2. **Verify Windows Firewall**: Right-click the tray icon and ensure **🛡️ Allow through Windows Firewall** is checked, or run `tally-mcp.exe --firewall allow`.
3. **Verify IP Address**: Make sure your AI assistant configuration uses the IP address of the Windows PC running Tally MCP, not `localhost` (unless running on the same PC).
4. **Port In Use**: If port `8080` was busy, check the tray icon menu to see if it bound to `8081` or another port.

### Q: `tally_status` returns "Tally connection failed".
1. **Is Tally running?** Make sure Tally ERP 9 or TallyPrime is open on the host PC.
2. **Is Tally's HTTP Server enabled?** In TallyPrime, press `F1 > Settings > Connectivity` and make sure it is set to `Both` (or `Server`) on Port `9000`.
3. **Is a Company loaded?** Open at least one company in Tally.

### Q: Can multiple team members connect their AI assistants at the same time?
Yes! Tally MCP safely serializes incoming requests with an internal queue and cooldown buffer, ensuring multiple AI assistants across your office can query Tally without crashing Tally's engine.

---

## Technical Details & Architecture

Interested in internal design, the single-thread query serialization engine, XML/TDL envelopes, token-reduction strategies, or compiling from source?

👉 **Read the full [Architecture & Technical Reference (ARCHITECTURE.md)](./ARCHITECTURE.md)**.

---

## Disclaimer

This is AI-generated code and can have errors or mistakes. Use at your own risk. It is provided "as is" without warranty of any kind. Always test queries, actions, and reports against a test or backup Tally environment before deploying or using it with production financial books.

---

## License

This project is licensed under the [MIT License](./LICENSE) - free for personal, commercial, and enterprise use.
