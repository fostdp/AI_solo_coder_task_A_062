package handler

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"gas-drainage-system/internal/database"
	"gas-drainage-system/internal/data_collector"
	"gas-drainage-system/internal/models"
	"gas-drainage-system/internal/network_optimizer"

	"github.com/jackc/pgx/v5"
)

type Handler struct {
	db   *database.DB
	hub  *Hub
	coll *data_collector.DataCollector
	opt  *network_optimizer.NetworkOptimizer
}

func NewHandler(db *database.DB, hub *Hub, coll *data_collector.DataCollector, opt *network_optimizer.NetworkOptimizer) *Handler {
	return &Handler{db: db, hub: hub, coll: coll, opt: opt}
}

func (h *Handler) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/pump-stations", h.handleGetPumpStations)
	mux.HandleFunc("GET /api/boreholes", h.handleGetBoreholes)
	mux.HandleFunc("GET /api/boreholes/{id}/trend", h.handleGetBoreholeTrend)
	mux.HandleFunc("GET /api/pipelines", h.handleGetPipelines)
	mux.HandleFunc("GET /api/kpi", h.handleGetKPI)
	mux.HandleFunc("GET /api/alerts", h.handleGetAlerts)
	mux.HandleFunc("GET /api/optimization/latest", h.handleGetOptimizationLatest)
	mux.HandleFunc("POST /api/optimization/run", h.handleRunOptimization)
	mux.HandleFunc("GET /api/plc-commands", h.handleGetPLCCommands)
	mux.HandleFunc("POST /api/data/borehole", h.handlePostBoreholeData)
	mux.HandleFunc("POST /api/data/pump-station", h.handlePostPumpStationData)
	mux.HandleFunc("POST /api/data/borehole/batch", h.handlePostBoreholeDataBatch)
	mux.HandleFunc("GET /ws", func(w http.ResponseWriter, r *http.Request) {
		ServeWs(h.hub, w, r)
	})
}

func (h *Handler) handleGetPumpStations(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()

	stations, err := h.db.GetPumpStations(ctx)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	type PumpStationWithLatest struct {
		models.PumpStation
		LatestFlowRate   float64 `json:"latest_flow_rate"`
		LatestEfficiency float64 `json:"latest_efficiency"`
	}

	var result []PumpStationWithLatest
	for _, s := range stations {
		var latestFlow, latestEff float64
		h.db.Pool.QueryRow(ctx, `
			SELECT COALESCE(flow_rate, 0), COALESCE(efficiency, 0)
			FROM pump_station_data WHERE pump_station_id = $1
			ORDER BY recorded_at DESC LIMIT 1`, s.ID).Scan(&latestFlow, &latestEff)
		result = append(result, PumpStationWithLatest{
			PumpStation:      s,
			LatestFlowRate:   latestFlow,
			LatestEfficiency: latestEff,
		})
	}
	writeJSON(w, http.StatusOK, result)
}

func (h *Handler) handleGetBoreholes(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()

	boreholes, err := h.db.GetBoreholesWithLatestData(ctx)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusOK, boreholes)
}

func (h *Handler) handleGetBoreholeTrend(w http.ResponseWriter, r *http.Request) {
	idStr := r.PathValue("id")
	id, err := strconv.Atoi(idStr)
	if err != nil {
		http.Error(w, "invalid borehole id", http.StatusBadRequest)
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()

	detail, err := h.db.GetBoreholeTrend(ctx, id)
	if err != nil {
		if err == pgx.ErrNoRows {
			writeJSON(w, http.StatusOK, nil)
			return
		}
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	type trendResponse struct {
		ID               int       `json:"id"`
		Name             string    `json:"name"`
		PumpStationID    int       `json:"pump_station_id"`
		GasFlow          float64   `json:"gas_flow"`
		GasConcentration float64   `json:"gas_concentration"`
		Score            float64   `json:"score"`
		Flow             []float64 `json:"flow"`
		Concentration    []float64 `json:"concentration"`
	}

	resp := trendResponse{
		ID:               detail.ID,
		Name:             detail.Name,
		PumpStationID:    detail.PumpStationID,
		GasFlow:          detail.GasFlow,
		GasConcentration: detail.GasConcentration,
		Score:            detail.Score,
		Flow:             detail.TrendData.Flow,
		Concentration:    detail.TrendData.Concentration,
	}
	writeJSON(w, http.StatusOK, resp)
}

func (h *Handler) handleGetPipelines(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()

	pipelines, err := h.db.GetPipelines(ctx)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusOK, pipelines)
}

func (h *Handler) handleGetKPI(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()

	kpi, err := h.db.GetKPIData(ctx)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusOK, kpi)
}

func (h *Handler) handleGetAlerts(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()

	alerts, err := h.db.GetUnresolvedAlerts(ctx)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusOK, alerts)
}

