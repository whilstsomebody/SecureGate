package main

import (
	"crypto/rand"
	"fmt"
	"os"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/whilstsomebody/securegate/internal/config"
)

func main() {
	config.LoadENV()

	secret := config.GetJWTSecret()

	role := "USER"
	if len(os.Args) > 1 {
		role = os.Args[1]
	}

	signedToken, err := generateToken(secret, config.GetJWTIssuer(), config.GetJWTAudience(), role)
	if err != nil {
		panic(err)
	}

	fmt.Println("\nYour JWT Token:")
	fmt.Println(signedToken)
}

func generateToken(secret, issuer, audience, role string) (string, error) {
	claims := jwt.MapClaims{
		"user": "aman",
		"role": role,
		"jti":  newJTI(),
		"exp":  time.Now().Add(time.Hour).Unix(),
		"iss":  issuer,
		"aud":  audience,
	}
	return jwt.NewWithClaims(jwt.SigningMethodHS256, claims).SignedString([]byte(secret))
}

func newJTI() string {
	b := make([]byte, 16)
	rand.Read(b)
	return fmt.Sprintf("%x", b)
}
