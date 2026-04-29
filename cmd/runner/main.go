package main

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"strconv"
	"strings"

	"github.com/containerd/containerd"
)

var ctrClient *containerd.Client

func main() {
	var err error
	ctrClient, err = containerd.New(containerdSock)
	if err != nil {
		log.Fatalf("connecting to containerd at %s: %v", containerdSock, err)
	}
	defer ctrClient.Close()

	token := os.Getenv("RUNNER_TOKEN")
	if token == "" {
		log.Fatal("RUNNER_TOKEN environment variable must be set")
	}

	port := os.Getenv("RUNNER_PORT")
	if port == "" {
		port = "8080"
	}
	http.HandleFunc("/run", requireToken(token, handleRun))
	http.HandleFunc("/benchmark", requireToken(token, handleBenchmark))
	http.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	log.Printf("runner listening on :%s", port)
	if err := http.ListenAndServe(":"+port, nil); err != nil {
		log.Fatal(err)
	}
}

func requireToken(token string, next http.HandlerFunc) http.HandlerFunc {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		auth := r.Header.Get("Authorization")
		bearerToken, ok := strings.CutPrefix(auth, "Bearer ")
		if !ok || bearerToken != token {
			http.Error(w, "Unauthorized: incorrect token", http.StatusUnauthorized)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func handleBenchmark(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	iterations := 5
	if s := r.URL.Query().Get("n"); s != "" {
		if n, err := strconv.Atoi(s); err == nil && n > 0 && n <= 50 {
			iterations = n
		}
	}
	report, err := benchmark(r.Context(), iterations)
	if err != nil {
		http.Error(w, fmt.Sprintf("benchmark error: %v", err), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(report)
}

func handleRun(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var req RunRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, fmt.Sprintf("bad request: %v", err), http.StatusBadRequest)
		return
	}
	if err := req.validate(); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	result, err := execute(r.Context(), req, gvisorRuntime)
	if err != nil {
		http.Error(w, fmt.Sprintf("execution error: %v", err), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(result)
}
