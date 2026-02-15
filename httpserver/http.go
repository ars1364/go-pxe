package httpserver

import (
	"log"
	"net/http"
)

func ListenAndServe(addr, root string) error {
	fs := http.FileServer(http.Dir(root))
	mux := http.NewServeMux()
	mux.Handle("/", logRequests(fs))

	log.Printf("[HTTP] Serving %s on %s", root, addr)
	return http.ListenAndServe(addr, mux)
}

func logRequests(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		log.Printf("[HTTP] %s %s from %s", r.Method, r.URL.Path, r.RemoteAddr)
		next.ServeHTTP(w, r)
	})
}
