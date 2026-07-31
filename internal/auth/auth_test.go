package auth

import (
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestJWT(t *testing.T) {
	userID := uuid.New()
	secret := "mi_secreto_super_seguro"
	expiresIn := time.Hour

	// 1. Prueba de éxito
	t.Run("Valid Token", func(t *testing.T) {
		token, err := MakeJWT(userID, secret, expiresIn)
		if err != nil {
			t.Fatalf("Error creando token: %v", err)
		}

		parsedID, err := ValidateJWT(token, secret)
		if err != nil {
			t.Fatalf("Error validando token: %v", err)
		}

		if parsedID != userID {
			t.Errorf("Se esperaba el ID %v, pero se obtuvo %v", userID, parsedID)
		}
	})

	// 2. Prueba de secreto incorrecto
	t.Run("Wrong Secret", func(t *testing.T) {
		token, err := MakeJWT(userID, secret, expiresIn)
		if err != nil {
			t.Fatalf("Error creando token: %v", err)
		}

		_, err = ValidateJWT(token, "secreto_equivocado")
		if err == nil {
			t.Fatal("Se esperaba un error por usar el secreto incorrecto, pero no hubo error")
		}
	})

	// 3. Prueba de token expirado
	t.Run("Expired Token", func(t *testing.T) {
		// Creamos un token que expiró hace 1 hora (tiempo negativo)
		token, err := MakeJWT(userID, secret, -time.Hour)
		if err != nil {
			t.Fatalf("Error creando token: %v", err)
		}

		_, err = ValidateJWT(token, secret)
		if err == nil {
			t.Fatal("Se esperaba un error por token expirado, pero no hubo error")
		}
	})
}
