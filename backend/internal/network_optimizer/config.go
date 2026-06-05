package network_optimizer

import (
	"encoding/json"
	"fmt"
	"os"
	"time"
)

type OptimizerConfig struct {
	PopulationSize              int
	MaxGenerations              int
	EliteCount                  int
	TournamentSize              int
	MutationRate                float64
	CrossoverRate               float64
	PumpMin                     float64
	PumpMax                     float64
	ValveMin                    float64
	ValveMax                    float64
	MaxOptimizationTimeSeconds  int
	MaxOptimizationTime         time.Duration
	StagnationLimit             int
}

type jsonConfig struct {
	PopulationSize             int     `json:"population_size"`
	MaxGenerations             int     `json:"max_generations"`
	EliteCount                 int     `json:"elite_count"`
	TournamentSize             int     `json:"tournament_size"`
	MutationRate               float64 `json:"mutation_rate"`
	CrossoverRate              float64 `json:"crossover_rate"`
	PumpMin                    float64 `json:"pump_min"`
	PumpMax                    float64 `json:"pump_max"`
	ValveMin                   float64 `json:"valve_min"`
	ValveMax                   float64 `json:"valve_max"`
	MaxOptimizationTimeSeconds int     `json:"max_optimization_time_seconds"`
	StagnationLimit            int     `json:"stagnation_limit"`
}

func LoadConfig(path string) (*OptimizerConfig, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read config file: %w", err)
	}

	var jc jsonConfig
	if err := json.Unmarshal(data, &jc); err != nil {
		return nil, fmt.Errorf("parse config json: %w", err)
	}

	cfg := &OptimizerConfig{
		PopulationSize:              jc.PopulationSize,
		MaxGenerations:              jc.MaxGenerations,
		EliteCount:                  jc.EliteCount,
		TournamentSize:              jc.TournamentSize,
		MutationRate:                jc.MutationRate,
		CrossoverRate:               jc.CrossoverRate,
		PumpMin:                     jc.PumpMin,
		PumpMax:                     jc.PumpMax,
		ValveMin:                    jc.ValveMin,
		ValveMax:                    jc.ValveMax,
		MaxOptimizationTimeSeconds:  jc.MaxOptimizationTimeSeconds,
		MaxOptimizationTime:         time.Duration(jc.MaxOptimizationTimeSeconds) * time.Second,
		StagnationLimit:             jc.StagnationLimit,
	}

	return cfg, nil
}

func DefaultConfig() *OptimizerConfig {
	return &OptimizerConfig{
		PopulationSize:              100,
		MaxGenerations:              200,
		EliteCount:                  5,
		TournamentSize:              3,
		MutationRate:                0.1,
		CrossoverRate:               0.5,
		PumpMin:                     20.0,
		PumpMax:                     60.0,
		ValveMin:                    0.0,
		ValveMax:                    100.0,
		MaxOptimizationTimeSeconds:  5,
		MaxOptimizationTime:         5 * time.Second,
		StagnationLimit:             30,
	}
}
