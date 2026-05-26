package main

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"os"

	"github.com/joho/godotenv"

	"ow-custom-app/backend/db"
	"ow-custom-app/backend/handlers"
)

func main() {
	// .env を読み込む。backend/ から起動するので ../.env を優先、
	// なければカレントの .env を試す。どちらも無くてもエラーにしない
	// （本番では環境変数が直接注入されることを想定）。
	if err := godotenv.Load("../.env"); err != nil {
		if err := godotenv.Load(); err != nil {
			log.Println(".env not found, using OS environment variables")
		}
	}

	ctx := context.Background()
	pool, err := db.Connect(ctx)
	if err != nil {
		log.Fatalf("DB connect failed: %v", err)
	}
	defer pool.Close()
	log.Println("DB connected")

	mux := http.NewServeMux()

	mux.HandleFunc("GET /", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintln(w, "ow-custom-app API server is running!")
	})
	mux.HandleFunc("GET /health", handlers.Health(pool))

	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}
	addr := ":" + port

	log.Printf("Server started on http://localhost%s", addr)
	if err := http.ListenAndServe(addr, mux); err != nil {
		log.Fatalf("server error: %v", err)
	}
}
