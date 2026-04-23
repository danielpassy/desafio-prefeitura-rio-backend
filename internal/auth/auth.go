package auth

import (
	"context"
	"fmt"
	"net/http"
	"strings"

	"github.com/MicahParks/keyfunc/v3"
	"github.com/gin-gonic/gin"
	"github.com/golang-jwt/jwt/v5"
)

type cpfContextKey struct{}

func CPFFromContext(ctx context.Context) (string, bool) {
	v, ok := ctx.Value(cpfContextKey{}).(string)
	return v, ok
}

func NewJWKSKeyfunc(ctx context.Context, jwksURL string) (keyfunc.Keyfunc, error) {
	kf, err := keyfunc.NewDefaultCtx(ctx, []string{jwksURL})
	if err != nil {
		return nil, fmt.Errorf("init jwks keyfunc: %w", err)
	}
	return kf, nil
}

// AuthMiddleware validates the Bearer JWT and sets the citizen's CPF in the context.
// Rejects with 401 if the token is missing, malformed, expired, or has no preferred_username.
func AuthMiddleware(kf keyfunc.Keyfunc) gin.HandlerFunc {
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

		ctx := context.WithValue(c.Request.Context(), cpfContextKey{}, cpf)
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
