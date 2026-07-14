package main

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"os"

	"github.com/joho/godotenv"

	"ow-custom-app/backend/config"
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

	oauthCfg, err := config.GoogleOAuthConfig()
	if err != nil {
		log.Fatalf("OAuth config failed: %v", err)
	}

	mux := http.NewServeMux()

	mux.HandleFunc("GET /", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintln(w, "ow-custom-app API server is running!")
	})
	mux.HandleFunc("GET /health", handlers.Health(pool))
	mux.HandleFunc("GET /api/v1/auth/google/login", handlers.AuthGoogleLogin(oauthCfg))
	mux.HandleFunc("GET /api/v1/auth/google/callback", handlers.AuthGoogleCallback(oauthCfg, pool))
	mux.HandleFunc("GET /api/v1/auth/me", handlers.RequireAuth(pool, handlers.AuthMe()))
	mux.HandleFunc("POST /api/v1/auth/logout", handlers.AuthLogout(pool))

	// 組織（ログイン必須。member/admin の権限は各ハンドラ内で判定）
	mux.HandleFunc("POST /api/v1/organizations", handlers.RequireAuth(pool, handlers.CreateOrganization(pool)))
	mux.HandleFunc("GET /api/v1/organizations", handlers.RequireAuth(pool, handlers.ListOrganizations(pool)))
	mux.HandleFunc("GET /api/v1/organizations/{id}", handlers.RequireAuth(pool, handlers.GetOrganization(pool)))
	mux.HandleFunc("PUT /api/v1/organizations/{id}/level-display", handlers.RequireAuth(pool, handlers.UpdateLevelDisplay(pool)))

	// プレイヤー（ログイン必須。member/admin の権限は各ハンドラ内で判定）
	mux.HandleFunc("GET /api/v1/organizations/{orgId}/players", handlers.RequireAuth(pool, handlers.ListPlayers(pool)))
	mux.HandleFunc("POST /api/v1/organizations/{orgId}/players", handlers.RequireAuth(pool, handlers.CreatePlayer(pool)))
	mux.HandleFunc("PUT /api/v1/players/{id}", handlers.RequireAuth(pool, handlers.UpdatePlayer(pool)))
	mux.HandleFunc("DELETE /api/v1/players/{id}", handlers.RequireAuth(pool, handlers.DeletePlayer(pool)))

	// マップ（ログイン必須。member/admin の権限は各ハンドラ内で判定）
	mux.HandleFunc("GET /api/v1/organizations/{orgId}/maps", handlers.RequireAuth(pool, handlers.ListMaps(pool)))
	mux.HandleFunc("POST /api/v1/organizations/{orgId}/maps", handlers.RequireAuth(pool, handlers.CreateMap(pool)))
	mux.HandleFunc("DELETE /api/v1/maps/{id}", handlers.RequireAuth(pool, handlers.DeleteMap(pool)))

	// セッション（ログイン必須。member/admin の権限は各ハンドラ内で判定）
	mux.HandleFunc("POST /api/v1/organizations/{orgId}/sessions", handlers.RequireAuth(pool, handlers.CreateSession(pool)))
	mux.HandleFunc("GET /api/v1/sessions/{id}", handlers.RequireAuth(pool, handlers.GetSession(pool)))
	mux.HandleFunc("POST /api/v1/sessions/{id}/maps", handlers.RequireAuth(pool, handlers.SetSessionMaps(pool)))
	mux.HandleFunc("POST /api/v1/sessions/{id}/end", handlers.RequireAuth(pool, handlers.EndSession(pool)))

	// 試合（ログイン必須。member/admin の権限は各ハンドラ内で判定）
	mux.HandleFunc("POST /api/v1/sessions/{id}/matches", handlers.RequireAuth(pool, handlers.CreateMatch(pool)))
	mux.HandleFunc("POST /api/v1/matches/{id}/result", handlers.RequireAuth(pool, handlers.SetMatchResult(pool)))
	mux.HandleFunc("DELETE /api/v1/matches/{id}", handlers.RequireAuth(pool, handlers.CancelMatch(pool)))

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
