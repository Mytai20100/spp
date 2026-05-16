package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"time"
)

type AutoRestart string

const (
	AutoRestartAlways   AutoRestart = "always"
	AutoRestartOnCrash  AutoRestart = "on_crash"
	AutoRestartNever    AutoRestart = "never"
)

type Process struct {
	ID          string      `json:"id"`
	Name        string      `json:"name"`
	Command     string      `json:"command"`
	Args        []string    `json:"args"`
	WorkDir     string      `json:"work_dir"`
	AutoRestart AutoRestart `json:"auto_restart"`
	Env         []string    `json:"env"`
	CreatedAt   time.Time   `json:"created_at"`
}

type Config struct {
	Processes  []Process `json:"processes"`
	ServerPort int       `json:"server_port"`
	DataDir    string    `json:"data_dir"`
}

var (
	mu       sync.RWMutex
	cfgPath  string
	instance *Config
)

func Load(path string) (*Config, error) {
	mu.Lock()
	defer mu.Unlock()
	cfgPath = path

	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			instance = &Config{
				Processes:  []Process{},
				ServerPort: 8080,
				DataDir:    filepath.Dir(path),
			}
			return instance, nil
		}
		return nil, err
	}
	instance = &Config{}
	if err := json.Unmarshal(data, instance); err != nil {
		return nil, err
	}
	return instance, nil
}

func Save() error {
	mu.RLock()
	defer mu.RUnlock()
	data, err := json.MarshalIndent(instance, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(cfgPath, data, 0644)
}

func Get() *Config {
	mu.RLock()
	defer mu.RUnlock()
	return instance
}

func AddProcess(p Process) {
	mu.Lock()
	defer mu.Unlock()
	instance.Processes = append(instance.Processes, p)
}

func UpdateProcess(p Process) {
	mu.Lock()
	defer mu.Unlock()
	for i, proc := range instance.Processes {
		if proc.ID == p.ID {
			instance.Processes[i] = p
			return
		}
	}
}

func DeleteProcess(id string) {
	mu.Lock()
	defer mu.Unlock()
	procs := make([]Process, 0, len(instance.Processes))
	for _, p := range instance.Processes {
		if p.ID != id {
			procs = append(procs, p)
		}
	}
	instance.Processes = procs
}

func GetProcess(id string) (*Process, bool) {
	mu.RLock()
	defer mu.RUnlock()
	for _, p := range instance.Processes {
		if p.ID == id {
			cp := p
			return &cp, true
		}
	}
	return nil, false
}
