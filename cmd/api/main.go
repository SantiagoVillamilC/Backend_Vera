package main

import (
	"log"
	"net/http"

	"github.com/SantiagoVillamilC/Backend_Centinela/internal/platform/config"
	"github.com/SantiagoVillamilC/Backend_Centinela/internal/platform/httpserver"
)

func main() {
	cfg := config.Load()
	router := httpserver.NewRouter(cfg)
	log.Printf("API listening on :%s", cfg.Port)
	log.Fatal(http.ListenAndServe(":"+cfg.Port, router))
}
