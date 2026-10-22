package main

import (
	"github.com/golang-jwt/jwt/v5"
	"github.com/whilstsomebody/securegate/internal/config"
	"testing"
	"time"
)

func TestGeneratedToken(t *testing.T) {
	for _, custom := range []bool{false, true} {
		t.Setenv("JWT_ISSUER", "")
		t.Setenv("JWT_AUDIENCE", "")
		if custom {
			t.Setenv("JWT_ISSUER", "custom-issuer")
			t.Setenv("JWT_AUDIENCE", "custom-audience")
		}
		issuer, audience := config.GetJWTIssuer(), config.GetJWTAudience()
		seen := map[string]bool{}
		for i := 0; i < 2; i++ {
			signed, err := generateToken("test-secret", issuer, audience, "USER")
			if err != nil {
				t.Fatal(err)
			}
			token, err := jwt.Parse(signed, func(*jwt.Token) (any, error) { return []byte("test-secret"), nil }, jwt.WithValidMethods([]string{"HS256"}), jwt.WithExpirationRequired(), jwt.WithIssuer(issuer), jwt.WithAudience(audience))
			if err != nil {
				t.Fatal(err)
			}
			claims := token.Claims.(jwt.MapClaims)
			id, ok := claims["jti"].(string)
			if !ok || len(id) != 32 || seen[id] {
				t.Fatalf("invalid or repeated JTI: %q", id)
			}
			seen[id] = true
			exp, err := claims.GetExpirationTime()
			if err != nil || exp == nil {
				t.Fatalf("expiration: %v", err)
			}
			if remaining := time.Until(exp.Time); remaining < 59*time.Minute || remaining > time.Hour {
				t.Fatalf("unexpected lifetime: %v", remaining)
			}
			if claims["role"] != "USER" {
				t.Fatal("role not preserved")
			}
		}
	}
}
