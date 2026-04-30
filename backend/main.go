package main

import (
	"fmt"
	"net/http"
)

func main() {
	// ルートにアクセスしたときの処理
	http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintln(w, "ow-custom-app API server is running!")
	})

	// ポート8080でサーバー起動
	fmt.Println("Server started on http://localhost:8080")
	http.ListenAndServe(":8080", nil)
}
