package models

import (
	"encoding/json"
	"time"
)

type PumpStation struct {
	ID                     int     `json:"id"`
	Name                   string  `json:"name"`
	Lng                    float64 `json:"lng"`
	Lat                    float64 `json:"lat"`
	NegativePressure       float64 `json:"negative_pressure"`
	TargetNegativePressure float64 `json:"target_negative_pressure"`
	Status                 string  `json:"status"`
}

type Borehole struct {
	ID                 int     `json:"id"`
	Name               string  `json:"name"`
	PumpStationID      int     `json:"pump_station_id"`
	Lng                float64 `json:"lng"`
	Lat                float64 `json:"lat"`
	ValveOpening       float64 `json:"valve_opening"`
	TargetValveOpening float64 `json:"target_valve_opening"`
	Status             string  `json:"status"`
	Score              float64 `json:"score"`
	GasFlow            float64 `json:"gas_flow"`
	GasConcentration   float64 `json:"gas_concentration"`
	NegativePressure   float64 `json:"negative_pressure"`
	Temperature        float64 `json:"temperature"`
}

type BoreholeData struct {
	ID               int       `json:"id"`
	BoreholeID       int       `json:"borehole_id"`
	GasFlow          float64   `json:"gas_flow"`
	GasConcentration float64   `json:"gas_concentration"`
	NegativePressure float64   `json:"negative_pressure"`
	Temperature      float64   `json:"temperature"`
	RecordedAt       time.Time `json:"recorded_at"`
}

type PumpStationData struct {
	ID               int       `json:"id"`
	PumpStationID    int       `json:"pump_station_id"`
	NegativePressure float64   `json:"negative_pressure"`
	FlowRate         float64   `json:"flow_rate"`
	Efficiency       float64   `json:"efficiency"`
	RecordedAt       time.Time `json:"recorded_at"`
}

type Pipeline struct {
	ID            int         `json:"id"`
	Name          string      `json:"name"`
	PumpStationID int         `json:"pump_station_id"`
	Points        [][]float64 `json:"points"`
	Diameter      float64     `json:"diameter"`
}

type Alert struct {
	ID         int        `json:"id"`
	AlertType  string     `json:"alert_type"`
	Level      string     `json:"level"`
	SourceID   int        `json:"source_id"`
	SourceType string     `json:"source_type"`
	Message    string     `json:"message"`
	IsResolved bool       `json:"is_resolved"`
	CreatedAt  time.Time  `json:"created_at"`
	ResolvedAt *time.Time `json:"resolved_at"`
}

type OptimizationResult struct {
	ID                 int             `json:"id"`
	Result             json.RawMessage `json:"result"`
	TotalConcentration float64         `json:"total_concentration"`
	CreatedAt          time.Time       `json:"created_at"`
}

type PLCCommand struct {
	ID           int        `json:"id"`
	TargetType   string     `json:"target_type"`
	TargetID     int        `json:"target_id"`
	CommandType  string     `json:"command_type"`
	CommandValue float64    `json:"command_value"`
	Status       string     `json:"status"`
	MQTTTopic    string     `json:"mqtt_topic"`
	CreatedAt    time.Time  `json:"created_at"`
	ExecutedAt   *time.Time `json:"executed_at"`
	Result       string     `json:"result"`
}

type KPIData struct {
	TotalFlow        float64 `json:"total_flow"`
	AvgConcentration float64 `json:"avg_concentration"`
	PumpEfficiency   float64 `json:"pump_efficiency"`
}

type TrendData struct {
	Flow         []float64 `json:"flow"`
	Concentration []float64 `json:"concentration"`
}

type BoreholeDetail struct {
	Borehole
	TrendData TrendData `json:"trend_data"`
}
