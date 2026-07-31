package main

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"goHttpServers/internal/auth"
	"goHttpServers/internal/database"
	"log"
	"net/http"
	"os"
	"slices"
	"strings"
	"sync/atomic"
	"time"

	"github.com/google/uuid"
	"github.com/joho/godotenv"
	_ "github.com/lib/pq"
)

type apiConfig struct {
	fileserverHits atomic.Int32
	dbQueries      *database.Queries
	platform       string
	jwtSecret      string
}

type User struct {
	ID          uuid.UUID `json:"id"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
	Email       string    `json:"email"`
	IsChirpyRed bool      `json:"is_chirpy_red"`
}

type DatosRecibidos struct {
	Email            string `json:"email"`
	Password         string `json:"password"`
	ExpiresInSeconds int    `json:"expires_in_seconds"`
}

type newChirpy struct {
	Body string `json:"body"`
}

type chirpResponse struct {
	ID        uuid.UUID `json:"id"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
	Body      string    `json:"body"`
	UserID    uuid.UUID `json:"user_id"`
}

type loginResponse struct {
	ID           uuid.UUID `json:"id"`
	CreatedAt    time.Time `json:"created_at"`
	UpdatedAt    time.Time `json:"updated_at"`
	Email        string    `json:"email"`
	Token        string    `json:"token"`
	RefreshToken string    `json:"refresh_token"`
	IsChirpyRed  bool      `json:"is_chirpy_red"`
}

