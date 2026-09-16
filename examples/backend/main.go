// A local smoke-test backend; not part of the gateway binary.
package main

import (
	"encoding/json"
	"flag"
	"log"
	"net/http"
	"time"
)

func main() {
	addr := flag.String("listen", "127.0.0.1:9000", "listen address")
	flag.Parse()
	srv := &http.Server{Addr: *addr, ReadHeaderTimeout: 5 * time.Second, Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]string{"backend": *addr, "method": r.Method, "path": r.URL.Path})
	})}
	log.Fatal(srv.ListenAndServe())
}
