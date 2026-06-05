package alert

import (
	"fmt"
	"log"
	"strconv"
	"time"

	"gas-drainage-system/internal/database"
	"gas-drainage-system/internal/models"
)

type Broadcaster interface {
	Broadcast(msgType string, payload interface{})
}

type Engine struct {
	db            *database.DB
	hub           Broadcaster
	checkInterval time.Duration
}

func NewEngine(db *database.DB, hub Broadcaster, checkInterval time.Duration) *Engine {
	return &Engine{
		db:            db,
		hub:           hub,
		checkInterval: checkInterval,
	}
}

func (e *Engine) Start() {
	ticker := time.NewTicker(e.checkInterval)
	defer ticker.Stop()
	for range ticker.C {
		e.CheckLowEfficiency()
		e.CheckPressureAnomaly()
	}
}

func (e *Engine) CheckLowEfficiency() {
	rows, err := e.db.Query(`
		SELECT borehole_id
		FROM borehole_data
		WHERE recorded_at > NOW() - INTERVAL '30 minutes'
		GROUP BY borehole_id
		HAVING MAX(gas_concentration) < 10
	`)
	if err != nil {
		log.Printf("CheckLowEfficiency query error: %v", err)
		return
	}
	defer rows.Close()

	for rows.Next() {
		var boreholeID int
		if err := rows.Scan(&boreholeID); err != nil {
			log.Printf("scan error: %v", err)
			continue
		}

		var exists bool
		err := e.db.QueryRow(
			`SELECT EXISTS(SELECT 1 FROM alerts WHERE source_id = $1 AND alert_type = 'low_efficiency' AND created_at > NOW() - INTERVAL '30 minutes')`,
			boreholeID,
		).Scan(&exists)
		if err != nil || exists {
			continue
		}

		a := models.Alert{
			AlertType:  "low_efficiency",
			Level:      "warning",
			SourceID:   boreholeID,
			SourceType: "borehole",
			Message:    fmt.Sprintf("钻孔 %d 瓦斯浓度低于10%%持续超过30分钟", boreholeID),
			IsResolved: false,
			CreatedAt:  time.Now(),
		}
		err = e.db.StoreAlert(a)
		if err != nil {
			log.Printf("insert alert error: %v", err)
			continue
		}

		e.hub.Broadcast("alert", a)
	}
}

func (e *Engine) CheckPressureAnomaly() {
	rows, err := e.db.Query(`
		WITH avg_pressure AS (
			SELECT pump_station_id, AVG(negative_pressure) AS avg_pressure
			FROM pump_station_data
			WHERE recorded_at > NOW() - INTERVAL '1 hour'
			GROUP BY pump_station_id
		),
		latest AS (
			SELECT DISTINCT ON (pump_station_id) pump_station_id, negative_pressure
			FROM pump_station_data
			ORDER BY pump_station_id, recorded_at DESC
		)
		SELECT l.pump_station_id, l.negative_pressure, a.avg_pressure
		FROM latest l
		JOIN avg_pressure a ON l.pump_station_id = a.pump_station_id
		WHERE a.avg_pressure <> 0
		  AND ABS(l.negative_pressure - a.avg_pressure) / ABS(a.avg_pressure) > 0.2
	`)
	if err != nil {
		log.Printf("CheckPressureAnomaly query error: %v", err)
		return
	}
	defer rows.Close()

	for rows.Next() {
		var stationID int
		var currentPressure, avgPressure float64
		if err := rows.Scan(&stationID, &currentPressure, &avgPressure); err != nil {
			log.Printf("scan error: %v", err)
			continue
		}

		var exists bool
		err := e.db.QueryRow(
			`SELECT EXISTS(SELECT 1 FROM alerts WHERE source_id = $1 AND alert_type = 'pressure_anomaly' AND created_at > NOW() - INTERVAL '1 hour')`,
			strconv.Itoa(stationID),
		).Scan(&exists)
		if err != nil || exists {
			continue
		}

		a := models.Alert{
			AlertType:  "pressure_anomaly",
			Level:      "critical",
			SourceID:   stationID,
			SourceType: "pump_station",
			Message:    fmt.Sprintf("泵站 %d 负压异常波动：当前 %.2f kPa，均值 %.2f kPa（偏差>20%%）", stationID, currentPressure, avgPressure),
			IsResolved: false,
			CreatedAt:  time.Now(),
		}
		err = e.db.StoreAlert(a)
		if err != nil {
			log.Printf("insert alert error: %v", err)
			continue
		}

		e.hub.Broadcast("alert", a)
	}
}
