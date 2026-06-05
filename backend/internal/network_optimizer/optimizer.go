package network_optimizer

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
)

type Broadcaster interface {
	Broadcast(msgType string, payload interface{})
}

type CommandDispatcher interface {
	Dispatch(ctx context.Context, targetType string, targetID int, command interface{}) error
}

type OptimizeRequest struct {
	Response chan *OptimizeResponse
}

type OptimizeResponse struct {
	Result *OptimizationOutput
	Error  error
}

type Chromosome struct {
	PumpPressures []float64 `json:"pump_pressures"`
	ValveOpenings []float64 `json:"valve_openings"`
}

type OptimizationOutput struct {
	BestChromosome           Chromosome        `json:"best_chromosome"`
	Fitness                  float64           `json:"fitness"`
	Generations              int               `json:"generations"`
	TimedOut                 bool              `json:"timed_out"`
	RecommendedPumpPressures []float64         `json:"recommended_pump_pressures"`
	RecommendedValveOpenings []float64         `json:"recommended_valve_openings"`
	Recommendations          []Recommendation  `json:"recommendations"`
	TotalConcentration       float64           `json:"total_concentration"`
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
	PumpStationID  int
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
	groupCache []float64
}

type NetworkOptimizer struct {
	db         *database.DB
	hub        Broadcaster
	dispatcher CommandDispatcher
	config     *OptimizerConfig
	mu         sync.Mutex
	running    bool
	OptimizeCh chan *OptimizeRequest
}

func NewNetworkOptimizer(db *database.DB, hub Broadcaster, dispatcher CommandDispatcher, cfg *OptimizerConfig) *NetworkOptimizer {
	if cfg == nil {
		cfg = DefaultConfig()
	}
	return &NetworkOptimizer{
		db:         db,
		hub:        hub,
		dispatcher: dispatcher,
		config:     cfg,
		OptimizeCh: make(chan *OptimizeRequest, 8),
	}
}

func (o *NetworkOptimizer) Start(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case req := <-o.OptimizeCh:
			result, err := o.Run(ctx)
			if err != nil {
				log.Printf("optimization error: %v", err)
			}
			if req.Response != nil {
				select {
				case req.Response <- &OptimizeResponse{Result: result, Error: err}:
				default:
				}
			}
			if result != nil {
				o.hub.Broadcast("optimization", result)
			}
		}
	}
}

