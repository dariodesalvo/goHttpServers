package main

import (
	"fmt"
	"log"
	"net/http"
	"sync/atomic"
)

type apiConfig struct {
	fileserverHits atomic.Int32
}

func (cfg *apiConfig) middlewareMetricsInc(next http.Handler) http.Handler {

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		cfg.fileserverHits.Add(1)
		next.ServeHTTP(w, r)
	})
}

func (cfg *apiConfig) handlerReset(w http.ResponseWriter, r *http.Request) {
	cfg.fileserverHits.Store(0)
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.WriteHeader(http.StatusOK)
}

func main() {

	apiCfg := &apiConfig{}

	mux := http.NewServeMux()

	fileServerHandler := http.FileServer(http.Dir("."))

	strippedHandler := http.StripPrefix("/app/", fileServerHandler)

	metricsHandler := apiCfg.middlewareMetricsInc(strippedHandler)

	mux.Handle("/app/", metricsHandler)

	mux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.WriteHeader(http.StatusOK)
		mensaje := "OK"
		datosEnBytes := []byte(mensaje)
		w.Write(datosEnBytes)
	})

	mux.HandleFunc("/metrics", func(w http.ResponseWriter, r *http.Request) {
		hits := apiCfg.fileserverHits.Load()
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.WriteHeader(http.StatusOK)
		mensaje := fmt.Sprintf("Hits: %d", hits)
		datosEnBytes := []byte(mensaje)
		w.Write(datosEnBytes)
	})

	mux.HandleFunc("/reset", apiCfg.handlerReset)

	server := &http.Server{
		Addr:    ":8080",
		Handler: mux,
	}

	fmt.Println("Starting server on http://localhost:8080 ...")

	err := server.ListenAndServe()

	if err != nil && err != http.ErrServerClosed {
		log.Fatalf("Server failed to start: %v", err)
	}
}
