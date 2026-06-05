package main

import (
	"gas-drainage-system/internal/alert"
	"gas-drainage-system/internal/database"
	"gas-drainage-system/internal/handler"
	"gas-drainage-system/internal/mqtt"
	"gas-drainage-system/internal/optimizer"
	"log"
	"net/http"
	"os"
	"strconv"
	"time"
)

func main() {
	cfg := database.Config{
		Host:     getEnv("DB_HOST", "localhost"),
		Port:     getEnvInt("DB_PORT", 5432),
		User:     getEnv("DB_USER", "postgres"),
		Password: getEnv("DB_PASSWORD", "postgres"),
		DBName:   getEnv("DB_NAME", "gas_drainage"),
		SSLMode:  getEnv("DB_SSLMODE", "disable"),
	}

	db, err := database.NewPool(cfg)
	if err != nil {
		log.Fatalf("Failed to connect to database: %v", err)
	}
	defer db.Close()

	hub := handler.NewHub()
	go hub.Run()

	mqttClient := mqtt.NewClient(
		getEnv("MQTT_BROKER", "tcp://localhost:1883"),
		"gas-drainage-server",
	)
	mqttClient.SetDB(db)
	mqttClient.SetHub(hub)
	go mqttClient.Start()

	alertEngine := alert.NewEngine(db, hub, 1*time.Minute)
	go alertEngine.Start()

	opt := optimizer.NewOptimizer(db, hub, mqttClient)

	h := handler.NewHandler(db, hub, opt)

	mux := http.NewServeMux()
	h.RegisterRoutes(mux)

	fs := http.FileServer(http.Dir("../frontend/static"))
	mux.Handle("/", fs)

	port := getEnv("SERVER_PORT", "8080")
	log.Printf("Server starting on port %s", port)
	if err := http.ListenAndServe(":"+port, corsMiddleware(mux)); err != nil {
		log.Fatalf("Server failed: %v", err)
	}
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
