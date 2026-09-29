package admin

import (
	"net/http"

	"github.com/yashok111/mocker/internal/apidesign"
	"github.com/yashok111/mocker/internal/httpx"
	"github.com/yashok111/mocker/internal/jsonx"
)

func (s *Server) handleAnalyzeAPIDesignImpact(w http.ResponseWriter, r *http.Request) {
	id, ok := s.designRequest(w, r)
	if !ok {
		return
	}
	// Raw targets distinguish an absent property from explicit JSON null.
	var body struct {
		FromRevisionID int64            `json:"fromRevisionId"`
		ToRevisionID   jsonx.RawMessage `json:"toRevisionId"`
		Document       jsonx.RawMessage `json:"document"`
	}
	if !s.designBody(w, r, &body) {
		return
	}
	if (len(body.Document) == 0) == (len(body.ToRevisionID) == 0) {
		httpx.Err(w, 400, "design_invalid", "Укажите ровно одно из document или toRevisionId")
		return
	}
	in := apidesign.ImpactInput{FromRevisionID: body.FromRevisionID}
	if len(body.Document) != 0 {
		if err := jsonx.Unmarshal(body.Document, &in.Document); err != nil || in.Document == nil {
			httpx.Err(w, 400, "design_invalid", "document должен быть строкой JSON")
			return
		}
	} else if err := jsonx.Unmarshal(body.ToRevisionID, &in.ToRevisionID); err != nil || in.ToRevisionID == nil {
		httpx.Err(w, 400, "design_invalid", "toRevisionId должен быть положительным целым числом")
		return
	}
	report, pair, err := s.designsRepo.AnalyzeImpactWithDocuments(r.Context(), id, in)
	if err != nil {
		s.designError(w, err)
		return
	}
	scenarios, err := s.designScenariosRepo.ImpactUsages(r.Context(), *report, pair)
	if err != nil {
		s.designScenarioError(w, err)
		return
	}
	report.Affected = append(report.Affected, scenarios.Affected...)
	report.Evidence = append(report.Evidence, scenarios.Evidence...)
	report.Diagnostics = append(report.Diagnostics, scenarios.Diagnostics...)
	report.FieldImpacts = append(report.FieldImpacts, scenarios.FieldImpacts...)
	report.Coverage.ScenariosScanned = scenarios.Coverage.ScenariosScanned
	report.Coverage.FieldUsagesChecked = scenarios.Coverage.FieldUsagesChecked
	report.Coverage.TruncatedReasons = append(report.Coverage.TruncatedReasons, scenarios.Coverage.TruncatedReasons...)
	report.Complete = report.Complete && scenarios.Complete
	if err := apidesign.FinalizeImpactReport(r.Context(), report); err != nil {
		s.designError(w, err)
		return
	}
	httpx.JSON(w, http.StatusOK, report)
}
