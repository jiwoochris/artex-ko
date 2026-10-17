package server

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/Autumn-27/artex/db"
)

const maxAssuranceRequestBytes = 1 << 20

func writeAssuranceError(w http.ResponseWriter, err error) {
	status := http.StatusInternalServerError
	switch {
	case errors.Is(err, db.ErrAssuranceInvalid):
		status = http.StatusBadRequest
	case errors.Is(err, db.ErrAssuranceNotFound):
		status = http.StatusNotFound
	case errors.Is(err, db.ErrAssuranceConflict):
		status = http.StatusConflict
	case errors.Is(err, db.ErrTaskArchiveState), errors.Is(err, db.ErrTaskArchiveQueued):
		status = http.StatusConflict
	}
	if status == http.StatusInternalServerError {
		// SQL diagnostics and connection details do not belong in API exports.
		writeErr(w, status, "Unable to load or save assurance data")
		return
	}
	writeErr(w, status, err.Error())
}

func decodeAssuranceRequest(w http.ResponseWriter, r *http.Request, value any) bool {
	r.Body = http.MaxBytesReader(w, r.Body, maxAssuranceRequestBytes)
	d := json.NewDecoder(r.Body)
	d.DisallowUnknownFields()
	if err := d.Decode(value); err != nil {
		var large *http.MaxBytesError
		if errors.As(err, &large) {
			writeErr(w, http.StatusRequestEntityTooLarge, "Assurance input exceeds 1 MiB")
		} else {
			writeErr(w, http.StatusBadRequest, "Invalid assurance input: "+err.Error())
		}
		return false
	}
	if err := d.Decode(new(any)); err != io.EOF {
		writeErr(w, http.StatusBadRequest, "Expected exactly one JSON document")
		return false
	}
	return true
}

func (s *Server) assuranceTask(w http.ResponseWriter, r *http.Request) (*db.DB, int64) {
	id, ok := pathInt(r, "id")
	if !ok || id <= 0 {
		writeErr(w, http.StatusBadRequest, "Invalid task ID")
		return nil, 0
	}
	return s.pg(w), id
}

func (s *Server) taskAssurance(w http.ResponseWriter, r *http.Request) {
	pg, id := s.assuranceTask(w, r)
	if pg == nil {
		return
	}
	workspace, err := pg.GetTaskAssurance(r.Context(), id)
	if err != nil {
		writeAssuranceError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, workspace)
}

func (s *Server) startAssuranceRetest(w http.ResponseWriter, r *http.Request) {
	pg, id := s.assuranceTask(w, r)
	if pg == nil {
		return
	}
	pid, ok := pathInt(r, "pid")
	if !ok || pid <= 0 {
		writeErr(w, http.StatusBadRequest, "Invalid plan ID")
		return
	}
	var input struct {
		Notes string `json:"notes"`
	}
	if !decodeAssuranceRequest(w, r, &input) {
		return
	}
	input.Notes = strings.TrimSpace(input.Notes)
	if utf8.RuneCountInString(input.Notes) > 4000 {
		writeErr(w, http.StatusBadRequest, errFindingRetestNotesTooLong)
		return
	}
	if !s.findingRetestReady(w, r) {
		return
	}
	retest, conv, created, err := pg.CreateAssuranceRetest(r.Context(), id, pid, r.PathValue("cid"), input.Notes)
	if err != nil {
		writeAssuranceError(w, err)
		return
	}
	s.respondFindingRetest(w, retest, conv, created)
}

func (s *Server) appendAssuranceSnapshot(w http.ResponseWriter, r *http.Request) {
	pg, id := s.assuranceTask(w, r)
	if pg == nil {
		return
	}
	var input db.AssuranceSnapshotInput
	if !decodeAssuranceRequest(w, r, &input) {
		return
	}
	result, err := pg.AppendAssuranceSnapshot(r.Context(), id, input)
	if err != nil {
		writeAssuranceError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, result)
}

func (s *Server) upsertAssuranceConclusion(w http.ResponseWriter, r *http.Request) {
	pg, id := s.assuranceTask(w, r)
	if pg == nil {
		return
	}
	var input db.AssuranceConclusionInput
	if !decodeAssuranceRequest(w, r, &input) {
		return
	}
	result, err := pg.UpsertAssuranceConclusion(r.Context(), id, r.PathValue("cid"), input)
	if err != nil {
		writeAssuranceError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func (s *Server) addAssuranceCoverage(w http.ResponseWriter, r *http.Request) {
	pg, id := s.assuranceTask(w, r)
	if pg == nil {
		return
	}
	var input db.AssuranceCoverageInput
	if !decodeAssuranceRequest(w, r, &input) {
		return
	}
	result, err := pg.AddCoverageCheck(r.Context(), id, input)
	if err != nil {
		writeAssuranceError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, result)
}

func (s *Server) recordAssuranceReview(w http.ResponseWriter, r *http.Request) {
	pg, id := s.assuranceTask(w, r)
	if pg == nil {
		return
	}
	var input db.AssuranceReviewInput
	if !decodeAssuranceRequest(w, r, &input) {
		return
	}
	// This is an authenticated operator assertion, never a model/runtime verdict.
	if input.Provenance != "" && input.Provenance != "operator" {
		writeErr(w, http.StatusBadRequest, "Review provenance must be operator")
		return
	}
	input.Provenance = "operator"
	result, err := pg.RecordAssuranceReview(r.Context(), id, r.PathValue("cid"), input)
	if err != nil {
		writeAssuranceError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, result)
}

func (s *Server) createAssurancePlan(w http.ResponseWriter, r *http.Request) {
	pg, id := s.assuranceTask(w, r)
	if pg == nil {
		return
	}
	var input db.AssurancePlanInput
	if !decodeAssuranceRequest(w, r, &input) {
		return
	}
	result, err := pg.CreateAssurancePlan(r.Context(), id, input)
	if err != nil {
		writeAssuranceError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, result)
}

func (s *Server) getAssurancePlan(w http.ResponseWriter, r *http.Request) {
	pg, id := s.assuranceTask(w, r)
	if pg == nil {
		return
	}
	pid, ok := pathInt(r, "pid")
	if !ok || pid <= 0 {
		writeErr(w, http.StatusBadRequest, "Invalid plan ID")
		return
	}
	result, err := pg.GetAssurancePlan(r.Context(), id, pid)
	if err != nil {
		writeAssuranceError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func (s *Server) exportTaskAssurance(w http.ResponseWriter, r *http.Request) {
	pg, id := s.assuranceTask(w, r)
	if pg == nil {
		return
	}
	workspace, err := pg.GetTaskAssuranceExport(r.Context(), id)
	if err != nil {
		writeAssuranceError(w, err)
		return
	}
	// Keep incomplete runs and unrepresented scope explicit. These are counts of
	// recorded conclusions, never a percentage of all possible security checks.
	out := struct {
		*db.TaskAssurance
		SchemaVersion int       `json:"schema_version"`
		ExportedAt    time.Time `json:"exported_at"`
		Complete      bool      `json:"complete"`
		CoverageBasis string    `json:"coverage_basis"`
	}{workspace, 1, time.Now().UTC(), false, "explicitly_recorded_conclusions_only"}
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="artex-task-%s-assurance.json"`, strconv.FormatInt(id, 10)))
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, out)
}
