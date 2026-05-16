package main

import (
	"embed"
	"flag"
	"fmt"
	"io/fs"
	"log"
	"net/http"
	"os"
	"path/filepath"

	"spp/src/api"
	"spp/src/config"
	"spp/src/process"
	"spp/src/system"
)

//go:embed web/index.html
var webFS embed.FS

func main() {
	var (
		port    = flag.Int("port", 8080, "HTTP server port")
		cfgFile = flag.String("config", "spp.json", "Config file path")
	)
	flag.Parse()

	// Load config
	absPath, _ := filepath.Abs(*cfgFile)
	cfg, err := config.Load(absPath)
	if err != nil {
		log.Fatalf("failed to load config: %v", err)
	}
	if *port != 8080 {
		cfg.ServerPort = *port
	}

	// Start system metrics
	system.Start()

	// Restore existing processes into manager
	mgr := process.GetManager()
	for _, p := range cfg.Processes {
		mgr.Ensure(p)
	}

	// Boot auto-restart
	mgr.BootAutoRestart()

	// Start SSE broadcasters
	api.StartBroadcasters()

	// HTTP mux
	mux := http.NewServeMux()

	// API routes
	api.SetupRoutes(mux)

	// Serve frontend
	webRoot, _ := fs.Sub(webFS, "web")
	mux.Handle("/", http.FileServer(http.FS(webRoot)))

	addr := fmt.Sprintf(":%d", cfg.ServerPort)
	log.Printf("SPP listening on http://localhost%s", addr)
	log.Printf("Config: %s", absPath)

	if err := http.ListenAndServe(addr, corsMiddleware(mux)); err != nil {
		fmt.Fprintf(os.Stderr, "server error: %v\n", err)
		os.Exit(1)
	}
}

func corsMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type")
		if r.Method == http.MethodOptions {
			w.WriteHeader(204)
			return
		}
		next.ServeHTTP(w, r)
	})
}
