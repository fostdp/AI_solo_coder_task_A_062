package optimizer

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"math"
	"math/rand"
	"sort"
	"sync"
	"time"

	"gas-drainage-system/internal/database"
	"gas-drainage-system/internal/mqtt"
	"gas-drainage-system/internal/models"
)

type Broadcaster interface {
	Broadcast(msgType string, payload interface{})
}

const (
	populationSize     = 100
	maxGenerations     = 200
	eliteCount         = 5
	tournamentSize     = 3
	mutationRate       = 0.1
	pumpMin            = 20.0
	pumpMax            = 60.0
	valveMin           = 0.0
	valveMax           = 100.0
	maxOptimizationTime = 5 * time.Second
	stagnationLimit    = 30
)

type Optimizer struct {
	db         *database.DB
	hub        Broadcaster
	mqttClient *mqtt.Client
	mu         sync.Mutex
	running    bool
}

type Chromosome struct {
	PumpPressures [3]float64 `json:"pump_pressures"`
	ValveOpenings []float64  `json:"valve_openings"`
}

type OptimizationOutput struct {
	BestChromosome           Chromosome       `json:"best_chromosome"`
	Fitness                  float64         `json:"fitness"`
	Generations              int             `json:"generations"`
	TimedOut                 bool            `json:"timed_out"`
	RecommendedPumpPressures [3]float64      `json:"recommended_pump_pressures"`
	RecommendedValveOpenings []float64       `json:"recommended_valve_openings"`
	Recommendations          []Recommendation `json:"recommendations"`
	TotalConcentration       float64         `json:"total_concentration"`
}

type Recommendation struct {
	TargetType   string  `json:"target_type"`
	TargetID     int     `json:"target_id"`
	CommandType  string  `json:"command_type"`
	CurrentValue float64 `json:"current_value"`
	CommandValue float64 `json:"command_value"`
}

type boreholeInfo struct {
	ID             int
	PumpStationIdx int
	CurrentFlow    float64
	CurrentConc    float64
	BasePressure   float64
	ValveOpening   float64
}

type stationGroup struct {
	Idx       int
	Boreholes []int
}

type scoredChromosome struct {
	chromosome Chromosome
	fitness    float64
	groupCache [3]float64
}

func NewOptimizer(db *database.DB, hub Broadcaster, mqttClient *mqtt.Client) *Optimizer {
	return &Optimizer{
		db:         db,
		hub:        hub,
		mqttClient: mqttClient,
	}
}

func (o *Optimizer) Run(ctx context.Context) (*OptimizationOutput, error) {
	o.mu.Lock()
	if o.running {
		o.mu.Unlock()
		return nil, fmt.Errorf("optimization already running")
	}
	o.running = true
	o.mu.Unlock()
	defer func() {
		o.mu.Lock()
		o.running = false
		o.mu.Unlock()
	}()

	boreholes, err := o.fetchBoreholeData(ctx)
	if err != nil {
		return nil, fmt.Errorf("fetch borehole data: %w", err)
	}
	if len(boreholes) == 0 {
		return nil, fmt.Errorf("no borehole data available")
	}

	groups := o.buildStationGroups(boreholes)

	deadline := time.Now().Add(maxOptimizationTime)
	ctx, cancel := context.WithDeadline(ctx, deadline)
	defer cancel()

	pop := o.initPopulation(len(boreholes))
	scored := o.evaluate(pop, boreholes, groups)
	sort.Slice(scored, func(i, j int) bool {
		return scored[i].fitness > scored[j].fitness
	})

	bestEver := scored[0]
	stagnationCount := 0

	var gen int
	for gen = 1; gen < maxGenerations; gen++ {
		select {
		case <-ctx.Done():
			goto done
		default:
		}

		newPop := make([]Chromosome, 0, populationSize)
		for i := 0; i < eliteCount; i++ {
			newPop = append(newPop, scored[i].chromosome)
		}

		for len(newPop) < populationSize {
			p1 := o.tournamentSelect(scored)
			p2 := o.tournamentSelect(scored)
			child := o.crossover(p1, p2)
			o.mutate(&child)
			newPop = append(newPop, child)
		}

		pop = newPop
		scored = o.evaluateIncremental(pop, scored[:eliteCount], boreholes, groups)
		sort.Slice(scored, func(i, j int) bool {
			return scored[i].fitness > scored[j].fitness
		})

		if scored[0].fitness > bestEver.fitness {
			bestEver = scored[0]
			stagnationCount = 0
		} else {
			stagnationCount++
		}

		if stagnationCount >= stagnationLimit {
			log.Printf("GA stagnation at gen %d, using best-so-far (fitness=%.4f)", gen, bestEver.fitness)
			goto done
		}
	}

done:
	best := bestEver

	recs := o.buildRecommendations(ctx, best.chromosome, boreholes)

	result := &OptimizationOutput{
		BestChromosome:           best.chromosome,
		Fitness:                  best.fitness,
		Generations:              gen,
		TimedOut:                 gen < maxGenerations,
		RecommendedPumpPressures: best.chromosome.PumpPressures,
		RecommendedValveOpenings: best.chromosome.ValveOpenings,
		Recommendations:          recs,
		TotalConcentration:       best.fitness,
	}

	o.publishAndStore(ctx, result, boreholes)

	o.hub.Broadcast("optimization", result)
	return result, nil
}

