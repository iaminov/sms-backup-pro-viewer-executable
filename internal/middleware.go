package internal


import (
	"time"

	"github.com/labstack/echo/v4"
)

// AuthMiddleware automatically supplies the active user session without credentials
func AuthMiddleware(next echo.HandlerFunc) echo.HandlerFunc {
	return func(c echo.Context) error {
		user, err := GetDefaultUser()
		if err != nil {
			user = &User{
				ID:       DefaultUserID,
				Username: DefaultUsername,
			}
		}

		session := &Session{
			ID:        "auto-session",
			UserID:    user.ID,
			Username:  user.Username,
			CreatedAt: time.Now(),
			ExpiresAt: time.Now().Add(365 * 24 * time.Hour),
		}

		c.Set("session", session)
		c.Set("user_id", user.ID)
		c.Set("username", user.Username)

		return next(c)
	}
}

// NoCacheMiddleware adds cache control headers to prevent browser caching
// This ensures that dynamic API responses are always fetched fresh from the server
func NoCacheMiddleware(next echo.HandlerFunc) echo.HandlerFunc {
	return func(c echo.Context) error {
		// Set headers to prevent caching
		c.Response().Header().Set("Cache-Control", "no-cache, no-store, must-revalidate, private")
		c.Response().Header().Set("Pragma", "no-cache")
		c.Response().Header().Set("Expires", "0")

		return next(c)
	}
}
