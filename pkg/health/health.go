package health

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sync"
	"time"

	"github.com/prometheus/client_golang/prometheus/promhttp"
	"github.com/u16-io/FindSenryu4Discord/config"
	"github.com/u16-io/FindSenryu4Discord/db"
	"github.com/u16-io/FindSenryu4Discord/pkg/logger"
	"github.com/u16-io/FindSenryu4Discord/pkg/metrics"
)

// Server represents the health check HTTP server
type Server struct {
	server     *http.Server
	ready      bool
	readyMutex sync.RWMutex
	startTime  time.Time
}

// HealthResponse represents the health check response
type HealthResponse struct {
	Status    string            `json:"status"`
	Timestamp string            `json:"timestamp"`
	Uptime    string            `json:"uptime"`
	Checks    map[string]string `json:"checks"`
}

// NewServer creates a new health check server
func NewServer(port int) *Server {
	mux := http.NewServeMux()

	s := &Server{
		server: &http.Server{
			Addr:         fmt.Sprintf(":%d", port),
			Handler:      mux,
			ReadTimeout:  10 * time.Second,
			WriteTimeout: 10 * time.Second,
		},
		startTime: time.Now(),
	}

	mux.Handle("/metrics", promhttp.Handler())
	mux.HandleFunc("/health", s.healthHandler)
	mux.HandleFunc("/ready", s.readyHandler)
	mux.HandleFunc("/stats", s.statsHandler)

	return s
}

// Start starts the health check server
func (s *Server) Start() error {
	logger.Info("Starting health check server", "addr", s.server.Addr)
	go func() {
		if err := s.server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			logger.Error("Health check server error", "error", err)
		}
	}()
	return nil
}

// Stop stops the health check server
func (s *Server) Stop(ctx context.Context) error {
	logger.Info("Stopping health check server")
	return s.server.Shutdown(ctx)
}

// SetReady sets the ready state
func (s *Server) SetReady(ready bool) {
	s.readyMutex.Lock()
	defer s.readyMutex.Unlock()
	s.ready = ready
}

// IsReady returns the ready state
func (s *Server) IsReady() bool {
	s.readyMutex.RLock()
	defer s.readyMutex.RUnlock()
	return s.ready
}

func (s *Server) healthHandler(w http.ResponseWriter, r *http.Request) {
	checks := make(map[string]string)

	// Check database connection
	if db.IsConnected() {
		checks["database"] = "ok"
	} else {
		checks["database"] = "error"
	}

	status := "healthy"
	statusCode := http.StatusOK
	for _, v := range checks {
		if v != "ok" {
			status = "unhealthy"
			statusCode = http.StatusServiceUnavailable
			break
		}
	}

	response := HealthResponse{
		Status:    status,
		Timestamp: time.Now().UTC().Format(time.RFC3339),
		Uptime:    time.Since(s.startTime).String(),
		Checks:    checks,
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(statusCode)
	json.NewEncoder(w).Encode(response)
}

func (s *Server) readyHandler(w http.ResponseWriter, r *http.Request) {
	if s.IsReady() {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("ready"))
	} else {
		w.WriteHeader(http.StatusServiceUnavailable)
		w.Write([]byte("not ready"))
	}
}

func (s *Server) statsHandler(w http.ResponseWriter, r *http.Request) {
	dbStats := db.GetStats()

	// Update metrics
	metrics.SetDatabaseStats(dbStats.SenryuCount, dbStats.MutedChannelCount)

	stats := map[string]interface{}{
		"senryu_count":        dbStats.SenryuCount,
		"muted_channel_count": dbStats.MutedChannelCount,
		"database_connected":  dbStats.IsConnected,
		"uptime":              time.Since(s.startTime).String(),
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(stats)
}

// StartServer creates and starts the health check server if enabled
func StartServer() (*Server, error) {
	conf := config.GetConf()
	if !conf.Server.Enabled {
		logger.Info("Health check server is disabled")
		return nil, nil
	}

	s := NewServer(conf.Server.Port)
	if err := s.Start(); err != nil {
		return nil, err
	}
	return s, nil
}
