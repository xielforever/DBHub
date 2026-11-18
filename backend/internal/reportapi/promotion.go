package reportapi

import (
	"encoding/json"
	"net/http"
	"strings"

	"github.com/user/dbhub/internal/db"
	"github.com/user/dbhub/internal/httpx"
)

// Promote copies only a frozen, approved definition into a destination draft.
// The destination needs its own execution account, audience and independent review.
func (h *ManagedHandler) Promote(w http.ResponseWriter, r *http.Request) {
	id, ok := reportID(w, r)
	if !ok {
		return
	}
	var in struct {
		RequestID    string `json:"request_id"`
		ReviewID     int64  `json:"review_id"`
		ConnectionID int64  `json:"target_connection_id"`
		Name         string `json:"name"`
		ClearDrill   bool   `json:"clear_drillthrough"`
	}
	if err := httpx.DecodeJSON(w, r, &in); err != nil {
		httpx.Fail(w, err)
		return
	}
	in.RequestID = strings.ToLower(in.RequestID)
	if !requestKey(in.RequestID) || in.ReviewID < 1 || in.ConnectionID < 1 {
		httpx.Fail(w, httpx.BadRequest("迁移编号、获批版本和目标个人连接必填"))
		return
	}
	source, err := h.repo.ApprovedVersion(r.Context(), id, in.ReviewID)
	if err != nil {
		ManagedError(w, err)
		return
	}
	original := source.Definition
	var config map[string]json.RawMessage
	if err = json.Unmarshal(original.ChartConfig, &config); err != nil {
		ManagedError(w, err)
		return
	}
	if _, exists := config["drillthrough"]; exists {
		if !in.ClearDrill {
			httpx.Fail(w, httpx.BadRequest("跨环境下钻目标不能直接沿用，请明确同意移除，并在目标草稿重新配置"))
			return
		}
		delete(config, "drillthrough")
	}
	chart, _ := json.Marshal(config)
	definition, err := h.definition(r, managedDraftRequest{Name: strings.TrimSpace(in.Name), Description: original.Description, ConnectionID: in.ConnectionID, SQL: original.SQL, ChartType: original.ChartType, ChartConfig: chart, DataContract: original.DataContract, Parameters: original.Parameters, ExportPolicy: original.ExportPolicy})
	if err != nil {
		ManagedError(w, err)
		return
	}
	definition.Promotion = &db.ReportPromotion{ReportID: id, VersionID: source.ID, Version: source.Number, ReviewID: source.ReviewID, ContentHash: source.ContentHash, DefinitionID: original.DefinitionID}
	result, err := h.repo.SaveDraft(r.Context(), in.RequestID, 0, 0, definition)
	if err == nil {
		bindManaged(r.Context(), result.ID, result.Version)
	}
	commandResponse(w, result, err)
}
