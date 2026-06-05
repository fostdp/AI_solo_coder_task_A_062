CREATE EXTENSION IF NOT EXISTS postgis;

CREATE TABLE IF NOT EXISTS pump_stations (
    id SERIAL PRIMARY KEY,
    name VARCHAR(100) NOT NULL,
    geom GEOMETRY(Point, 4326),
    negative_pressure FLOAT DEFAULT 0,
    target_negative_pressure FLOAT DEFAULT 0,
    status VARCHAR(20) DEFAULT 'normal',
    created_at TIMESTAMP DEFAULT NOW()
);

CREATE TABLE IF NOT EXISTS boreholes (
    id SERIAL PRIMARY KEY,
    name VARCHAR(100) NOT NULL,
    pump_station_id INT REFERENCES pump_stations(id),
    geom GEOMETRY(Point, 4326),
    valve_opening FLOAT DEFAULT 100.0,
    target_valve_opening FLOAT DEFAULT 100.0,
    status VARCHAR(20) DEFAULT 'normal',
    created_at TIMESTAMP DEFAULT NOW()
);

CREATE TABLE IF NOT EXISTS pipelines (
    id SERIAL PRIMARY KEY,
    name VARCHAR(100),
    pump_station_id INT REFERENCES pump_stations(id),
    geom GEOMETRY(LineString, 4326),
    diameter FLOAT DEFAULT 200.0,
    created_at TIMESTAMP DEFAULT NOW()
);

CREATE TABLE IF NOT EXISTS borehole_data (
    id BIGSERIAL PRIMARY KEY,
    borehole_id INT REFERENCES boreholes(id),
    gas_flow FLOAT NOT NULL,
    gas_concentration FLOAT NOT NULL,
    negative_pressure FLOAT NOT NULL,
    temperature FLOAT NOT NULL,
    recorded_at TIMESTAMP DEFAULT NOW()
);

CREATE TABLE IF NOT EXISTS pump_station_data (
    id BIGSERIAL PRIMARY KEY,
    pump_station_id INT REFERENCES pump_stations(id),
    negative_pressure FLOAT NOT NULL,
    flow_rate FLOAT DEFAULT 0,
    efficiency FLOAT DEFAULT 0,
    recorded_at TIMESTAMP DEFAULT NOW()
);

CREATE TABLE IF NOT EXISTS alerts (
    id BIGSERIAL PRIMARY KEY,
    alert_type VARCHAR(50) NOT NULL,
    level VARCHAR(20) NOT NULL,
    source_id INT NOT NULL,
    source_type VARCHAR(50) NOT NULL,
    message TEXT,
    is_resolved BOOLEAN DEFAULT FALSE,
    created_at TIMESTAMP DEFAULT NOW(),
    resolved_at TIMESTAMP
);

CREATE TABLE IF NOT EXISTS optimization_results (
    id BIGSERIAL PRIMARY KEY,
    result JSONB NOT NULL,
    total_concentration FLOAT DEFAULT 0,
    created_at TIMESTAMP DEFAULT NOW()
);

CREATE TABLE IF NOT EXISTS plc_commands (
    id BIGSERIAL PRIMARY KEY,
    target_type VARCHAR(50) NOT NULL,
    target_id INT NOT NULL,
    command_type VARCHAR(50) NOT NULL,
    command_value FLOAT NOT NULL,
    status VARCHAR(20) DEFAULT 'pending',
    mqtt_topic VARCHAR(200),
    created_at TIMESTAMP DEFAULT NOW(),
    executed_at TIMESTAMP,
    result TEXT
);

CREATE INDEX IF NOT EXISTS idx_borehole_data_borehole_id ON borehole_data(borehole_id);
CREATE INDEX IF NOT EXISTS idx_borehole_data_recorded_at ON borehole_data(recorded_at);
CREATE INDEX IF NOT EXISTS idx_borehole_data_composite ON borehole_data(borehole_id, recorded_at);
CREATE INDEX IF NOT EXISTS idx_pump_station_data_pump_id ON pump_station_data(pump_station_id);
CREATE INDEX IF NOT EXISTS idx_alerts_unresolved ON alerts(is_resolved) WHERE is_resolved = FALSE;
CREATE INDEX IF NOT EXISTS idx_boreholes_pump_station ON boreholes(pump_station_id);

INSERT INTO pump_stations (id, name, geom, negative_pressure, status) VALUES
(1, '1号泵站', ST_SetSRID(ST_MakePoint(106.550, 28.630), 4326), 40.0, 'normal'),
(2, '2号泵站', ST_SetSRID(ST_MakePoint(106.555, 28.635), 4326), 42.0, 'normal'),
(3, '3号泵站', ST_SetSRID(ST_MakePoint(106.560, 28.628), 4326), 38.0, 'normal');

DO $$
DECLARE
    i INT;
    station_id INT;
    bh_count INT;
    base_x FLOAT;
    base_y FLOAT;
BEGIN
    FOR station_id IN 1..3 LOOP
        SELECT ST_X(geom), ST_Y(geom) INTO base_x, base_y FROM pump_stations WHERE id = station_id;
        bh_count := 200;
        FOR i IN 1..bh_count LOOP
            INSERT INTO boreholes (name, pump_station_id, geom, valve_opening, status)
            VALUES (
                station_id::text || '号站-' || LPAD(i::text, 3, '0') || '号孔',
                station_id,
                ST_SetSRID(ST_MakePoint(
                    base_x + (random() - 0.5) * 0.008,
                    base_y + (random() - 0.5) * 0.006
                ), 4326),
                50.0 + random() * 50.0,
                'normal'
            );
        END LOOP;
    END LOOP;
END $$;

INSERT INTO pipelines (name, pump_station_id, geom, diameter)
SELECT
    '主管道-' || ps.name,
    ps.id,
    ST_SetSRID(ST_MakeLine(
        ps.geom,
        ST_SetSRID(ST_MakePoint(
            ST_X(ps.geom) + 0.002,
            ST_Y(ps.geom) + 0.001
        ), 4326)
    ), 4326),
    300.0
FROM pump_stations ps;

INSERT INTO borehole_data (borehole_id, gas_flow, gas_concentration, negative_pressure, temperature, recorded_at)
SELECT
    b.id,
    0.5 + random() * 4.5,
    10.0 + random() * 60.0,
    20.0 + random() * 25.0,
    18.0 + random() * 12.0,
    NOW() - (random() * INTERVAL '24 hours')
FROM boreholes b;

INSERT INTO pump_station_data (pump_station_id, negative_pressure, flow_rate, efficiency, recorded_at)
SELECT
    ps.id,
    ps.negative_pressure + (random() - 0.5) * 5,
    500 + random() * 500,
    60 + random() * 30,
    NOW() - (random() * INTERVAL '24 hours')
FROM pump_stations ps;