func (o *NetworkOptimizer) Run(ctx context.Context) (*OptimizationOutput, error) {
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

	deadline := time.Now().Add(o.config.MaxOptimizationTime)
	ctx, cancel := context.WithDeadline(ctx, deadline)
	defer cancel()

	pop := o.initPopulation(len(boreholes), len(groups))
	scored := o.evaluate(pop, boreholes, groups)
	sort.Slice(scored, func(i, j int) bool {
		return scored[i].fitness > scored[j].fitness
	})

	bestEver := scored[0]
	stagnationCount := 0

	var gen int
	for gen = 1; gen < o.config.MaxGenerations; gen++ {
		select {
		case <-ctx.Done():
			goto done
		default:
		}

		newPop := make([]Chromosome, 0, o.config.PopulationSize)
		for i := 0; i < o.config.EliteCount; i++ {
			newPop = append(newPop, scored[i].chromosome)
		}

		for len(newPop) < o.config.PopulationSize {
			p1 := o.tournamentSelect(scored)
			p2 := o.tournamentSelect(scored)
			child := o.crossover(p1, p2)
			o.mutate(&child)
			newPop = append(newPop, child)
		}

		pop = newPop
		scored = o.evaluateIncremental(pop, scored[:o.config.EliteCount], boreholes, groups)
		sort.Slice(scored, func(i, j int) bool {
			return scored[i].fitness > scored[j].fitness
		})

		if scored[0].fitness > bestEver.fitness {
			bestEver = scored[0]
			stagnationCount = 0
		} else {
			stagnationCount++
		}

		if stagnationCount >= o.config.StagnationLimit {
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
		TimedOut:                 gen < o.config.MaxGenerations,
		RecommendedPumpPressures: best.chromosome.PumpPressures,
		RecommendedValveOpenings: best.chromosome.ValveOpenings,
		Recommendations:          recs,
		TotalConcentration:       best.fitness,
	}

	o.publishAndStore(ctx, result, boreholes)

	o.hub.Broadcast("optimization", result)
	return result, nil
}

func (o *NetworkOptimizer) initPopulation(numBoreholes int, numPumpStations int) []Chromosome {
	pop := make([]Chromosome, o.config.PopulationSize)
	for i := range pop {
		pop[i].PumpPressures = make([]float64, numPumpStations)
		for j := 0; j < numPumpStations; j++ {
			pop[i].PumpPressures[j] = o.config.PumpMin + rand.Float64()*(o.config.PumpMax-o.config.PumpMin)
		}
		pop[i].ValveOpenings = make([]float64, numBoreholes)
		for j := range pop[i].ValveOpenings {
			pop[i].ValveOpenings[j] = o.config.ValveMin + rand.Float64()*(o.config.ValveMax-o.config.ValveMin)
		}
	}
	return pop
}

func (o *NetworkOptimizer) tournamentSelect(scored []scoredChromosome) Chromosome {
	best := scored[rand.Intn(len(scored))]
	for i := 1; i < o.config.TournamentSize; i++ {
		candidate := scored[rand.Intn(len(scored))]
		if candidate.fitness > best.fitness {
			best = candidate
		}
	}
	return best.chromosome
}

func (o *NetworkOptimizer) crossover(p1, p2 Chromosome) Chromosome {
	child := Chromosome{
		PumpPressures: make([]float64, len(p1.PumpPressures)),
		ValveOpenings: make([]float64, len(p1.ValveOpenings)),
	}
	for i := range child.PumpPressures {
		if rand.Float64() < o.config.CrossoverRate {
			child.PumpPressures[i] = p1.PumpPressures[i]
		} else {
			child.PumpPressures[i] = p2.PumpPressures[i]
		}
	}
	for i := range child.ValveOpenings {
		if rand.Float64() < o.config.CrossoverRate {
			child.ValveOpenings[i] = p1.ValveOpenings[i]
		} else {
			child.ValveOpenings[i] = p2.ValveOpenings[i]
		}
	}
	return child
}

func (o *NetworkOptimizer) mutate(chrom *Chromosome) {
	pumpRange := o.config.PumpMax - o.config.PumpMin
	pumpStd := 0.05 * pumpRange
	for i := range chrom.PumpPressures {
		if rand.Float64() < o.config.MutationRate {
			chrom.PumpPressures[i] += gaussianRandom() * pumpStd
			chrom.PumpPressures[i] = clamp(chrom.PumpPressures[i], o.config.PumpMin, o.config.PumpMax)
		}
	}
	valveRange := o.config.ValveMax - o.config.ValveMin
	valveStd := 0.05 * valveRange
	for i := range chrom.ValveOpenings {
		if rand.Float64() < o.config.MutationRate {
			chrom.ValveOpenings[i] += gaussianRandom() * valveStd
			chrom.ValveOpenings[i] = clamp(chrom.ValveOpenings[i], o.config.ValveMin, o.config.ValveMax)
		}
	}
}

func (o *NetworkOptimizer) computeFitness(chrom Chromosome, boreholes []boreholeInfo, groups []stationGroup) (float64, []float64) {
	cache := make([]float64, len(groups))
	var totalFlow, weightedConc float64
	for gi, group := range groups {
		pumpPressure := chrom.PumpPressures[gi]
		for _, bhIdx := range group.Boreholes {
			bh := boreholes[bhIdx]
			valveOpening := clamp(chrom.ValveOpenings[bhIdx], o.config.ValveMin, o.config.ValveMax)
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

func (o *NetworkOptimizer) evaluate(pop []Chromosome, boreholes []boreholeInfo, groups []stationGroup) []scoredChromosome {
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

func (o *NetworkOptimizer) evaluateIncremental(pop []Chromosome, elites []scoredChromosome, boreholes []boreholeInfo, groups []stationGroup) []scoredChromosome {
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

func (o *NetworkOptimizer) buildRecommendations(ctx context.Context, chrom Chromosome, boreholes []boreholeInfo) []Recommendation {
	pumpStationIDs := make(map[int]int)
	for _, bh := range boreholes {
		if _, exists := pumpStationIDs[bh.PumpStationIdx]; !exists {
			pumpStationIDs[bh.PumpStationIdx] = bh.PumpStationID
		}
	}

	var recs []Recommendation
	for i, p := range chrom.PumpPressures {
		stationID := pumpStationIDs[i]
		var currentP float64
		o.db.Pool.QueryRow(ctx, "SELECT negative_pressure FROM pump_stations WHERE id = $1", stationID).Scan(&currentP)
		recs = append(recs, Recommendation{
			TargetType:   "pump_station",
			TargetID:     stationID,
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

func (o *NetworkOptimizer) buildStationGroups(boreholes []boreholeInfo) []stationGroup {
	groupMap := make(map[int]*stationGroup)
	for idx, bh := range boreholes {
		if _, exists := groupMap[bh.PumpStationIdx]; !exists {
			groupMap[bh.PumpStationIdx] = &stationGroup{Idx: bh.PumpStationIdx}
		}
		groupMap[bh.PumpStationIdx].Boreholes = append(groupMap[bh.PumpStationIdx].Boreholes, idx)
	}
	groups := make([]stationGroup, 0, len(groupMap))
	for i := 0; i < len(groupMap); i++ {
		groups = append(groups, *groupMap[i])
	}
	return groups
}

func (o *NetworkOptimizer) publishAndStore(ctx context.Context, result *OptimizationOutput, boreholes []boreholeInfo) {
	pumpStationIDs := make(map[int]int)
	for _, bh := range boreholes {
		if _, exists := pumpStationIDs[bh.PumpStationIdx]; !exists {
			pumpStationIDs[bh.PumpStationIdx] = bh.PumpStationID
		}
	}

	for i, p := range result.BestChromosome.PumpPressures {
		stationID := pumpStationIDs[i]
		err := o.dispatcher.Dispatch(ctx, "pump_station", stationID, map[string]interface{}{
			"command_type":  "set_pressure",
			"command_value": p,
		})
		if err != nil {
			log.Printf("dispatch pump pressure command error: %v", err)
		}
	}
	for i, v := range result.BestChromosome.ValveOpenings {
		if i < len(boreholes) {
			err := o.dispatcher.Dispatch(ctx, "borehole", boreholes[i].ID, map[string]interface{}{
				"command_type":  "set_valve",
				"command_value": v,
			})
			if err != nil {
				log.Printf("dispatch valve opening command error: %v", err)
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

func (o *NetworkOptimizer) fetchBoreholeData(ctx context.Context) ([]boreholeInfo, error) {
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
	pumpIndexMap := make(map[int]int)
	for rows.Next() {
		var bh boreholeInfo
		var pumpStationID int
		if err := rows.Scan(&bh.ID, &pumpStationID, &bh.ValveOpening, &bh.CurrentFlow, &bh.CurrentConc, &bh.BasePressure); err != nil {
			return nil, err
		}
		bh.PumpStationID = pumpStationID
		if _, exists := pumpIndexMap[pumpStationID]; !exists {
			pumpIndexMap[pumpStationID] = len(pumpIndexMap)
		}
		bh.PumpStationIdx = pumpIndexMap[pumpStationID]
		result = append(result, bh)
	}
	return result, rows.Err()
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
