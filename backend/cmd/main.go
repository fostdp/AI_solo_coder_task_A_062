package main

import (
	"context"
	"log"
	"net/http"
	"net/http/pprof"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	"gas-drainage-system/internal/alarm_monitor"
	"gas-drainage-system/internal/command_dispatcher"
	"gas-drainage-system/internal/data_collector"
	"gas-drainage-system/internal/database"
	"gas-drainage-system/internal/handler"
	"gas-drainage-system/internal/network_optimizer"
)

func main() {
	cfg := database.Config{
		Host:     getEnv("DB_HOST", "localhost"),
		Port:     getEnvInt("DB_PORT", 5432),
		User:     getEnv("DB_USER", "postgres"),
		Password: getEnv("DB_PASSWORD", "postgres"),
		DBName:   getEnv("DB_NAME", "gas_drainage"),
		SSLMode:  getEnv("DB_SSLMODE", "disable"),
		MaxConns: int32(getEnvInt("DB_MAX_CONNS", 50)),
	}

	db, err := database.NewPool(cfg)
	if err != nil {
		log.Fatalf("Failed to connect to database: %v", err)
	}
	defer db.Close()
	log.Printf("Database pool initialized (max_conns=%d)", cfg.MaxConns)

	hub := handler.NewHub()
	go hub.Run()

	collector := data_collector.NewDataCollector(db, hub)
	go collector.Start()

	dispatcher := command_dispatcher.NewCommandDispatcher(
		getEnv("MQTT_BROKER", "tcp://localhost:1883"),
		"gas-drainage-server",
		db,
		hub,
	)

	optConfigPath := getEnv("OPTIMIZER_CONFIG", "config/optimizer.json")
	optCfg, err := network_optimizer.LoadConfig(optConfigPath)
	if err != nil {
		log.Printf("Failed to load optimizer config from %s: %v, using defaults", optConfigPath, err)
		optCfg = network_optimizer.DefaultConfig()
	}
	log.Printf("Optimizer config: pop=%d gens=%d stagnation=%d timeout=%vs",
		optCfg.PopulationSize, optCfg.MaxGenerations, optCfg.StagnationLimit,
		optCfg.MaxOptimizationTimeSeconds)

	optimizer := network_optimizer.NewNetworkOptimizer(db, hub, dispatcher, optCfg)
	go optimizer.Start(context.Background())

	monitor := alarm_monitor.NewAlarmMonitor(db, hub, 1*time.Minute)
	go monitor.Start(context.Background())

	h := handler.NewHandler(db, hub, collector, optimizer)

	mux := http.NewServeMux()
	h.RegisterRoutes(mux)

	staticDir := getEnv("STATIC_DIR", "../frontend/static")
	fs := http.FileServer(http.Dir(staticDir))
	mux.Handle("/", fs)

	pprofPort := getEnv("PPROF_PORT", "6060")
	pprofMux := http.NewServeMux()
	pprofMux.HandleFunc("/debug/pprof/", pprof.Index)
	pprofMux.HandleFunc("/debug/pprof/cmdline", pprof.Cmdline)
	pprofMux.HandleFunc("/debug/pprof/profile", pprof.Profile)
	pprofMux.HandleFunc("/debug/pprof/symbol", pprof.Symbol)
	pprofMux.HandleFunc("/debug/pprof/trace", pprof.Trace)
	go func() {
		log.Printf("pprof listening on :%s", pprofPort)
		if err := http.ListenAndServe(":"+pprofPort, pprofMux); err != nil {
			log.Printf("pprof server error: %v", err)
		}
	}()

	port := getEnv("SERVER_PORT", "8080")
	srv := &http.Server{
		Addr:    ":" + port,
		Handler: corsMiddleware(mux),
	}

	go func() {
		log.Printf("Server starting on port %s", port)
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("Server failed: %v", err)
		}
	}()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go func() {
		if err := dispatcher.Start(ctx); err != nil {
			log.Printf("MQTT dispatcher error: %v", err)
		}
	}()

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	<-sigCh

	log.Println("Shutting down...")
	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer shutdownCancel()
	srv.Shutdown(shutdownCtx)
	cancel()
	time.Sleep(2 * time.Second)
	log.Println("Server stopped")
}

func corsMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type")
		if r.Method == "OPTIONS" {
			w.WriteHeader(http.StatusOK)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func getEnv(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func getEnvInt(key string, fallback int) int {
	if v := os.Getenv(key); v != "" {
		if i, err := strconv.Atoi(v); err == nil {
			return i
		}
	}
	return fallback
}
