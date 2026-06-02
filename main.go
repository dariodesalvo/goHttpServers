package main

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"goHttpServers/internal/database"
	"log"
	"net/http"
	"os"
	"slices"
	"strings"
	"sync/atomic"

	"github.com/joho/godotenv"
	_ "github.com/lib/pq"
)

type apiConfig struct {
	fileserverHits atomic.Int32
	dbQueries      *database.Queries
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

func respondWithError(w http.ResponseWriter, code int, msg string) {

	type body struct {
		Error string `json:"error"`
	}

	respondWithJSON(w, code, body{Error: msg})
}

func respondWithJSON(w http.ResponseWriter, code int, payload interface{}) {

	dat, err := json.Marshal(payload)

	if err != nil {
		log.Printf("Error marshalling JSON: %s", err)
		w.WriteHeader(500)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	w.Write(dat)
}

func main() {
	godotenv.Load()
	dbURL := os.Getenv("DB_URL")
	db, err := sql.Open("postgres", dbURL)
	if err != nil {
		log.Fatalf("Error al abrir la base de datos: %s", err)
	}
	defer db.Close()
	dbQueries := database.New(db)
	apiCfg := &apiConfig{
		dbQueries: dbQueries,
	}

	mux := http.NewServeMux()

	fileServerHandler := http.FileServer(http.Dir("."))

	strippedHandler := http.StripPrefix("/app/", fileServerHandler)

	metricsHandler := apiCfg.middlewareMetricsInc(strippedHandler)

	mux.Handle("/app/", metricsHandler)

	mux.HandleFunc("GET  /api/healthz", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.WriteHeader(http.StatusOK)
		mensaje := "OK"
		datosEnBytes := []byte(mensaje)
		w.Write(datosEnBytes)
	})

	mux.HandleFunc("POST  /api/validate_chirp", func(w http.ResponseWriter, r *http.Request) {

		bad_words := []string{"kerfuffle", "sharbert", "fornax"}

		type returnVals struct {
			Cleaned_body string `json:"cleaned_body"`
		}

		type parameters struct {
			Body string `json:"body"`
		}

		decoder := json.NewDecoder(r.Body)
		params := parameters{}
		err := decoder.Decode(&params)
		if err != nil {
			log.Printf("Error decoding parameters: %s", err)
			w.WriteHeader(500)
			return
		}

		if len(params.Body) > 140 {
			respondWithError(w, 400, "Something went wrong")
		} else {
			words := strings.Split(params.Body, " ")
			respuestas := []string{}
			for _, word := range words {
				if slices.Contains(bad_words, strings.ToLower(word)) {
					word = "****"
				}
				respuestas = append(respuestas, word)
			}
			resultado := strings.Join(respuestas, " ")
			respBody := returnVals{
				Cleaned_body: resultado,
			}

			respondWithJSON(w, 200, respBody)
		}
	})

	mux.HandleFunc("GET /admin/metrics", func(w http.ResponseWriter, r *http.Request) {
		hits := apiCfg.fileserverHits.Load()
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.WriteHeader(http.StatusOK)
		mensaje := fmt.Sprintf("<html><body><h1>Welcome, Chirpy Admin</h1><p>Chirpy has been visited %d times!</p></body></html>", hits)
		datosEnBytes := []byte(mensaje)
		w.Write(datosEnBytes)
	})

	mux.HandleFunc("POST /admin/reset", apiCfg.handlerReset)

	server := &http.Server{
		Addr:    ":8080",
		Handler: mux,
	}

	fmt.Println("Starting server on http://localhost:8080 ...")

	err = server.ListenAndServe()

	if err != nil && err != http.ErrServerClosed {
		log.Fatalf("Server failed to start: %v", err)
	}
}