func (h *Handler) handleGetOptimizationLatest(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()

	var or models.OptimizationResult
	err := h.db.Pool.QueryRow(ctx, `
		SELECT id, result, total_concentration, created_at
		FROM optimization_results
		ORDER BY created_at DESC LIMIT 1
	`).Scan(&or.ID, &or.Result, &or.TotalConcentration, &or.CreatedAt)
	if err != nil {
		if err == pgx.ErrNoRows {
			writeJSON(w, http.StatusOK, nil)
			return
		}
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusOK, or)
}

func (h *Handler) handleRunOptimization(w http.ResponseWriter, r *http.Request) {
	if h.opt == nil {
		http.Error(w, "optimizer not configured", http.StatusServiceUnavailable)
		return
	}

	req := &network_optimizer.OptimizeRequest{
		Response: nil,
	}

	select {
	case h.opt.OptimizeCh <- req:
		writeJSON(w, http.StatusOK, map[string]string{
			"status":  "running",
			"message": "优化计算已启动，结果将通过WebSocket推送",
		})
	default:
		http.Error(w, "optimization queue full", http.StatusServiceUnavailable)
	}
}

func (h *Handler) handleGetPLCCommands(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()

	rows, err := h.db.Pool.Query(ctx, `
		SELECT id, target_type, target_id, command_type, command_value, status, mqtt_topic, created_at, executed_at, result
		FROM plc_commands
		ORDER BY created_at DESC LIMIT 50
	`)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	defer rows.Close()

	var result []models.PLCCommand
	for rows.Next() {
		var c models.PLCCommand
		if err := rows.Scan(&c.ID, &c.TargetType, &c.TargetID, &c.CommandType, &c.CommandValue, &c.Status, &c.MQTTTopic, &c.CreatedAt, &c.ExecutedAt, &c.Result); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		result = append(result, c)
	}
	writeJSON(w, http.StatusOK, result)
}

func (h *Handler) handlePostBoreholeData(w http.ResponseWriter, r *http.Request) {
	var raw json.RawMessage
	if err := json.NewDecoder(r.Body).Decode(&raw); err != nil {
		http.Error(w, fmt.Sprintf("invalid request body: %v", err), http.StatusBadRequest)
		return
	}

	respCh := make(chan *data_collector.DataResponse, 1)
	evt := &data_collector.DataEvent{
		Kind:     "borehole",
		Payload:  raw,
		Response: respCh,
	}

	select {
	case h.coll.DataCh <- evt:
	case <-time.After(3 * time.Second):
		http.Error(w, "data collector busy", http.StatusServiceUnavailable)
		return
	}

	select {
	case resp := <-respCh:
		if resp.Error != nil {
			http.Error(w, resp.Error.Error(), http.StatusInternalServerError)
			return
		}
		writeJSON(w, http.StatusCreated, resp.Data)
	case <-time.After(5 * time.Second):
		http.Error(w, "write timeout", http.StatusGatewayTimeout)
	}
}

func (h *Handler) handlePostBoreholeDataBatch(w http.ResponseWriter, r *http.Request) {
	var raw json.RawMessage
	if err := json.NewDecoder(r.Body).Decode(&raw); err != nil {
		http.Error(w, fmt.Sprintf("invalid request body: %v", err), http.StatusBadRequest)
		return
	}

	respCh := make(chan *data_collector.DataResponse, 1)
	evt := &data_collector.DataEvent{
		Kind:     "borehole_batch",
		Payload:  raw,
		Response: respCh,
	}

	select {
	case h.coll.DataCh <- evt:
	case <-time.After(5 * time.Second):
		http.Error(w, "data collector busy", http.StatusServiceUnavailable)
		return
	}

	select {
	case resp := <-respCh:
		if resp.Error != nil {
			http.Error(w, resp.Error.Error(), http.StatusInternalServerError)
			return
		}
		writeJSON(w, http.StatusCreated, resp.Data)
	case <-time.After(10 * time.Second):
		http.Error(w, "batch write timeout", http.StatusGatewayTimeout)
	}
}

func (h *Handler) handlePostPumpStationData(w http.ResponseWriter, r *http.Request) {
	var raw json.RawMessage
	if err := json.NewDecoder(r.Body).Decode(&raw); err != nil {
		http.Error(w, fmt.Sprintf("invalid request body: %v", err), http.StatusBadRequest)
		return
	}

	respCh := make(chan *data_collector.DataResponse, 1)
	evt := &data_collector.DataEvent{
		Kind:     "pump_station",
		Payload:  raw,
		Response: respCh,
	}

	select {
	case h.coll.DataCh <- evt:
	case <-time.After(3 * time.Second):
		http.Error(w, "data collector busy", http.StatusServiceUnavailable)
		return
	}

	select {
	case resp := <-respCh:
		if resp.Error != nil {
			http.Error(w, resp.Error.Error(), http.StatusInternalServerError)
			return
		}
		writeJSON(w, http.StatusCreated, resp.Data)
	case <-time.After(5 * time.Second):
		http.Error(w, "write timeout", http.StatusGatewayTimeout)
	}
}

func writeJSON(w http.ResponseWriter, status int, v interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}