func (cfg *apiConfig) middlewareMetricsInc(next http.Handler) http.Handler {

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		cfg.fileserverHits.Add(1)
		next.ServeHTTP(w, r)
	})
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
	platform := os.Getenv("PLATFORM")
	db, err := sql.Open("postgres", dbURL)
	if err != nil {
		log.Fatalf("Error al abrir la base de datos: %s", err)
	}
	jwtSecret := os.Getenv("JWT_SECRET")
	if jwtSecret == "" {
		log.Fatal("JWT_SECRET environment variable is not set")
	}
	defer db.Close()
	dbQueries := database.New(db)
	apiCfg := &apiConfig{
		dbQueries: dbQueries,
		platform:  platform,
		jwtSecret: jwtSecret,
	}

	mux := http.NewServeMux()

	fileServerHandler := http.FileServer(http.Dir("."))

	strippedHandler := http.StripPrefix("/app/", fileServerHandler)

	metricsHandler := apiCfg.middlewareMetricsInc(strippedHandler)

	mux.Handle("/app/", metricsHandler)

	mux.HandleFunc("GET /api/healthz", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.WriteHeader(http.StatusOK)
		mensaje := "OK"
		datosEnBytes := []byte(mensaje)
		w.Write(datosEnBytes)
	})

	mux.HandleFunc("POST /api/users", func(w http.ResponseWriter, r *http.Request) {
		defer r.Body.Close()
		// recibe el json email
		var datos DatosRecibidos
		err := json.NewDecoder(r.Body).Decode(&datos)
		if err != nil {
			http.Error(w, "JSON inválido", http.StatusBadRequest)
			return
		}

		datos.Password, err = auth.HashPassword(datos.Password)
		if err != nil {
			http.Error(w, "Problemas codificando password", http.StatusBadRequest)
			return
		}

		user, err := dbQueries.CreateUser(r.Context(), database.CreateUserParams{
			Email:          datos.Email,
			HashedPassword: datos.Password,
		})
		if err != nil {
			respondWithError(w, 404, "Something went wrong")
			return
		}
		nuevoUsuario := User{ID: user.ID, Email: user.Email, CreatedAt: user.CreatedAt, UpdatedAt: user.UpdatedAt}
		respondWithJSON(w, 201, nuevoUsuario)
		//devuelve el json user
	})

	mux.HandleFunc("POST /api/login", func(w http.ResponseWriter, r *http.Request) {
		defer r.Body.Close()

		var datos DatosRecibidos
		err := json.NewDecoder(r.Body).Decode(&datos)
		if err != nil {
			respondWithError(w, 400, "JSON inválido")
			return
		}

		user, err := dbQueries.GetUserByEmail(r.Context(), datos.Email)
		if err != nil {
			respondWithError(w, 401, "Incorrect email or password")
			return
		}

		match, err := auth.CheckPasswordHash(datos.Password, user.HashedPassword)
		if err != nil || !match {
			respondWithError(w, 401, "Incorrect email or password")
			return
		}

		expiresIn := time.Hour
		if datos.ExpiresInSeconds > 0 {
			requestedExpiration := time.Duration(datos.ExpiresInSeconds) * time.Second
			if requestedExpiration < time.Hour {
				expiresIn = requestedExpiration
			}
		}

		tokenString, err := auth.MakeJWT(user.ID, apiCfg.jwtSecret, expiresIn)
		if err != nil {
			respondWithError(w, 500, "Couldn't create JWT")
			return
		}

		refreshToken, err := auth.MakeRefreshToken()
		if err != nil {
			respondWithError(w, 500, "Couldn't create refresh token")
			return
		}

		_, err = dbQueries.CreateRefreshToken(r.Context(), database.CreateRefreshTokenParams{
			Token:     refreshToken,
			UserID:    user.ID,
			ExpiresAt: time.Now().UTC().Add(time.Hour * 24 * 60),
		})
		if err != nil {
			respondWithError(w, 500, "Couldn't save refresh token")
			return
		}

		usuarioLogueado := loginResponse{
			ID:           user.ID,
			Email:        user.Email,
			CreatedAt:    user.CreatedAt,
			UpdatedAt:    user.UpdatedAt,
			Token:        tokenString,
			RefreshToken: refreshToken,
			IsChirpyRed:  user.IsChirpyRed,
		}

		respondWithJSON(w, 200, usuarioLogueado)
	})

	mux.HandleFunc("POST /api/refresh", func(w http.ResponseWriter, r *http.Request) {
		tokenString, err := auth.GetBearerToken(r.Header)
		if err != nil {
			respondWithError(w, 401, "Couldn't find refresh token")
			return
		}

		// Buscamos el usuario asociado si el token es válido, no expiró y no fue revocado
		user, err := dbQueries.GetUserFromRefreshToken(r.Context(), tokenString)
		if err != nil {
			respondWithError(w, 401, "Couldn't get user from refresh token")
			return
		}

		// Generamos un nuevo Access Token de 1 hora
		accessToken, err := auth.MakeJWT(user.ID, apiCfg.jwtSecret, time.Hour)
		if err != nil {
			respondWithError(w, 500, "Couldn't create JWT")
			return
		}

		respondWithJSON(w, 200, struct {
			Token string `json:"token"`
		}{
			Token: accessToken,
		})
	})

	mux.HandleFunc("POST /api/revoke", func(w http.ResponseWriter, r *http.Request) {
		tokenString, err := auth.GetBearerToken(r.Header)
		if err != nil {
			respondWithError(w, 401, "Couldn't find refresh token")
			return
		}

		err = dbQueries.RevokeRefreshToken(r.Context(), tokenString)
		if err != nil {
			respondWithError(w, 500, "Couldn't revoke refresh token")
			return
		}

		w.WriteHeader(http.StatusNoContent)
	})

	mux.HandleFunc("PUT /api/users", func(w http.ResponseWriter, r *http.Request) {
		defer r.Body.Close()

		// 1. Extraer el token de los headers (Authentication)
		tokenString, err := auth.GetBearerToken(r.Header)
		if err != nil {
			respondWithError(w, 401, "Missing token")
			return
		}

		// 2. Validar el token y obtener el userID (Authorization)
		userID, err := auth.ValidateJWT(tokenString, apiCfg.jwtSecret)
		if err != nil {
			respondWithError(w, 401, "Invalid or expired token")
			return
		}

		// 3. Decodificar el body con los nuevos datos
		var params struct {
			Email    string `json:"email"`
			Password string `json:"password"`
		}
		err = json.NewDecoder(r.Body).Decode(&params)
		if err != nil {
			respondWithError(w, 400, "Invalid JSON")
			return
		}

		// 4. Hashear la nueva contraseña
		hashedPassword, err := auth.HashPassword(params.Password)
		if err != nil {
			respondWithError(w, 500, "Error hashing password")
			return
		}

		// 5. Actualizar el usuario en la base de datos
		// Usamos el userID extraído del token, ¡así es imposible que actualicen a otra persona!
		updatedUser, err := dbQueries.UpdateUser(r.Context(), database.UpdateUserParams{
			Email:          params.Email,
			HashedPassword: hashedPassword,
			ID:             userID,
		})
		if err != nil {
			respondWithError(w, 500, "Error updating user")
			return
		}

		// 6. Devolver el usuario actualizado (usamos el struct User normal que no expone la password)
		respondWithJSON(w, 200, User{
			ID:          updatedUser.ID,
			Email:       updatedUser.Email,
			CreatedAt:   updatedUser.CreatedAt,
			UpdatedAt:   updatedUser.UpdatedAt,
			IsChirpyRed: updatedUser.IsChirpyRed,
		})
	})

	mux.HandleFunc("POST /api/chirps", func(w http.ResponseWriter, r *http.Request) {

		// 1. Obtener el token del header Authorization
		tokenString, err := auth.GetBearerToken(r.Header)
		if err != nil {
			// Si no hay token, devolvemos 401 Unauthorized
			respondWithError(w, 401, "Couldn't find JWT")
			return
		}

		// 2. Validar el token y obtener el ID real del usuario
		userID, err := auth.ValidateJWT(tokenString, apiCfg.jwtSecret)
		if err != nil {
			// Si el token expiró o es falso, devolvemos 401
			respondWithError(w, 401, "Couldn't validate JWT")
			return
		}

		bad_words := []string{"kerfuffle", "sharbert", "fornax"}

		var newchirpy newChirpy
		err = json.NewDecoder(r.Body).Decode(&newchirpy)

		if err != nil {
			respondWithError(w, 500, "Error decoding chirpy")
			return
		}

		if len(newchirpy.Body) > 140 {
			respondWithError(w, 400, "Chirp is too long")
			return
		}

		// Limpieza de palabras
		words := strings.Split(newchirpy.Body, " ")
		respuestas := []string{}
		for _, word := range words {
			if slices.Contains(bad_words, strings.ToLower(word)) {
				word = "****"
			}
			respuestas = append(respuestas, word)
		}
		resultado := strings.Join(respuestas, " ")

		// 3. Crear el chirp usando el userID SEGURO que sacamos del JWT
		i, err := dbQueries.CreateChirp(r.Context(), database.CreateChirpParams{
			Body:   resultado,
			UserID: userID, // <-- ¡MAGIA AQUÍ! Ya no usamos newchirpy.User_id
		})

		if err != nil {
			respondWithError(w, 500, "Something went wrong")
			return
		}

		response := chirpResponse{
			ID:        i.ID,
			CreatedAt: i.CreatedAt,
			UpdatedAt: i.UpdatedAt,
			Body:      i.Body,
			UserID:    i.UserID,
		}

		respondWithJSON(w, 201, response)
	})

	mux.HandleFunc("DELETE /api/chirps/{chirpID}", func(w http.ResponseWriter, r *http.Request) {
		// 1. Obtener y parsear el ID de la URL
		chirpIDString := r.PathValue("chirpID")
		chirpID, err := uuid.Parse(chirpIDString)
		if err != nil {
			respondWithError(w, 400, "Invalid chirp ID")
			return
		}

		// 2. Extraer y validar el token (Autenticación)
		tokenString, err := auth.GetBearerToken(r.Header)
		if err != nil {
			respondWithError(w, 401, "Missing token")
			return
		}

		userID, err := auth.ValidateJWT(tokenString, apiCfg.jwtSecret)
		if err != nil {
			respondWithError(w, 401, "Invalid token")
			return
		}

		// 3. Buscar el chirp en la DB para ver si existe y quién es el dueño
		chirp, err := dbQueries.GetChirp(r.Context(), chirpID)
		if err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				respondWithError(w, 404, "Chirp not found")
				return
			}
			respondWithError(w, 500, "Error finding chirp")
			return
		}

		// 4. Verificar permisos (Autorización: ¿El usuario es el dueño?)
		if chirp.UserID != userID {
			respondWithError(w, 403, "You can only delete your own chirps")
			return
		}

		// 5. Eliminar el chirp
		err = dbQueries.DeleteChirp(r.Context(), chirpID)
		if err != nil {
			respondWithError(w, 500, "Could not delete chirp")
			return
		}

		// 6. Éxito: Enviar status 204 (No Content) sin body
		w.WriteHeader(http.StatusNoContent)
	})

	mux.HandleFunc("GET /api/chirps", func(w http.ResponseWriter, r *http.Request) {

		allChirp, err := dbQueries.GetChirps(r.Context())

		if err != nil {
			respondWithError(w, 400, "Something went wrong")
			return
		}

		chirpList := []chirpResponse{}

		for _, chori := range allChirp {
			// 4. Copiamos los datos al formato de respuesta JSON
			chirpList = append(chirpList, chirpResponse{
				ID:        chori.ID,
				CreatedAt: chori.CreatedAt,
				UpdatedAt: chori.UpdatedAt,
				Body:      chori.Body,
				UserID:    chori.UserID,
			})
		}
		respondWithJSON(w, 200, chirpList)

	})

	mux.HandleFunc("POST /api/polka/webhooks", func(w http.ResponseWriter, r *http.Request) {
		defer r.Body.Close()

		// 1. Estructura para leer el JSON que nos envía Polka
		type polkaWebhook struct {
			Event string `json:"event"`
			Data  struct {
				UserID uuid.UUID `json:"user_id"`
			} `json:"data"`
		}

		var req polkaWebhook
		err := json.NewDecoder(r.Body).Decode(&req)
		if err != nil {
			respondWithError(w, 400, "Invalid JSON")
			return
		}

		// 2. Si el evento NO es "user.upgraded", ignoramos y devolvemos 204
		if req.Event != "user.upgraded" {
			w.WriteHeader(http.StatusNoContent) // Status 204
			return
		}

		// 3. Si ES "user.upgraded", actualizamos al usuario en la DB
		_, err = dbQueries.UpgradeUserToRed(r.Context(), req.Data.UserID)
		if err != nil {
			// Si el error es que no encontró ninguna fila, es un 404
			if errors.Is(err, sql.ErrNoRows) {
				respondWithError(w, 404, "User not found")
				return
			}
			// Si es otro error de base de datos, es un 500
			respondWithError(w, 500, "Could not upgrade user")
			return
		}

		// 4. Éxito: devolvemos 204 sin cuerpo
		w.WriteHeader(http.StatusNoContent)
	})

	mux.HandleFunc("GET /api/chirps/{chirpID}", func(w http.ResponseWriter, r *http.Request) {

		chirpIDString := r.PathValue("chirpID")

		chirpIDUUID, err := uuid.Parse(chirpIDString)

		if err != nil {
			respondWithError(w, 404, "Chirp not found")
			return
		}

		oneChirp, err := dbQueries.GetChirp(r.Context(), chirpIDUUID)

		if err != nil {

			if errors.Is(err, sql.ErrNoRows) {
				respondWithError(w, 404, "Chirp not found")
				return
			}

			log.Printf("Error crítico en GetChirp: %v\n", err)

			// Los errores desconocidos de DB deberían ser 500 (Internal Server Error)
			respondWithError(w, 500, "Something went wrong")
			return
		}

		chirpResponse := chirpResponse{
			ID:        oneChirp.ID,
			CreatedAt: oneChirp.CreatedAt,
			UpdatedAt: oneChirp.UpdatedAt,
			Body:      oneChirp.Body,
			UserID:    oneChirp.UserID,
		}

		respondWithJSON(w, 200, chirpResponse)

	})

	mux.HandleFunc("GET /admin/metrics", func(w http.ResponseWriter, r *http.Request) {
		hits := apiCfg.fileserverHits.Load()
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.WriteHeader(http.StatusOK)
		mensaje := fmt.Sprintf("<html><body><h1>Welcome, Chirpy Admin</h1><p>Chirpy has been visited %d times!</p></body></html>", hits)
		datosEnBytes := []byte(mensaje)
		w.Write(datosEnBytes)
	})

	mux.HandleFunc("POST /admin/reset", func(w http.ResponseWriter, r *http.Request) {
		if apiCfg.platform != platform {
			respondWithError(w, http.StatusForbidden, "Forbidden")
			return
		}
		apiCfg.fileserverHits.Store(0)
		err := dbQueries.DeleteAllUsers(r.Context())
		if err != nil {
			respondWithError(w, 403, "Something went wrong")
			return
		}
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.WriteHeader(http.StatusOK)
	})

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
