package api

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/lutzifer/burpsuite-clone/internal/intruder"
)

type IntruderService interface {
	Preview(context.Context, intruder.Config) (intruder.Preview, error)
	Create(context.Context, intruder.Draft) (intruder.Job, error)
	Update(context.Context, string, int64, intruder.Config) (intruder.Job, error)
	Delete(context.Context, string) error
	List(context.Context) ([]intruder.JobSummary, error)
	Get(context.Context, string) (intruder.Job, error)
	Start(context.Context, string, int64) (intruder.Job, error)
	Pause(context.Context, string, int64) (intruder.Job, error)
	Resume(context.Context, string, int64) (intruder.Job, error)
	Abort(context.Context, string, int64) (intruder.Job, error)
	ListResults(context.Context, string, intruder.ResultQuery) (intruder.ResultPage, error)
	GetResult(context.Context, string, int64) (intruder.Result, error)
	SetBaseline(context.Context, string, int64, int64) (intruder.Job, error)
}

type intruderConfigDTO struct {
	Attack   intruder.AttackType `json:"attack"`
	Template struct {
		Method string `json:"method"`
		URL    string `json:"url"`
		Raw    []byte `json:"raw"`
	} `json:"template"`
	Positions     []intruder.Position   `json:"positions"`
	PayloadSets   []intruder.PayloadSet `json:"payloadSets"`
	RequestLimit  int                   `json:"requestLimit"`
	Concurrency   int                   `json:"concurrency"`
	RatePerSecond float64               `json:"ratePerSecond"`
	TimeoutMS     int64                 `json:"timeoutMs"`
}

func (d intruderConfigDTO) domain() intruder.Config {
	return intruder.Config{
		Attack:    d.Attack,
		Template:  intruder.Template{Method: d.Template.Method, URL: d.Template.URL, Raw: d.Template.Raw},
		Positions: d.Positions, PayloadSets: d.PayloadSets, RequestLimit: d.RequestLimit,
		Concurrency: d.Concurrency, RatePerSecond: d.RatePerSecond,
		Timeout: time.Duration(d.TimeoutMS) * time.Millisecond,
	}
}

func intruderConfigOutput(c intruder.Config) intruderConfigDTO {
	return intruderConfigDTO{Attack: c.Attack, Template: struct {
		Method string `json:"method"`
		URL    string `json:"url"`
		Raw    []byte `json:"raw"`
	}{Method: c.Template.Method, URL: c.Template.URL, Raw: c.Template.Raw}, Positions: c.Positions,
		PayloadSets: c.PayloadSets, RequestLimit: c.RequestLimit, Concurrency: c.Concurrency,
		RatePerSecond: c.RatePerSecond, TimeoutMS: c.Timeout.Milliseconds()}
}

func intruderJobOutput(job intruder.Job) interface{} {
	return struct {
		ID               string            `json:"id"`
		Config           intruderConfigDTO `json:"config"`
		State            intruder.State    `json:"state"`
		StateReason      string            `json:"stateReason"`
		Revision         int64             `json:"revision"`
		TotalRequests    int64             `json:"totalRequests"`
		NextSequence     int64             `json:"nextSequence"`
		CompletedCount   int64             `json:"completedCount"`
		ErrorCount       int64             `json:"errorCount"`
		BaselineSequence *int64            `json:"baselineSequence"`
	}{job.ID, intruderConfigOutput(job.Config), job.State, job.StateReason, job.Revision,
		job.TotalRequests, job.NextSequence, job.CompletedCount, job.ErrorCount, job.BaselineSequence}
}

func (s *Server) handleIntruderPreview(w http.ResponseWriter, r *http.Request) {
	if !s.intruderReady(w) {
		return
	}
	var body struct {
		Config intruderConfigDTO `json:"config"`
	}
	if !s.decodeIntruder(w, r, &body) {
		return
	}
	if !validIntruderTimeout(w, body.Config.TimeoutMS) {
		return
	}
	preview, err := s.cfg.Intruder.Preview(r.Context(), body.Config.domain())
	if err != nil {
		intruderError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, preview)
}

func (s *Server) handleIntruderBaseline(w http.ResponseWriter, r *http.Request) {
	if !s.intruderReady(w) {
		return
	}
	id, ok := intruderID(w, r)
	if !ok {
		return
	}
	var body struct {
		Revision int64 `json:"revision"`
		Sequence int64 `json:"sequence"`
	}
	if !s.decodeIntruder(w, r, &body) {
		return
	}
	if body.Sequence < 0 {
		intruderError(w, &intruder.FieldError{Field: "sequence", Code: "range"})
		return
	}
	job, err := s.cfg.Intruder.SetBaseline(r.Context(), id, body.Revision, body.Sequence)
	if err != nil {
		intruderError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, intruderJobOutput(job))
}

