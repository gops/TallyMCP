package tally

import (
	"encoding/json"
	"log"
	"slices"
	"time"
)

// AuditEntry records a structured audit trail event for any mutating Tally operation or dry-run preview.
type AuditEntry struct {
	Timestamp         string   `json:"timestamp"`
	Company           string   `json:"company,omitempty"`
	Operation         string   `json:"operation"`
	DryRun            bool     `json:"dry_run"`
	Status            string   `json:"status"` // "preview", "applied", "rejected"
	AffectedMasterIDs []string `json:"affected_master_ids,omitempty"`
	AffectedLedgers   []string `json:"affected_ledgers,omitempty"`
	Before            any      `json:"before,omitempty"`
	After             any      `json:"after,omitempty"`
	Error             string   `json:"error,omitempty"`
}

const maxRetainedAuditEntries = 200

func (c *Client) emitAudit(entry AuditEntry) {
	if entry.Timestamp == "" {
		entry.Timestamp = time.Now().UTC().Format(time.RFC3339)
	}
	if entry.Company == "" {
		entry.Company = c.defaultCompany
	}

	if b, err := json.Marshal(entry); err == nil {
		log.Printf("[AUDIT] %s", string(b))
	}

	c.auditMu.Lock()
	if len(c.auditLogs) >= maxRetainedAuditEntries {
		c.auditLogs = append(c.auditLogs[1:], entry)
	} else {
		c.auditLogs = append(c.auditLogs, entry)
	}
	c.auditMu.Unlock()
}

// AuditLogs returns a copy of recent structured audit log entries recorded by this client.
func (c *Client) AuditLogs() []AuditEntry {
	c.auditMu.Lock()
	defer c.auditMu.Unlock()
	return slices.Clone(c.auditLogs)
}
