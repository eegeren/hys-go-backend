package middlewares

import (
	"net/http"
	"strings"
)

// İsteklerde X-Role header'ı bekliyoruz. Örn: "Patron", "IK", "Admin", "Manager", "Personel"
func RequireRoles(roles ...string) func(http.Handler) http.Handler {
	allowed := map[string]struct{}{}
	for _, r := range roles {
		allowed[strings.ToLower(strings.TrimSpace(r))] = struct{}{}
	}

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			role := strings.ToLower(strings.TrimSpace(r.Header.Get("X-Role")))
			if _, ok := allowed[role]; !ok {
				http.Error(w, "forbidden", http.StatusForbidden)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}
