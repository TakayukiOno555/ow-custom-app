package handlers

import (
	"context"
	"encoding/json"
	"net/http"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// Health は GET /health のハンドラを返す。
// DB に Ping を投げて疎通を確認し、結果を JSON で返す。
func Health(pool *pgxpool.Pool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
		defer cancel()

		status := "ok"
		dbStatus := "ok"
		httpCode := http.StatusOK

		if err := pool.Ping(ctx); err != nil {
			status = "degraded"
			dbStatus = "down: " + err.Error()
			httpCode = http.StatusServiceUnavailable
		}

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(httpCode)
		_ = json.NewEncoder(w).Encode(map[string]string{
			"status": status,
			"db":     dbStatus,
		})
	}
}
