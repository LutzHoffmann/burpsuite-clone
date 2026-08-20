package api

import (
	"database/sql"
	"errors"
	"math"
	"net/http"
	"strconv"
	"time"

	"github.com/lutzifer/burpsuite-clone/internal/scope"
	"github.com/lutzifer/burpsuite-clone/internal/store"
	"github.com/lutzifer/burpsuite-clone/internal/target"
)

type scopeUpdateDTO struct {
	Version int64        `json:"version"`
	Rules   []scope.Rule `json:"rules"`
}

type scopeStateDTO struct {
	Version int64          `json:"version"`
	Rules   []scopeRuleDTO `json:"rules"`
}

type scopeRuleDTO struct {
	ID          int64        `json:"id"`
	Enabled     bool         `json:"enabled"`
	Action      scope.Action `json:"action"`
	Scheme      string       `json:"scheme"`
	HostPattern string       `json:"hostPattern"`
	Port        int          `json:"port"`
	PathPrefix  string       `json:"pathPrefix"`
}

type targetTreeNodeDTO struct {
	ID            int64               `json:"id"`
	Scheme        string              `json:"scheme"`
	Host          string              `json:"host"`
	Port          int                 `json:"port"`
	Path          string              `json:"path"`
	Method        string              `json:"method"`
	InScope       bool                `json:"inScope"`
	Statuses      []int               `json:"statuses"`
	RequestMIMEs  []string            `json:"requestMimes"`
	ResponseMIMEs []string            `json:"responseMimes"`
	Count         int64               `json:"count"`
	LastSeen      time.Time           `json:"lastSeen"`
	Children      []targetTreeNodeDTO `json:"children"`
}

type targetEndpointDTO struct {
	ID               int64     `json:"id"`
	Scheme           string    `json:"scheme"`
	Host             string    `json:"host"`
	Port             int       `json:"port"`
	Path             string    `json:"path"`
	Method           string    `json:"method"`
	InScope          bool      `json:"inScope"`
	FirstSeen        time.Time `json:"firstSeen"`
	LastSeen         time.Time `json:"lastSeen"`
	Count            int64     `json:"count"`
	Statuses         []int     `json:"statuses"`
	RequestMIMEs     []string  `json:"requestMimes"`
	ResponseMIMEs    []string  `json:"responseMimes"`
	ParseDiagnostics []string  `json:"parseDiagnostics"`
	ErrorSeen        bool      `json:"errorSeen"`
	LatestExchangeID int64     `json:"latestExchangeId"`
}

type targetRequestRefDTO struct {
	ExchangeID int64     `json:"exchangeId"`
	StartedAt  time.Time `json:"startedAt"`
	Status     int       `json:"status"`
	Error      bool      `json:"error"`
}

type targetParameterDTO struct {
	Location  string    `json:"location"`
	Name      string    `json:"name"`
	ValueType string    `json:"valueType"`
	FirstSeen time.Time `json:"firstSeen"`
	LastSeen  time.Time `json:"lastSeen"`
	Count     int64     `json:"count"`
}

type rebuildStatusDTO struct {
	ID                 int64  `json:"id"`
	ScopeVersion       int64  `json:"scopeVersion"`
	ActiveScopeVersion int64  `json:"activeScopeVersion"`
	Status             string `json:"status"`
	Processed          int64  `json:"processed"`
	Total              int64  `json:"total"`
	Error              string `json:"error"`
}

