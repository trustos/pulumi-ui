package auth

import (
	"context"
	"net/http"
	"strings"

	"github.com/trustos/pulumi-ui/internal/db"
)

type contextKey int

const (
	ctxUserID contextKey = iota
	ctxUser
)

// RequireAuth is middleware that validates the session token from the Authorization header
// or the "session" cookie. On success it stores the user in the request context.
func RequireAuth(users *db.UserStore, sessions *db.SessionStore) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			token := tokenFromRequest(r)
			if token == "" {
				http.Error(w, "unauthorized", http.StatusUnauthorized)
				return
			}

			sess, err := sessions.GetValid(token)
			if err != nil || sess == nil {
				http.Error(w, "unauthorized", http.StatusUnauthorized)
				return
			}

			user, err := users.GetByID(sess.UserID)
			if err != nil || user == nil {
				http.Error(w, "unauthorized", http.StatusUnauthorized)
				return
			}

			ctx := context.WithValue(r.Context(), ctxUser, user)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

func tokenFromRequest(r *http.Request) string {
	// Authorization: Bearer <token>
	if h := r.Header.Get("Authorization"); h != "" {
		if rest, ok := strings.CutPrefix(h, "Bearer "); ok {
			return strings.TrimSpace(rest)
		}
	}
	// Cookie: session=<token>
	if c, err := r.Cookie("session"); err == nil {
		return c.Value
	}
	return ""
}

// UserFromContext returns the authenticated user stored by RequireAuth.
func UserFromContext(ctx context.Context) *db.User {
	u, _ := ctx.Value(ctxUser).(*db.User)
	return u
}
