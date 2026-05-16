package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"spp/src/config"
	"spp/src/process"
	"spp/src/system"
)

func jsonResp(w http.ResponseWriter, code int, v interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

func parseJSON(r *http.Request, v interface{}) error {
	return json.NewDecoder(r.Body).Decode(v)
}

// generateID creates a simple unique ID
func generateID() string {
	return fmt.Sprintf("%d", time.Now().UnixNano())
}

func SetupRoutes(mux *http.ServeMux) {
	// Processes CRUD
	mux.HandleFunc("/api/processes", handleProcesses)
	mux.HandleFunc("/api/processes/", handleProcess)

	// Metrics SSE
	mux.HandleFunc("/api/metrics/stream", handleMetricsStream)
	mux.HandleFunc("/api/metrics", handleMetrics)

	// Process logs SSE
	mux.HandleFunc("/api/processes/logs/stream/", handleLogsStream)
	mux.HandleFunc("/api/processes/logs/", handleLogs)

	// Process stdin
	mux.HandleFunc("/api/processes/stdin/", handleStdin)

	// Disk usage
	mux.HandleFunc("/api/processes/disk/", handleDisk)
}

func handleProcesses(w http.ResponseWriter, r *http.Request) {
	mgr := process.GetManager()
	switch r.Method {
	case http.MethodGet:
		cfg := config.Get()
		type ProcessInfo struct {
			config.Process
			Status    process.Status `json:"status"`
			PID       int            `json:"pid"`
			StartedAt *time.Time     `json:"started_at,omitempty"`
		}
		result := make([]ProcessInfo, 0, len(cfg.Processes))
		for _, p := range cfg.Processes {
			inst := mgr.Ensure(p)
			info := ProcessInfo{Process: p, Status: inst.Status(), PID: inst.PID()}
			if !inst.StartedAt().IsZero() {
				t := inst.StartedAt()
				info.StartedAt = &t
			}
			result = append(result, info)
		}
		jsonResp(w, 200, result)

	case http.MethodPost:
		var p config.Process
		if err := parseJSON(r, &p); err != nil {
			jsonResp(w, 400, map[string]string{"error": err.Error()})
			return
		}
		p.ID = generateID()
		p.CreatedAt = time.Now()
		if p.AutoRestart == "" {
			p.AutoRestart = config.AutoRestartNever
		}
		config.AddProcess(p)
		if err := config.Save(); err != nil {
			jsonResp(w, 500, map[string]string{"error": err.Error()})
			return
		}
		mgr.Ensure(p)
		jsonResp(w, 201, p)

	default:
		http.Error(w, "method not allowed", 405)
	}
}

func handleProcess(w http.ResponseWriter, r *http.Request) {
	// /api/processes/{id}/{action}
	path := strings.TrimPrefix(r.URL.Path, "/api/processes/")
	parts := strings.SplitN(path, "/", 2)
	id := parts[0]
	action := ""
	if len(parts) > 1 {
		action = parts[1]
	}

	// Skip if matched by more specific patterns
	if id == "logs" || id == "disk" || id == "stdin" {
		http.NotFound(w, r)
		return
	}

	mgr := process.GetManager()

	switch action {
	case "start":
		if err := mgr.Start(id); err != nil {
			jsonResp(w, 400, map[string]string{"error": err.Error()})
			return
		}
		jsonResp(w, 200, map[string]string{"status": "started"})

	case "stop":
		if err := mgr.Stop(id); err != nil {
			jsonResp(w, 400, map[string]string{"error": err.Error()})
			return
		}
		jsonResp(w, 200, map[string]string{"status": "stopped"})

	case "restart":
		if err := mgr.Restart(id); err != nil {
			jsonResp(w, 400, map[string]string{"error": err.Error()})
			return
		}
		jsonResp(w, 200, map[string]string{"status": "restarted"})

	case "":
		switch r.Method {
		case http.MethodPut:
			var p config.Process
			if err := parseJSON(r, &p); err != nil {
				jsonResp(w, 400, map[string]string{"error": err.Error()})
				return
			}
			p.ID = id
			config.UpdateProcess(p)
			if err := config.Save(); err != nil {
				jsonResp(w, 500, map[string]string{"error": err.Error()})
				return
			}
			jsonResp(w, 200, p)

		case http.MethodDelete:
			_ = mgr.Stop(id)
			config.DeleteProcess(id)
			mgr.Remove(id)
			if err := config.Save(); err != nil {
				jsonResp(w, 500, map[string]string{"error": err.Error()})
				return
			}
			jsonResp(w, 200, map[string]string{"status": "deleted"})

		default:
			http.Error(w, "method not allowed", 405)
		}

	default:
		http.NotFound(w, r)
	}
}

func handleMetrics(w http.ResponseWriter, r *http.Request) {
	m := system.Get()
	jsonResp(w, 200, m)
}

func handleMetricsStream(w http.ResponseWriter, r *http.Request) {
	SSEHandler("metrics", w, r)
}

func handleLogsStream(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimPrefix(r.URL.Path, "/api/processes/logs/stream/")
	SSEHandler("logs:"+id, w, r)
}

func handleLogs(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimPrefix(r.URL.Path, "/api/processes/logs/")
	mgr := process.GetManager()
	inst, ok := mgr.Get(id)
	if !ok {
		jsonResp(w, 404, map[string]string{"error": "not found"})
		return
	}
	jsonResp(w, 200, inst.Logs())
}

func handleStdin(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", 405)
		return
	}
	id := strings.TrimPrefix(r.URL.Path, "/api/processes/stdin/")
	var body struct {
		Data string `json:"data"`
	}
	if err := parseJSON(r, &body); err != nil {
		jsonResp(w, 400, map[string]string{"error": err.Error()})
		return
	}
	mgr := process.GetManager()
	if err := mgr.WriteInput(id, []byte(body.Data+"\n")); err != nil {
		jsonResp(w, 400, map[string]string{"error": err.Error()})
		return
	}
	jsonResp(w, 200, map[string]string{"status": "ok"})
}

func handleDisk(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimPrefix(r.URL.Path, "/api/processes/disk/")
	mgr := process.GetManager()
	inst, ok := mgr.Get(id)
	if !ok {
		jsonResp(w, 404, map[string]string{"error": "not found"})
		return
	}
	size, err := inst.DiskUsage()
	if err != nil {
		jsonResp(w, 500, map[string]string{"error": err.Error()})
		return
	}
	jsonResp(w, 200, map[string]interface{}{
		"bytes":     size,
		"formatted": system.FormatBytes(uint64(size)),
	})
}
