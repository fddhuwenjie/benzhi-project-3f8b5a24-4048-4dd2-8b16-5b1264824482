package httpapi

import "net/http"

func methodAllowed(r *http.Request, want string) bool { return r.Method == want }
