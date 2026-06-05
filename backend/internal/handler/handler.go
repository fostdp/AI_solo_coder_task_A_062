package handler

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strconv"

	"gas-drainage-system/internal/database"
	"gas-drainage-system/internal/models"
	"gas-drainage-system/internal/optimizer"
)

type Handler struct {
	db   *database.DB
	hub  *Hub
	opt  *optimizer.Optimizer
}

func NewHandler(db *database.DB, hub *Hub, opt *optimizer.Optimizer) *Handler {
	return &Handler{db: db, hub: hub, opt: opt}
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
	mux.HandleFunc("GET /ws", func(w http.ResponseWriter, r *http.Request) {
		ServeWs(h.hub, w, r)
	})
}

func (h *Handler) handleGetPumpStations(w http.ResponseWriter, r *http.Request) {
	stations, err := h.db.GetPumpStations()
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
		h.db.QueryRow(`
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
	boreholes, err := h.db.GetBoreholesWithLatestData()
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

	detail, err := h.db.GetBoreholeTrend(id)
	if err != nil {
		if err == sql.ErrNoRows {
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
	pipelines, err := h.db.GetPipelines()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusOK, pipelines)
}

func (h *Handler) handleGetKPI(w http.ResponseWriter, r *http.Request) {
	kpi, err := h.db.GetKPIData()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusOK, kpi)
}

func (h *Handler) handleGetAlerts(w http.ResponseWriter, r *http.Request) {
	alerts, err := h.db.GetUnresolvedAlerts()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusOK, alerts)
}

func (h *Handler) handleGetOptimizationLatest(w http.ResponseWriter, r *http.Request) {
	var or models.OptimizationResult
	err := h.db.QueryRow(`
		SELECT id, result, total_concentration, created_at
		FROM optimization_results
		ORDER BY created_at DESC LIMIT 1
	`).Scan(&or.ID, &or.Result, &or.TotalConcentration, &or.CreatedAt)
	if err != nil {
		if err == sql.ErrNoRows {
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

	go func() {
		result, err := h.opt.Run()
		if err != nil {
			log.Printf("optimization run error: %v", err)
		} else {
			log.Printf("optimization complete: fitness=%.4f", result.Fitness)
		}
	}()

	writeJSON(w, http.StatusOK, map[string]string{
		"status":  "running",
		"message": "优化计算已启动，请稍后查看结果",
	})
}

func (h *Handler) handleGetPLCCommands(w http.ResponseWriter, r *http.Request) {
	rows, err := h.db.Query(`
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
	var bd models.BoreholeData
	if err := json.NewDecoder(r.Body).Decode(&bd); err != nil {
		http.Error(w, fmt.Sprintf("invalid request body: %v", err), http.StatusBadRequest)
		return
	}

	if err := h.db.StoreBoreholeData(bd); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	h.hub.Broadcast("borehole_update", bd)
	writeJSON(w, http.StatusCreated, bd)
}

func (h *Handler) handlePostPumpStationData(w http.ResponseWriter, r *http.Request) {
	var psd models.PumpStationData
	if err := json.NewDecoder(r.Body).Decode(&psd); err != nil {
		http.Error(w, fmt.Sprintf("invalid request body: %v", err), http.StatusBadRequest)
		return
	}

	if err := h.db.StorePumpStationData(psd); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	writeJSON(w, http.StatusCreated, psd)
}

func writeJSON(w http.ResponseWriter, status int, v interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}