func (o *Optimizer) buildStationGroups(boreholes []boreholeInfo) [3]stationGroup {
	var groups [3]stationGroup
	for i := range groups {
		groups[i] = stationGroup{Idx: i}
	}
	for idx, bh := range boreholes {
		groups[bh.PumpStationIdx].Boreholes = append(groups[bh.PumpStationIdx].Boreholes, idx)
	}
	return groups
}

func (o *Optimizer) computeFitness(chrom Chromosome, boreholes []boreholeInfo, groups [3]stationGroup) (float64, [3]float64) {
	var cache [3]float64
	var totalFlow, weightedConc float64
	for gi, group := range groups {
		pumpPressure := chrom.PumpPressures[gi]
		for _, bhIdx := range group.Boreholes {
			bh := boreholes[bhIdx]
			valveOpening := clamp(chrom.ValveOpenings[bhIdx], valveMin, valveMax)
			pressureRatio := pumpPressure / bh.BasePressure
			flowAdjusted := bh.CurrentFlow * (valveOpening / 100.0) * pressureRatio
			concAdjusted := bh.CurrentConc * math.Pow(pressureRatio, 0.3)
			totalFlow += flowAdjusted
			weightedConc += concAdjusted * flowAdjusted
		}
		cache[gi] = pumpPressure
	}
	if totalFlow == 0 {
		return 0, cache
	}
	return weightedConc / totalFlow, cache
}

func (o *Optimizer) evaluate(pop []Chromosome, boreholes []boreholeInfo, groups [3]stationGroup) []scoredChromosome {
	result := make([]scoredChromosome, len(pop))
	for i, chrom := range pop {
		fitness, cache := o.computeFitness(chrom, boreholes, groups)
		result[i] = scoredChromosome{
			chromosome: chrom,
			fitness:    fitness,
			groupCache: cache,
		}
	}
	return result
}

func (o *Optimizer) evaluateIncremental(pop []Chromosome, elites []scoredChromosome, boreholes []boreholeInfo, groups [3]stationGroup) []scoredChromosome {
	result := make([]scoredChromosome, len(pop))
	for i, chrom := range pop {
		if i < len(elites) {
			result[i] = elites[i]
			continue
		}
		fitness, cache := o.computeFitness(chrom, boreholes, groups)
		result[i] = scoredChromosome{
			chromosome: chrom,
			fitness:    fitness,
			groupCache: cache,
		}
	}
	return result
}

func (o *Optimizer) buildRecommendations(ctx context.Context, chrom Chromosome, boreholes []boreholeInfo) []Recommendation {
	var recs []Recommendation
	for i, p := range chrom.PumpPressures {
		var currentP float64
		o.db.Pool.QueryRow(ctx, "SELECT negative_pressure FROM pump_stations WHERE id = $1", i+1).Scan(&currentP)
		recs = append(recs, Recommendation{
			TargetType:   "pump_station",
			TargetID:     i + 1,
			CommandType:  "set_pressure",
			CurrentValue: currentP,
			CommandValue: p,
		})
	}
	for i, v := range chrom.ValveOpenings {
		if i < len(boreholes) {
			recs = append(recs, Recommendation{
				TargetType:   "borehole",
				TargetID:     boreholes[i].ID,
				CommandType:  "set_valve",
				CurrentValue: boreholes[i].ValveOpening,
				CommandValue: v,
			})
		}
	}
	return recs
}

func (o *Optimizer) publishAndStore(ctx context.Context, result *OptimizationOutput, boreholes []boreholeInfo) {
	for i, p := range result.BestChromosome.PumpPressures {
		if err := o.mqttClient.PublishCommandWithAck(ctx, "pump_station", i+1, map[string]interface{}{
			"command_type":  "set_pressure",
			"command_value": p,
		}); err != nil {
			log.Printf("publish pump pressure command error: %v", err)
		}
	}
	for i, v := range result.BestChromosome.ValveOpenings {
		if i < len(boreholes) {
			if err := o.mqttClient.PublishCommandWithAck(ctx, "borehole", boreholes[i].ID, map[string]interface{}{
				"command_type":  "set_valve",
				"command_value": v,
			}); err != nil {
				log.Printf("publish valve opening command error: %v", err)
			}
		}
	}

	resultJSON, _ := json.Marshal(result)
	_, err := o.db.Pool.Exec(ctx,
		`INSERT INTO optimization_results (result, total_concentration, created_at) VALUES ($1, $2, NOW())`,
		string(resultJSON), result.Fitness,
	)
	if err != nil {
		log.Printf("store optimization result error: %v", err)
	}

	for _, rec := range result.Recommendations {
		topic := fmt.Sprintf("gas/plc/%s/%d/command", rec.TargetType, rec.TargetID)
		_, err := o.db.Pool.Exec(ctx,
			`INSERT INTO plc_commands (target_type, target_id, command_type, command_value, status, mqtt_topic, created_at) VALUES ($1, $2, $3, $4, $5, $6, NOW())`,
			rec.TargetType, rec.TargetID, rec.CommandType, rec.CommandValue, "sent", topic,
		)
		if err != nil {
			log.Printf("store plc command error: %v", err)
		}
	}
}