func (s *Server) intruderReady(w http.ResponseWriter) bool {
	w.Header().Set("Cache-Control", "no-store")
	if s.cfg.Intruder == nil {
		http.Error(w, "Intruder unavailable", http.StatusServiceUnavailable)
		return false
	}
	return true
}

func intruderID(w http.ResponseWriter, r *http.Request) (string, bool) {
	id := r.PathValue("id")
	if len(id) != 32 {
		http.Error(w, "invalid job ID", http.StatusBadRequest)
		return "", false
	}
	for _, c := range id {
		if c < '0' || c > '9' {
			if c < 'a' || c > 'f' {
				http.Error(w, "invalid job ID", http.StatusBadRequest)
				return "", false
			}
		}
	}
	return id, true
}

func intruderError(w http.ResponseWriter, err error) {
	var field *intruder.FieldError
	switch {
	case errors.Is(err, sql.ErrNoRows):
		http.Error(w, "not found", http.StatusNotFound)
	case errors.Is(err, intruder.ErrScopeDenied):
		http.Error(w, "target outside scope", http.StatusForbidden)
	case errors.Is(err, intruder.ErrRevisionConflict), errors.Is(err, intruder.ErrStateConflict), errors.Is(err, intruder.ErrJobLimit):
		http.Error(w, "job conflict", http.StatusConflict)
	case errors.As(err, &field):
		writeJSON(w, http.StatusUnprocessableEntity, map[string]string{"field": field.Field, "code": field.Code})
	default:
		http.Error(w, "internal error", http.StatusInternalServerError)
	}
}

func (s *Server) decodeIntruder(w http.ResponseWriter, r *http.Request, value interface{}) bool {
	return decodeJSONLimit(w, r, value, 16<<20) == nil
}

func (s *Server) handleIntruderCreate(w http.ResponseWriter, r *http.Request) {
	if !s.intruderReady(w) {
		return
	}
	var body struct {
		Config       intruderConfigDTO `json:"config"`
		ScopeVersion int64             `json:"scopeVersion"`
	}
	if !s.decodeIntruder(w, r, &body) {
		return
	}
	if !validIntruderTimeout(w, body.Config.TimeoutMS) {
		return
	}
	job, err := s.cfg.Intruder.Create(r.Context(), intruder.Draft{Config: body.Config.domain(), ScopeVersion: body.ScopeVersion})
	if err != nil {
		intruderError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, intruderJobOutput(job))
}

func (s *Server) handleIntruderList(w http.ResponseWriter, r *http.Request) {
	if !s.intruderReady(w) {
		return
	}
	jobs, err := s.cfg.Intruder.List(r.Context())
	if err != nil {
		intruderError(w, err)
		return
	}
	if jobs == nil {
		jobs = []intruder.JobSummary{}
	}
	writeJSON(w, http.StatusOK, jobs)
}

func (s *Server) handleIntruderGet(w http.ResponseWriter, r *http.Request) {
	if !s.intruderReady(w) {
		return
	}
	id, ok := intruderID(w, r)
	if !ok {
		return
	}
	job, err := s.cfg.Intruder.Get(r.Context(), id)
	if err != nil {
		intruderError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, intruderJobOutput(job))
}

func (s *Server) handleIntruderUpdate(w http.ResponseWriter, r *http.Request) {
	if !s.intruderReady(w) {
		return
	}
	id, ok := intruderID(w, r)
	if !ok {
		return
	}
	var body struct {
		Revision int64             `json:"revision"`
		Config   intruderConfigDTO `json:"config"`
	}
	if !s.decodeIntruder(w, r, &body) {
		return
	}
	if !validIntruderTimeout(w, body.Config.TimeoutMS) {
		return
	}
	job, err := s.cfg.Intruder.Update(r.Context(), id, body.Revision, body.Config.domain())
	if err != nil {
		intruderError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, intruderJobOutput(job))
}

