# Tally MCP — Talk to Your Tally Books in Plain English

[![License: MIT](https://img.shields.io/badge/License-MIT-blue.svg)](https://opensource.org/licenses/MIT)
[![Release](https://img.shields.io/github/v/release/gops/TallyMCP)](https://github.com/gops/TallyMCP/releases)
[![Platform: Windows](https://img.shields.io/badge/Platform-Windows%20%7C%20LAN-0078D6)](https://github.com/gops/TallyMCP/releases)

**Turn Tally ERP 9 & TallyPrime into an AI-ready financial assistant in under 2 minutes.**  
Connect your live accounting data to **Claude Desktop**, **Cursor**, **Windsurf**, or any MCP-compatible AI—right over your office Wi-Fi or local network.

Ask questions in plain English, spot overdue invoices, check stock valuations, and generate shareable PDF reports on demand—without exporting a single Excel sheet.

> [!WARNING]
> **Disclaimer**: This is AI-generated software and may contain errors. Always verify figures and test against a backup company before relying on it for production accounting decisions.

---

## Why Teams Love Tally MCP

- 💬 **Instant Answers, Zero Exports** — Skip manual Tally navigation and CSV exports. Ask for your Profit & Loss, Balance Sheet, Trial Balance, or party statements directly in chat.
- 🌐 **Works Across Your Office LAN** — Run Tally on one Windows PC and query it safely from any Mac, Windows laptop, or workstation on your network.
- 🛡️ **Safe for Production Books** — Enable **Read-Only Mode** (`--read-only`) so AI assistants can analyze and report without ever creating or modifying a single ledger or voucher.
- ⚡ **Protects Tally from Freezing** — Built-in request queuing and smart caching keep Tally responsive even when multiple team members ask questions at once.
- 🖥️ **Silent Windows Tray App** — Lives neatly in your Windows System Tray with **1-click Tally launch**, **1-click Windows Firewall setup**, **Start with Windows**, and **automatic updates**.
- 📄 **Instant PDF Reports** — Generate clean, multi-page PDF statements and voucher summaries with a direct download link for your team or clients.
- 💸 **100% Free & Open Source** — MIT-licensed, zero cloud lock-in, and your financial data never leaves your local network unless you send it to your chosen AI client.

---

## What Can You Ask?

### For Business Owners & CFOs
- *"Give me a 30-second financial health summary of the company."*
- *"What is our net profit this financial year compared to major expense heads?"*
- *"Show our current Cash & Bank balance and working capital position."*

### For Accounts & Collections
- *"Who are our top 5 overdue customers, and how much do they owe?"*
- *"Show the ledger statement for Acme Corp from April to today."*
- *"Check if Roquette's ₹30,090 invoice has been entered."*

### For Sales, Inventory & Audit
- *"Show all sales vouchers this month above ₹50,000."*
- *"What is our closing stock quantity and valuation for 100mm Pipes?"*
- *"Generate a PDF of today's Balance Sheet and give me the download link."*

---

## Get Started in 3 Steps

### Step 1: Turn On Tally's Built-In Connector
- **TallyPrime**: Press `F1` (Help) → **Settings** → **Connectivity** → Set **TallyPrime is acting as** to `Both` (Port `9000`) → Restart TallyPrime and open your company.
- **Tally ERP 9**: Press `F12` (Configure) → **Advanced Configuration** → Set **Tally is acting as** to `Both` (Port `9000`) → Restart Tally ERP 9.

### Step 2: Download & Launch Tally MCP
1. Download the latest `tally-mcp-windows-amd64.zip` from **[GitHub Releases](https://github.com/gops/TallyMCP/releases)**.
2. Extract it to a folder (e.g. `C:\TallyMCP\`) and double-click **`tally-mcp.exe`**.
3. Right-click the **Tally MCP icon** in your Windows System Tray (near the clock) to:
   - Click **🛡️ Allow through Windows Firewall** (so other computers on your office Wi-Fi can connect).
   - Click **🚀 Start with Windows** (optional, for hands-free startup).

### Step 3: Connect Your AI Assistant

Replace `192.168.1.50` with your Tally PC's local IP (or `localhost` if on the same PC):

**Cursor / Windsurf** (`.cursor/mcp.json`):
```json
{
  "mcpServers": {
    "tally": {
      "url": "http://192.168.1.50:8080/mcp"
    }
  }
}
```

**Claude Desktop** (`claude_desktop_config.json`):
```json
{
  "mcpServers": {
    "tally": {
      "url": "http://192.168.1.50:8080/sse"
    }
  }
}
```

> 🔐 **Locking Down Access?** Enable `auth.enabled: true` in `config.yaml` and pass `"headers": { "Authorization": "Bearer your-secret-key" }` in your client config. Pair it with `server.read_only: true` for maximum peace of mind.

---

## Built-In Capabilities at a Glance

| Area | What's Included |
|---|---|
| **Financial Statements** | Financial Health Snapshot, Profit & Loss, Balance Sheet, Trial Balance, Day Book |
| **Parties & Receivables** | 360° Party Overview, Ledger Statements, Bills Receivable & Payable Aging |
| **Vouchers & Inventory** | Smart Voucher Search (by party, amount, date, or text), Full Voucher Drilldown, Stock Summary |
| **Documents & Automation** | Multi-page PDF Export (`/files/`), Auto-Launch Tally, Built-in Accounting Rules & Audit Prompts |

---

## Frequently Asked Questions

**Does my accounting data go to any third-party cloud server?**  
No. Tally MCP runs 100% locally on your Windows PC and communicates only over your local network with the AI client you connect to it.

**Can multiple team members use it at the same time?**  
Yes. Incoming requests are queued and paced automatically so multiple people can query Tally simultaneously without overloading it.

**Can I prevent the AI from editing or creating entries in Tally?**  
Yes. Set `read_only: true` in `config.yaml` (or launch with `--read-only`) to block all write operations.

**What if I want technical specs, CLI flags, or source build instructions?**  
See **[ARCHITECTURE.md](./ARCHITECTURE.md)** for the full technical reference, `config.yaml` options, CLI flags, XML/TDL architecture, and developer build guide.

---

## License

Free for personal, commercial, and enterprise use under the [MIT License](./LICENSE).
