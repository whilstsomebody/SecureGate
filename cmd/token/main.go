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

	claims := jwt.MapClaims{
		"user": "aman",
		"role": role,
		"jti":  newJTI(),
		"exp":  time.Now().Add(time.Hour).Unix(),
	}

	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)

	signedToken, err := token.SignedString([]byte(secret))
	if err != nil {
		panic(err)
	}

	fmt.Println("\nYour JWT Token:")
	fmt.Println(signedToken)
}

func newJTI() string {
	b := make([]byte, 16)
	rand.Read(b)
	return fmt.Sprintf("%x", b)
}
