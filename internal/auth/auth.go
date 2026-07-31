package auth

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/alexedwards/argon2id"
	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
)

func HashPassword(password string) (string, error) {

	hash, err := argon2id.CreateHash(password, argon2id.DefaultParams)

	return hash, err
}

func CheckPasswordHash(password, hash string) (bool, error) {
	comp, err := argon2id.ComparePasswordAndHash(password, hash)
	return comp, err
}

// MakeJWT crea un nuevo token firmado
func MakeJWT(userID uuid.UUID, tokenSecret string, expiresIn time.Duration) (string, error) {
	// 1. Configuramos los "claims" (los datos que van dentro del token)
	claims := jwt.RegisteredClaims{
		Issuer:    "chirpy-access",
		IssuedAt:  jwt.NewNumericDate(time.Now().UTC()),
		ExpiresAt: jwt.NewNumericDate(time.Now().UTC().Add(expiresIn)),
		Subject:   userID.String(),
	}

	// 2. Creamos el token usando el algoritmo HS256
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)

	// 3. Firmamos el token con nuestro secreto.
	// La librería exige que el secreto sea de tipo []byte para HS256.
	return token.SignedString([]byte(tokenSecret))
}

// ValidateJWT verifica el token y extrae el ID del usuario
func ValidateJWT(tokenString, tokenSecret string) (uuid.UUID, error) {
	claimsStruct := jwt.RegisteredClaims{}

	// 1. Parseamos el token. La función anónima verifica que el algoritmo de firma sea el correcto
	// y le pasa nuestra clave secreta para comprobar la firma.
	token, err := jwt.ParseWithClaims(
		tokenString,
		&claimsStruct,
		func(token *jwt.Token) (interface{}, error) {
			// Validamos que el método de firma sea el que esperamos (HMAC)
			if _, ok := token.Method.(*jwt.SigningMethodHMAC); !ok {
				return nil, errors.New("unexpected signing method")
			}
			return []byte(tokenSecret), nil
		},
	)

	if err != nil {
		return uuid.Nil, err // El token es inválido o expiró
	}

	// 2. Extraemos el Subject (que es el ID del usuario como string)
	userIDString, err := token.Claims.GetSubject()
	if err != nil {
		return uuid.Nil, err
	}

	// 3. Convertimos el string de vuelta a uuid.UUID
	id, err := uuid.Parse(userIDString)
	if err != nil {
		return uuid.Nil, err
	}

	return id, nil
}

func GetBearerToken(headers http.Header) (string, error) {
	authHeader := headers.Get("Authorization")
	if authHeader == "" {
		return "", errors.New("no authorization header included")
	}

	if !strings.HasPrefix(authHeader, "Bearer ") {
		return "", errors.New("malformed authorization header")
	}

	// Quitamos el prefijo y los espacios para devolver solo el token
	return strings.TrimSpace(strings.TrimPrefix(authHeader, "Bearer ")), nil
}

func MakeRefreshToken() (string, error) {
	c := 32
	b := make([]byte, c)
	_, err := rand.Read(b)
	if err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}
