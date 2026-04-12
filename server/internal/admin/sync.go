package admin

import (
	"database/sql"
	"encoding/json"
	"log"
	"net/http"
	"time"

	"proxyness/server/internal/db"
)

// Wire types for POST /api/sync.

type syncRequest struct {
	LastSyncAt int64    `json:"last_sync_at"`
	Ops        []syncOp `json:"ops"`
}

type syncOp struct {
	Op      string   `json:"op"` // "add" | "remove" | "enable" | "disable" | "add_domain"
	LocalID *int     `json:"local_id,omitempty"`
	SiteID  int      `json:"site_id,omitempty"`
	Site    *siteDTO `json:"site,omitempty"`
	Domain  string   `json:"domain,omitempty"`
	At      int64    `json:"at"`
}

type siteDTO struct {
	PrimaryDomain string `json:"primary_domain"`
	Label         string `json:"label"`
}

type syncResponse struct {
	OpResults  []opResult    `json:"op_results"`
	MySites    []db.UserSite `json:"my_sites"`
	ServerTime int64         `json:"server_time"`
}

type opResult struct {
	LocalID *int   `json:"local_id,omitempty"`
	SiteID  int    `json:"site_id,omitempty"`
	Status  string `json:"status"` // "ok" | "error" | "invalid" | "stale"
	Deduped bool   `json:"deduped,omitempty"`
	Message string `json:"message,omitempty"`
}

func (h *Handler) handleSync(w http.ResponseWriter, r *http.Request) {
	userID, ok := UserIDFromContext(r.Context())
	if !ok {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}

	var req syncRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid body", http.StatusBadRequest)
		return
	}

	// One transaction for all ops.
	tx, err := h.db.SQL().Begin()
	if err != nil {
		http.Error(w, "db error", http.StatusInternalServerError)
		log.Printf("[sync] begin: %v", err)
		return
	}
	defer tx.Rollback()

	results := make([]opResult, 0, len(req.Ops))
	for _, op := range req.Ops {
		results = append(results, h.applyOp(tx, userID, op))
	}

	if err := tx.Commit(); err != nil {
		http.Error(w, "db error", http.StatusInternalServerError)
		log.Printf("[sync] commit: %v", err)
		return
	}

	mySites, err := h.db.GetMySites(userID)
	if err != nil {
		http.Error(w, "db error", http.StatusInternalServerError)
		log.Printf("[sync] GetMySites: %v", err)
		return
	}

	writeJSON(w, http.StatusOK, syncResponse{
		OpResults:  results,
		MySites:    mySites,
		ServerTime: time.Now().Unix(),
	})
}

func (h *Handler) applyOp(tx *sql.Tx, userID int, op syncOp) opResult {
	res := opResult{LocalID: op.LocalID, SiteID: op.SiteID}

	switch op.Op {
	case "add":
		if op.Site == nil {
			res.Status = "invalid"
			res.Message = "missing site"
			return res
		}
		r, err := h.db.ApplyAddOp(tx, userID, op.Site.PrimaryDomain, op.Site.Label, op.At)
		if err != nil {
			res.Status = "invalid"
			res.Message = err.Error()
			return res
		}
		res.Status = "ok"
		res.SiteID = r.SiteID
		res.Deduped = r.Deduped

	case "remove":
		if op.SiteID == 0 {
			res.Status = "invalid"
			res.Message = "missing site_id"
			return res
		}
		if err := h.db.ApplyRemoveOp(tx, userID, op.SiteID); err != nil {
			res.Status = "error"
			res.Message = err.Error()
			return res
		}
		res.Status = "ok"

	case "enable", "disable":
		enabled := op.Op == "enable"
		if op.SiteID == 0 {
			res.Status = "invalid"
			res.Message = "missing site_id"
			return res
		}
		switch h.db.ApplyToggleOp(tx, userID, op.SiteID, enabled, op.At) {
		case db.ToggleOK:
			res.Status = "ok"
		case db.ToggleStale:
			res.Status = "stale"
		case db.ToggleNotFound:
			res.Status = "error"
			res.Message = "site not found"
		}

	case "add_domain":
		if op.SiteID == 0 || op.Domain == "" {
			res.Status = "invalid"
			res.Message = "missing site_id or domain"
			return res
		}
		r, err := h.db.ApplyAddDomainOp(tx, userID, op.SiteID, op.Domain, op.At)
		if err != nil {
			res.Status = "error"
			res.Message = err.Error()
			return res
		}
		res.Status = "ok"
		res.SiteID = op.SiteID
		res.Deduped = r.Deduped

	default:
		res.Status = "invalid"
		res.Message = "unknown op: " + op.Op
	}
	return res
}
