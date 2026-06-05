package database

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"gas-drainage-system/internal/models"
)

type Config struct {
	Host     string
	Port     int
	User     string
	Password string
	DBName   string
	SSLMode  string
	MaxConns int32
}

type DB struct {
	Pool *pgxpool.Pool
}

func NewPool(cfg Config) (*DB, error) {
	maxConns := cfg.MaxConns
	if maxConns <= 0 {
		maxConns = 50
	}
	connStr := fmt.Sprintf(
		"host=%s port=%d user=%s password=%s dbname=%s sslmode=%s pool_max_conns=%d pool_min_conns=5",
		cfg.Host, cfg.Port, cfg.User, cfg.Password, cfg.DBName, cfg.SSLMode, maxConns,
	)
	config, err := pgxpool.ParseConfig(connStr)
	if err != nil {
		return nil, fmt.Errorf("parse config: %w", err)
	}
	config.HealthCheckPeriod = 30 * time.Second
	config.MaxConnIdleTime = 5 * time.Minute
	config.MaxConnLifetime = 1 * time.Hour

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	pool, err := pgxpool.NewWithConfig(ctx, config)
	if err != nil {
		return nil, fmt.Errorf("create pool: %w", err)
	}
	if err := pool.Ping(ctx); err != nil {
		return nil, fmt.Errorf("ping: %w", err)
	}
	return &DB{Pool: pool}, nil
}

func (db *DB) Close() {
	db.Pool.Close()
}

func (db *DB) StoreBoreholeData(ctx context.Context, data models.BoreholeData) error {
	_, err := db.Pool.Exec(ctx,
		"INSERT INTO borehole_data (borehole_id, gas_flow, gas_concentration, negative_pressure, temperature, recorded_at) VALUES ($1, $2, $3, $4, $5, $6)",
		data.BoreholeID, data.GasFlow, data.GasConcentration, data.NegativePressure, data.Temperature, data.RecordedAt,
	)
	return err
}

