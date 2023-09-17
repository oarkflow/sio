package main

import (
	"net/http"
	
	"github.com/oarkflow/sio/chi"
)

func main() {
	srv := chi.NewRouter()
	srv.Mount("/", http.FileServer(http.Dir("webroot")))
	http.ListenAndServe(":8083", srv)
}