func (s *Server) handleIntruderDelete(w http.ResponseWriter, r *http.Request) {
	if !s.intruderReady(w) {
		return
	}
	id, ok := intruderID(w, r)
	if !ok {
		return
	}
	if err := s.cfg.Intruder.Delete(r.Context(), id); err != nil {
		intruderError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) intruderControl(w http.ResponseWriter, r *http.Request, action string) {
	if !s.intruderReady(w) {
		return
	}
	id, ok := intruderID(w, r)
	if !ok {
		return
	}
	var body struct {
		Revision int64 `json:"revision"`
	}
	if !s.decodeIntruder(w, r, &body) {
		return
	}
	var job intruder.Job
	var err error
	switch action {
	case "start":
		job, err = s.cfg.Intruder.Start(r.Context(), id, body.Revision)
	case "pause":
		job, err = s.cfg.Intruder.Pause(r.Context(), id, body.Revision)
	case "resume":
		job, err = s.cfg.Intruder.Resume(r.Context(), id, body.Revision)
	case "abort":
		job, err = s.cfg.Intruder.Abort(r.Context(), id, body.Revision)
	}
	if err != nil {
		intruderError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, intruderJobOutput(job))
}

func (s *Server) handleIntruderStart(w http.ResponseWriter, r *http.Request) {
	s.intruderControl(w, r, "start")
}
func (s *Server) handleIntruderPause(w http.ResponseWriter, r *http.Request) {
	s.intruderControl(w, r, "pause")
}
func (s *Server) handleIntruderResume(w http.ResponseWriter, r *http.Request) {
	s.intruderControl(w, r, "resume")
}
func (s *Server) handleIntruderAbort(w http.ResponseWriter, r *http.Request) {
	s.intruderControl(w, r, "abort")
}

func (s *Server) handleIntruderResults(w http.ResponseWriter, r *http.Request) {
	if !s.intruderReady(w) {
		return
	}
	id, ok := intruderID(w, r)
	if !ok {
		return
	}
	query := intruder.ResultQuery{Limit: 100}
	allowed := map[string]bool{"beforeSequence": true, "limit": true, "status": true, "errorCategory": true, "mimeType": true,
		"minSize": true, "maxSize": true, "minDurationMs": true, "maxDurationMs": true,
		"minSimilarity": true, "maxSimilarity": true, "payloadSearch": true}
	params := r.URL.Query()
	for key, values := range params {
		if !allowed[key] || len(values) != 1 || values[0] == "" {
			http.Error(w, "invalid query", 400)
			return
		}
	}
	for key, target := range map[string]*int{"limit": &query.Limit, "status": &query.Status} {
		if value := params.Get(key); value != "" {
			n, err := strconv.Atoi(value)
			if err != nil || n < 0 {
				http.Error(w, "invalid query", 400)
				return
			}
			*target = n
		}
	}
	for key, target := range map[string]*int64{"minSize": &query.MinSize, "maxSize": &query.MaxSize,
		"minDurationMs": &query.MinDurationMS, "maxDurationMs": &query.MaxDurationMS} {
		if value := params.Get(key); value != "" {
			n, err := strconv.ParseInt(value, 10, 64)
			if err != nil || n < 0 {
				http.Error(w, "invalid query", 400)
				return
			}
			*target = n
		}
	}
	for key, target := range map[string]*int{"minSimilarity": &query.MinSimilarity, "maxSimilarity": &query.MaxSimilarity} {
		if value := params.Get(key); value != "" {
			n, err := strconv.Atoi(value)
			if err != nil || n < 0 || n > 100 {
				http.Error(w, "invalid similarity", 400)
				return
			}
			*target = n
		}
	}
	if query.Limit < 1 || query.Limit > 100 {
		http.Error(w, "invalid limit", 400)
		return
	}
	if query.Status != 0 && (query.Status < 100 || query.Status > 599) ||
		query.MaxSize > 0 && query.MinSize > query.MaxSize ||
		query.MaxDurationMS > 0 && query.MinDurationMS > query.MaxDurationMS ||
		query.MaxSimilarity > 0 && query.MinSimilarity > query.MaxSimilarity {
		http.Error(w, "invalid filter range", 400)
		return
	}
	if value := params.Get("beforeSequence"); value != "" {
		n, err := strconv.ParseInt(value, 10, 64)
		if err != nil || n < 0 {
			http.Error(w, "invalid cursor", 400)
			return
		}
		query.BeforeSequence = &n
	}
	query.ErrorCategory = params.Get("errorCategory")
	query.MIMEType = params.Get("mimeType")
	query.PayloadSearch = params.Get("payloadSearch")
	if len(query.ErrorCategory) > 64 || len(query.MIMEType) > 256 || len(query.PayloadSearch) > 128 {
		http.Error(w, "filter too long", 400)
		return
	}
	page, err := s.cfg.Intruder.ListResults(r.Context(), id, query)
	if err != nil {
		intruderError(w, err)
		return
	}
	if page.Results == nil {
		page.Results = []intruder.Result{}
	}
	writeJSON(w, http.StatusOK, page)
}

func validIntruderTimeout(w http.ResponseWriter, timeoutMS int64) bool {
	if timeoutMS < 1000 || timeoutMS > 120000 {
		writeJSON(w, http.StatusUnprocessableEntity, map[string]string{"field": "timeoutMs", "code": "out_of_range"})
		return false
	}
	return true
}

func (s *Server) handleIntruderResult(w http.ResponseWriter, r *http.Request) {
	if !s.intruderReady(w) {
		return
	}
	id, ok := intruderID(w, r)
	if !ok {
		return
	}
	sequence, err := strconv.ParseInt(r.PathValue("sequence"), 10, 64)
	if err != nil || sequence < 0 {
		http.Error(w, "invalid sequence", 400)
		return
	}
	result, err := s.cfg.Intruder.GetResult(r.Context(), id, sequence)
	if err != nil {
		intruderError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, result)
}