func (s *Server) handleScopeRules(w http.ResponseWriter, r *http.Request) {
	if s.cfg.Target == nil {
		http.Error(w, "target service unavailable", http.StatusServiceUnavailable)
		return
	}
	state, err := s.cfg.Target.State(r.Context())
	if err != nil {
		http.Error(w, "load scope rules", http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusOK, toScopeStateDTO(state))
}

func (s *Server) handleScopeRulesUpdate(w http.ResponseWriter, r *http.Request) {
	if s.cfg.Target == nil {
		http.Error(w, "target service unavailable", http.StatusServiceUnavailable)
		return
	}
	var update scopeUpdateDTO
	if err := s.decodeJSON(w, r, &update); err != nil {
		return
	}
	if err := validateScopeUpdate(update); err != nil {
		http.Error(w, "invalid scope rules", http.StatusBadRequest)
		return
	}
	state, err := s.cfg.Target.ReplaceRules(r.Context(), update.Version, update.Rules)
	if err != nil {
		if errors.Is(err, store.ErrScopeVersionConflict) {
			http.Error(w, "scope version conflict", http.StatusConflict)
		} else {
			http.Error(w, "replace scope rules", http.StatusInternalServerError)
		}
		return
	}
	writeJSON(w, http.StatusOK, toScopeStateDTO(state))
}

func validateScopeUpdate(update scopeUpdateDTO) error {
	if update.Version < 0 || update.Version == math.MaxInt64 {
		return errors.New("invalid scope version")
	}
	for _, rule := range update.Rules {
		if rule.ID < 0 {
			return errors.New("invalid scope rule id")
		}
	}
	_, err := scope.Compile(update.Version+1, update.Rules)
	return err
}

func (s *Server) handleTargetTree(w http.ResponseWriter, r *http.Request) {
	if s.cfg.Target == nil {
		http.Error(w, "target service unavailable", http.StatusServiceUnavailable)
		return
	}
	tree, err := s.cfg.Target.Tree(r.Context())
	if err != nil {
		http.Error(w, "list target tree", http.StatusInternalServerError)
		return
	}
	dtos := make([]targetTreeNodeDTO, len(tree))
	for index := range tree {
		dtos[index] = toTargetTreeNodeDTO(tree[index])
	}
	writeJSON(w, http.StatusOK, dtos)
}

func (s *Server) handleTargetEndpoint(w http.ResponseWriter, r *http.Request) {
	if s.cfg.Target == nil {
		http.Error(w, "target service unavailable", http.StatusServiceUnavailable)
		return
	}
	id, ok := targetEndpointID(w, r)
	if !ok {
		return
	}
	endpoint, err := s.cfg.Target.Endpoint(r.Context(), id)
	if err != nil {
		writeTargetQueryError(w, err, "get target endpoint")
		return
	}
	writeJSON(w, http.StatusOK, toTargetEndpointDTO(endpoint))
}

func (s *Server) handleTargetRequests(w http.ResponseWriter, r *http.Request) {
	if s.cfg.Target == nil {
		http.Error(w, "target service unavailable", http.StatusServiceUnavailable)
		return
	}
	id, ok := targetEndpointID(w, r)
	if !ok {
		return
	}
	requests, err := s.cfg.Target.Requests(r.Context(), id)
	if err != nil {
		writeTargetQueryError(w, err, "list target requests")
		return
	}
	dtos := make([]targetRequestRefDTO, len(requests))
	for index, request := range requests {
		dtos[index] = targetRequestRefDTO{
			ExchangeID: request.ExchangeID, StartedAt: request.StartedAt, Status: request.Status, Error: request.Error,
		}
	}
	writeJSON(w, http.StatusOK, dtos)
}

func (s *Server) handleTargetParameters(w http.ResponseWriter, r *http.Request) {
	if s.cfg.Target == nil {
		http.Error(w, "target service unavailable", http.StatusServiceUnavailable)
		return
	}
	id, ok := targetEndpointID(w, r)
	if !ok {
		return
	}
	parameters, err := s.cfg.Target.Parameters(r.Context(), id)
	if err != nil {
		writeTargetQueryError(w, err, "list target parameters")
		return
	}
	dtos := make([]targetParameterDTO, len(parameters))
	for index, parameter := range parameters {
		dtos[index] = targetParameterDTO{
			Location: parameter.Location, Name: parameter.Name, ValueType: parameter.ValueType,
			FirstSeen: parameter.FirstSeen, LastSeen: parameter.LastSeen, Count: parameter.Count,
		}
	}
	writeJSON(w, http.StatusOK, dtos)
}

func (s *Server) handleTargetRebuild(w http.ResponseWriter, r *http.Request) {
	if s.cfg.Target == nil {
		http.Error(w, "target service unavailable", http.StatusServiceUnavailable)
		return
	}
	status, err := s.cfg.Target.RebuildStatus(r.Context())
	if err != nil {
		http.Error(w, "load target rebuild status", http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusOK, toRebuildStatusDTO(status))
}

func (s *Server) handleTargetRebuildRetry(w http.ResponseWriter, r *http.Request) {
	if s.cfg.Target == nil {
		http.Error(w, "target service unavailable", http.StatusServiceUnavailable)
		return
	}
	var request *struct{}
	if err := s.decodeJSON(w, r, &request); err != nil {
		return
	}
	if request == nil {
		http.Error(w, "invalid JSON body", http.StatusBadRequest)
		return
	}
	if err := s.cfg.Target.RetryRebuild(r.Context()); err != nil {
		if errors.Is(err, target.ErrRebuildInProgress) {
			http.Error(w, "target rebuild already in progress", http.StatusConflict)
		} else {
			http.Error(w, "retry target rebuild", http.StatusInternalServerError)
		}
		return
	}
	status, err := s.cfg.Target.RebuildStatus(r.Context())
	if err != nil {
		http.Error(w, "load target rebuild status", http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusAccepted, toRebuildStatusDTO(status))
}

func targetEndpointID(w http.ResponseWriter, r *http.Request) (int64, bool) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id < 1 {
		http.Error(w, "invalid target endpoint id", http.StatusBadRequest)
		return 0, false
	}
	return id, true
}

func writeTargetQueryError(w http.ResponseWriter, err error, operation string) {
	if errors.Is(err, sql.ErrNoRows) {
		http.Error(w, "target endpoint not found", http.StatusNotFound)
		return
	}
	http.Error(w, operation, http.StatusInternalServerError)
}

func toScopeStateDTO(state scope.State) scopeStateDTO {
	rules := make([]scopeRuleDTO, len(state.Rules))
	for index, rule := range state.Rules {
		rules[index] = scopeRuleDTO{
			ID: rule.ID, Enabled: rule.Enabled, Action: rule.Action, Scheme: rule.Scheme,
			HostPattern: rule.HostPattern, Port: rule.Port, PathPrefix: rule.PathPrefix,
		}
	}
	return scopeStateDTO{Version: state.Version, Rules: rules}
}

func toTargetTreeNodeDTO(node store.TargetTreeNode) targetTreeNodeDTO {
	children := make([]targetTreeNodeDTO, len(node.Children))
	for index := range node.Children {
		children[index] = toTargetTreeNodeDTO(node.Children[index])
	}
	return targetTreeNodeDTO{
		ID: node.ID, Scheme: node.Scheme, Host: node.Host, Port: node.Port, Path: node.Path, Method: node.Method,
		InScope: node.InScope, Statuses: copyInts(node.Statuses), RequestMIMEs: copyStrings(node.RequestMIMEs),
		ResponseMIMEs: copyStrings(node.ResponseMIMEs), Count: node.Count, LastSeen: node.LastSeen, Children: children,
	}
}

func toTargetEndpointDTO(endpoint store.TargetEndpoint) targetEndpointDTO {
	return targetEndpointDTO{
		ID: endpoint.ID, Scheme: endpoint.Key.Scheme, Host: endpoint.Key.Host, Port: endpoint.Key.Port,
		Path: endpoint.Key.Path, Method: endpoint.Key.Method, InScope: endpoint.InScope,
		FirstSeen: endpoint.FirstSeen, LastSeen: endpoint.LastSeen, Count: endpoint.Count,
		Statuses: copyInts(endpoint.Statuses), RequestMIMEs: copyStrings(endpoint.RequestMIMEs),
		ResponseMIMEs: copyStrings(endpoint.ResponseMIMEs), ParseDiagnostics: copyStrings(endpoint.ParseDiagnostics),
		ErrorSeen: endpoint.ErrorSeen, LatestExchangeID: endpoint.LatestExchangeID,
	}
}

func toRebuildStatusDTO(status store.RebuildStatus) rebuildStatusDTO {
	return rebuildStatusDTO{
		ID: status.ID, ScopeVersion: status.ScopeVersion, ActiveScopeVersion: status.ActiveScopeVersion,
		Status: status.Status, Processed: status.Processed, Total: status.Total, Error: status.Error,
	}
}

func copyInts(values []int) []int {
	result := make([]int, len(values))
	copy(result, values)
	return result
}

func copyStrings(values []string) []string {
	result := make([]string, len(values))
	copy(result, values)
	return result
}