func (o *Optimizer) fetchBoreholeData(ctx context.Context) ([]boreholeInfo, error) {
	pumpIndexMap := map[int]int{1: 0, 2: 1, 3: 2}
	rows, err := o.db.Pool.Query(ctx, `
		SELECT b.id, b.pump_station_id, b.valve_opening,
			COALESCE(bd.gas_flow, 0), COALESCE(bd.gas_concentration, 0),
			COALESCE(ps.negative_pressure, 40.0)
		FROM boreholes b
		LEFT JOIN LATERAL (
			SELECT gas_flow, gas_concentration FROM borehole_data
			WHERE borehole_id = b.id ORDER BY recorded_at DESC LIMIT 1
		) bd ON true
		JOIN pump_stations ps ON b.pump_station_id = ps.id
		ORDER BY b.id
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var result []boreholeInfo
	for rows.Next() {
		var bh boreholeInfo
		var pumpStationID int
		if err := rows.Scan(&bh.ID, &pumpStationID, &bh.ValveOpening, &bh.CurrentFlow, &bh.CurrentConc, &bh.BasePressure); err != nil {
			return nil, err
		}
		bh.PumpStationIdx = pumpIndexMap[pumpStationID]
		result = append(result, bh)
	}
	return result, rows.Err()
}

func (o *Optimizer) initPopulation(numBoreholes int) []Chromosome {
	pop := make([]Chromosome, populationSize)
	for i := range pop {
		for j := 0; j < 3; j++ {
			pop[i].PumpPressures[j] = pumpMin + rand.Float64()*(pumpMax-pumpMin)
		}
		pop[i].ValveOpenings = make([]float64, numBoreholes)
		for j := range pop[i].ValveOpenings {
			pop[i].ValveOpenings[j] = valveMin + rand.Float64()*(valveMax-valveMin)
		}
	}
	return pop
}

func (o *Optimizer) tournamentSelect(scored []scoredChromosome) Chromosome {
	best := scored[rand.Intn(len(scored))]
	for i := 1; i < tournamentSize; i++ {
		candidate := scored[rand.Intn(len(scored))]
		if candidate.fitness > best.fitness {
			best = candidate
		}
	}
	return best.chromosome
}

func (o *Optimizer) crossover(p1, p2 Chromosome) Chromosome {
	child := Chromosome{
		ValveOpenings: make([]float64, len(p1.ValveOpenings)),
	}
	for i := 0; i < 3; i++ {
		if rand.Float64() < 0.5 {
			child.PumpPressures[i] = p1.PumpPressures[i]
		} else {
			child.PumpPressures[i] = p2.PumpPressures[i]
		}
	}
	for i := range child.ValveOpenings {
		if rand.Float64() < 0.5 {
			child.ValveOpenings[i] = p1.ValveOpenings[i]
		} else {
			child.ValveOpenings[i] = p2.ValveOpenings[i]
		}
	}
	return child
}

func (o *Optimizer) mutate(chrom *Chromosome) {
	pumpRange := pumpMax - pumpMin
	pumpStd := 0.05 * pumpRange
	for i := 0; i < 3; i++ {
		if rand.Float64() < mutationRate {
			chrom.PumpPressures[i] += gaussianRandom() * pumpStd
			chrom.PumpPressures[i] = clamp(chrom.PumpPressures[i], pumpMin, pumpMax)
		}
	}
	valveRange := valveMax - valveMin
	valveStd := 0.05 * valveRange
	for i := range chrom.ValveOpenings {
		if rand.Float64() < mutationRate {
			chrom.ValveOpenings[i] += gaussianRandom() * valveStd
			chrom.ValveOpenings[i] = clamp(chrom.ValveOpenings[i], valveMin, valveMax)
		}
	}
}

func gaussianRandom() float64 {
	u1 := rand.Float64()
	u2 := rand.Float64()
	for u1 == 0 {
		u1 = rand.Float64()
	}
	return math.Sqrt(-2*math.Log(u1)) * math.Cos(2*math.Pi*u2)
}

func clamp(value, minVal, maxVal float64) float64 {
	if value < minVal {
		return minVal
	}
	if value > maxVal {
		return maxVal
	}
	return value
}

func (o *Optimizer) RunSync(ctx context.Context) *models.OptimizationResult {
	out, err := o.Run(ctx)
	if err != nil {
		log.Printf("optimization error: %v", err)
		return nil
	}
	resultJSON, _ := json.Marshal(out)
	return &models.OptimizationResult{
		Result:             resultJSON,
		TotalConcentration: out.TotalConcentration,
	}
}
