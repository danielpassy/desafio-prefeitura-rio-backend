package auth

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"fmt"
	"net/http"
	"strings"

	"github.com/MicahParks/keyfunc/v3"
	"github.com/gin-gonic/gin"
	"github.com/golang-jwt/jwt/v5"
)

type citizenRefContextKey struct{}

func CitizenRefFromContext(ctx context.Context) ([]byte, bool) {
	v, ok := ctx.Value(citizenRefContextKey{}).([]byte)
	return v, ok
}

func NewJWKSKeyfunc(ctx context.Context, jwksURL string) (keyfunc.Keyfunc, error) {
	kf, err := keyfunc.NewDefaultCtx(ctx, []string{jwksURL})
	if err != nil {
		return nil, fmt.Errorf("init jwks keyfunc: %w", err)
	}
	return kf, nil
}

// AuthMiddleware validates the Bearer JWT, derives the citizen's citizenRef
// via HMAC-SHA256(preferred_username, cpfKey), and sets it in the context.
// Rejects with 401 if the token is missing, malformed, expired, or has no preferred_username.
func AuthMiddleware(kf keyfunc.Keyfunc, cpfKey []byte) gin.HandlerFunc {
	return func(c *gin.Context) {
		token, err := parseAndValidateBearerToken(c.GetHeader("Authorization"), kf)
		if err != nil {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "unauthorized"})
			return
		}

		claims, ok := token.Claims.(jwt.MapClaims)
		if !ok {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "unauthorized"})
			return
		}

		cpf, ok := claims["preferred_username"].(string)
		if !ok || cpf == "" {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "unauthorized"})
			return
		}

		citizenRef := computeHMAC([]byte(cpf), cpfKey)
		ctx := context.WithValue(c.Request.Context(), citizenRefContextKey{}, citizenRef)
		c.Request = c.Request.WithContext(ctx)
		c.Next()
	}
}

func parseAndValidateBearerToken(header string, kf keyfunc.Keyfunc) (*jwt.Token, error) {
	bearer, ok := strings.CutPrefix(header, "Bearer ")
	if !ok || bearer == "" {
		return nil, fmt.Errorf("missing bearer token")
	}

	return jwt.Parse(bearer, kf.Keyfunc, jwt.WithValidMethods([]string{"RS256"}))
}

func computeHMAC(data, key []byte) []byte {
	mac := hmac.New(sha256.New, key)
	mac.Write(data)
	return mac.Sum(nil)
}