func (db *DB) StoreBoreholeDataBatch(ctx context.Context, batch []models.BoreholeData) error {
	if len(batch) == 0 {
		return nil
	}
	tx, err := db.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	_, err = tx.Prepare(ctx, "batch_insert", `
		INSERT INTO borehole_data (borehole_id, gas_flow, gas_concentration, negative_pressure, temperature, recorded_at)
		VALUES ($1, $2, $3, $4, $5, $6)
	`)
	if err != nil {
		return err
	}
	for _, d := range batch {
		_, err = tx.Exec(ctx, "batch_insert",
			d.BoreholeID, d.GasFlow, d.GasConcentration, d.NegativePressure, d.Temperature, d.RecordedAt,
		)
		if err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
}

func (db *DB) StorePumpStationData(ctx context.Context, data models.PumpStationData) error {
	_, err := db.Pool.Exec(ctx,
		"INSERT INTO pump_station_data (pump_station_id, negative_pressure, flow_rate, efficiency, recorded_at) VALUES ($1, $2, $3, $4, $5)",
		data.PumpStationID, data.NegativePressure, data.FlowRate, data.Efficiency, data.RecordedAt,
	)
	return err
}

func computeScore(concentration, flow float64) float64 {
	score := (concentration/70.0)*0.6 + (flow/5.0)*0.4
	if score < 0 {
		return 0
	}
	if score > 1 {
		return 1
	}
	return score
}

func (db *DB) GetBoreholesWithLatestData(ctx context.Context) ([]models.Borehole, error) {
	rows, err := db.Pool.Query(ctx, `
		SELECT b.id, b.name, b.pump_station_id, ST_X(b.geom), ST_Y(b.geom),
			b.valve_opening, b.target_valve_opening, b.status,
			COALESCE(bd.gas_flow, 0), COALESCE(bd.gas_concentration, 0),
			COALESCE(bd.negative_pressure, 0), COALESCE(bd.temperature, 0)
		FROM boreholes b
		LEFT JOIN LATERAL (
			SELECT gas_flow, gas_concentration, negative_pressure, temperature
			FROM borehole_data
			WHERE borehole_id = b.id
			ORDER BY recorded_at DESC
			LIMIT 1
		) bd ON true
		ORDER BY b.id
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var boreholes []models.Borehole
	for rows.Next() {
		var b models.Borehole
		if err := rows.Scan(&b.ID, &b.Name, &b.PumpStationID, &b.Lng, &b.Lat,
			&b.ValveOpening, &b.TargetValveOpening, &b.Status,
			&b.GasFlow, &b.GasConcentration, &b.NegativePressure, &b.Temperature,
		); err != nil {
			return nil, err
		}
		b.Score = computeScore(b.GasConcentration, b.GasFlow)
		boreholes = append(boreholes, b)
	}
	return boreholes, rows.Err()
}

func (db *DB) GetBoreholeTrend(ctx context.Context, boreholeID int) (*models.BoreholeDetail, error) {
	var b models.Borehole
	err := db.Pool.QueryRow(ctx, `
		SELECT b.id, b.name, b.pump_station_id, ST_X(b.geom), ST_Y(b.geom),
			b.valve_opening, b.target_valve_opening, b.status,
			COALESCE(bd.gas_flow, 0), COALESCE(bd.gas_concentration, 0),
			COALESCE(bd.negative_pressure, 0), COALESCE(bd.temperature, 0)
		FROM boreholes b
		LEFT JOIN LATERAL (
			SELECT gas_flow, gas_concentration, negative_pressure, temperature
			FROM borehole_data
			WHERE borehole_id = b.id
			ORDER BY recorded_at DESC
			LIMIT 1
		) bd ON true
		WHERE b.id = $1
	`, boreholeID).Scan(&b.ID, &b.Name, &b.PumpStationID, &b.Lng, &b.Lat,
		&b.ValveOpening, &b.TargetValveOpening, &b.Status,
		&b.GasFlow, &b.GasConcentration, &b.NegativePressure, &b.Temperature,
	)
	if err != nil {
		return nil, err
	}
	b.Score = computeScore(b.GasConcentration, b.GasFlow)

	rows, err := db.Pool.Query(ctx, `
		SELECT gas_flow, gas_concentration
		FROM borehole_data
		WHERE borehole_id = $1 AND recorded_at >= NOW() - INTERVAL '24 hours'
		ORDER BY recorded_at ASC
	`, boreholeID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var trend models.TrendData
	for rows.Next() {
		var flow, conc float64
		if err := rows.Scan(&flow, &conc); err != nil {
			return nil, err
		}
		trend.Flow = append(trend.Flow, flow)
		trend.Concentration = append(trend.Concentration, conc)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	return &models.BoreholeDetail{
		Borehole:  b,
		TrendData: trend,
	}, nil
}

func (db *DB) GetKPIData(ctx context.Context) (*models.KPIData, error) {
	var kpi models.KPIData
	err := db.Pool.QueryRow(ctx, `
		SELECT
			COALESCE((SELECT SUM(gas_flow) FROM borehole_data WHERE recorded_at >= NOW() - INTERVAL '1 hour'), 0),
			COALESCE((SELECT AVG(gas_concentration) FROM borehole_data WHERE recorded_at >= NOW() - INTERVAL '1 hour'), 0),
			COALESCE((SELECT AVG(efficiency) FROM pump_station_data WHERE recorded_at >= NOW() - INTERVAL '1 hour'), 0)
	`).Scan(&kpi.TotalFlow, &kpi.AvgConcentration, &kpi.PumpEfficiency)
	if err != nil {
		return nil, err
	}
	return &kpi, nil
}

type geoJSONLineString struct {
	Coordinates [][]float64 `json:"coordinates"`
}

func (db *DB) GetPipelines(ctx context.Context) ([]models.Pipeline, error) {
	rows, err := db.Pool.Query(ctx, "SELECT id, name, pump_station_id, ST_AsGeoJSON(geom), diameter FROM pipelines ORDER BY id")
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var pipelines []models.Pipeline
	for rows.Next() {
		var p models.Pipeline
		var geoJSON string
		if err := rows.Scan(&p.ID, &p.Name, &p.PumpStationID, &geoJSON, &p.Diameter); err != nil {
			return nil, err
		}
		var ls geoJSONLineString
		if err := json.Unmarshal([]byte(geoJSON), &ls); err != nil {
			return nil, err
		}
		p.Points = ls.Coordinates
		pipelines = append(pipelines, p)
	}
	return pipelines, rows.Err()
}

func (db *DB) GetPumpStations(ctx context.Context) ([]models.PumpStation, error) {
	rows, err := db.Pool.Query(ctx, "SELECT id, name, ST_X(geom), ST_Y(geom), negative_pressure, target_negative_pressure, status FROM pump_stations ORDER BY id")
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var stations []models.PumpStation
	for rows.Next() {
		var s models.PumpStation
		if err := rows.Scan(&s.ID, &s.Name, &s.Lng, &s.Lat, &s.NegativePressure, &s.TargetNegativePressure, &s.Status); err != nil {
			return nil, err
		}
		stations = append(stations, s)
	}
	return stations, rows.Err()
}

func (db *DB) GetUnresolvedAlerts(ctx context.Context) ([]models.Alert, error) {
	rows, err := db.Pool.Query(ctx, `
		SELECT id, alert_type, level, source_id, source_type, COALESCE(message, ''), is_resolved, created_at, resolved_at
		FROM alerts WHERE is_resolved = false ORDER BY created_at DESC
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var alerts []models.Alert
	for rows.Next() {
		var a models.Alert
		if err := rows.Scan(&a.ID, &a.AlertType, &a.Level, &a.SourceID, &a.SourceType, &a.Message, &a.IsResolved, &a.CreatedAt, &a.ResolvedAt); err != nil {
			return nil, err
		}
		alerts = append(alerts, a)
	}
	return alerts, rows.Err()
}

func (db *DB) StoreAlert(ctx context.Context, a models.Alert) error {
	_, err := db.Pool.Exec(ctx,
		"INSERT INTO alerts (alert_type, level, source_id, source_type, message, is_resolved, created_at, resolved_at) VALUES ($1, $2, $3, $4, $5, $6, $7, $8)",
		a.AlertType, a.Level, a.SourceID, a.SourceType, a.Message, a.IsResolved, a.CreatedAt, a.ResolvedAt,
	)
	return err
}

func (db *DB) StoreOptimizationResult(ctx context.Context, r models.OptimizationResult) error {
	_, err := db.Pool.Exec(ctx,
		"INSERT INTO optimization_results (result, total_concentration, created_at) VALUES ($1, $2, $3)",
		string(r.Result), r.TotalConcentration, r.CreatedAt,
	)
	return err
}

func (db *DB) StorePLCCommand(ctx context.Context, c models.PLCCommand) error {
	var executedAt interface{}
	if c.ExecutedAt != nil {
		executedAt = *c.ExecutedAt
	}
	_, err := db.Pool.Exec(ctx,
		"INSERT INTO plc_commands (target_type, target_id, command_type, command_value, status, mqtt_topic, created_at, executed_at, result) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)",
		c.TargetType, c.TargetID, c.CommandType, c.CommandValue, c.Status, c.MQTTTopic, c.CreatedAt, executedAt, c.Result,
	)
	return err
}

func (db *DB) UpdatePLCCommandResult(ctx context.Context, id int, status string, result string) error {
	_, err := db.Pool.Exec(ctx,
		"UPDATE plc_commands SET status = $1, result = $2, executed_at = $3 WHERE id = $4",
		status, result, time.Now(), id,
	)
	return err
}

func (db *DB) UpdateBoreholeValve(ctx context.Context, id int, valveOpening float64) error {
	_, err := db.Pool.Exec(ctx,
		"UPDATE boreholes SET valve_opening = $1 WHERE id = $2",
		valveOpening, id,
	)
	return err
}

func (db *DB) UpdatePumpPressure(ctx context.Context, id int, negativePressure float64) error {
	_, err := db.Pool.Exec(ctx,
		"UPDATE pump_stations SET negative_pressure = $1 WHERE id = $2",
		negativePressure, id,
	)
	return err
}
