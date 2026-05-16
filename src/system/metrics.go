package system

import (
	"bufio"
	"fmt"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"
)

type CPUStat struct {
	User    uint64
	Nice    uint64
	System  uint64
	Idle    uint64
	Iowait  uint64
	Irq     uint64
	Softirq uint64
	Steal   uint64
}

type Metrics struct {
	CPUPercent  float64 `json:"cpu_percent"`
	MemTotal    uint64  `json:"mem_total"`
	MemUsed     uint64  `json:"mem_used"`
	MemPercent  float64 `json:"mem_percent"`
	NetRxBytes  uint64  `json:"net_rx_bytes"`
	NetTxBytes  uint64  `json:"net_tx_bytes"`
	NetRxRate   float64 `json:"net_rx_rate"`
	NetTxRate   float64 `json:"net_tx_rate"`
	Uptime      uint64  `json:"uptime"`
	LoadAvg     string  `json:"load_avg"`
}

var (
	mu          sync.RWMutex
	last        Metrics
	prevCPU     CPUStat
	prevRx      uint64
	prevTx      uint64
	prevSample  time.Time
)

func Start() {
	go func() {
		for {
			collect()
			time.Sleep(1 * time.Second)
		}
	}()
}

func Get() Metrics {
	mu.RLock()
	defer mu.RUnlock()
	return last
}

func collect() {
	now := time.Now()
	m := Metrics{}

	// CPU
	cur, err := readCPUStat()
	if err == nil {
		elapsed := now.Sub(prevSample).Seconds()
		if !prevSample.IsZero() && elapsed > 0 {
			deltaUser := float64(cur.User - prevCPU.User)
			deltaSystem := float64(cur.System - prevCPU.System)
			deltaIdle := float64(cur.Idle - prevCPU.Idle)
			deltaTotal := deltaUser + deltaSystem + deltaIdle +
				float64(cur.Nice-prevCPU.Nice) +
				float64(cur.Iowait-prevCPU.Iowait) +
				float64(cur.Irq-prevCPU.Irq) +
				float64(cur.Softirq-prevCPU.Softirq) +
				float64(cur.Steal-prevCPU.Steal)
			if deltaTotal > 0 {
				m.CPUPercent = (1 - deltaIdle/deltaTotal) * 100
			}
		}
		prevCPU = cur
	}

	// Memory
	memTotal, memAvail := readMemInfo()
	m.MemTotal = memTotal
	if memTotal > 0 {
		memUsed := memTotal - memAvail
		m.MemUsed = memUsed
		m.MemPercent = float64(memUsed) / float64(memTotal) * 100
	}

	// Network
	rx, tx := readNetDev()
	elapsed := now.Sub(prevSample).Seconds()
	if !prevSample.IsZero() && elapsed > 0 {
		m.NetRxRate = float64(rx-prevRx) / elapsed
		m.NetTxRate = float64(tx-prevTx) / elapsed
	}
	m.NetRxBytes = rx
	m.NetTxBytes = tx
	prevRx = rx
	prevTx = tx

	// Uptime
	m.Uptime = readUptime()
	m.LoadAvg = readLoadAvg()

	prevSample = now

	mu.Lock()
	last = m
	mu.Unlock()
}

func readCPUStat() (CPUStat, error) {
	f, err := os.Open("/proc/stat")
	if err != nil {
		return CPUStat{}, err
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := sc.Text()
		if !strings.HasPrefix(line, "cpu ") {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 8 {
			return CPUStat{}, fmt.Errorf("unexpected cpu stat format")
		}
		parse := func(s string) uint64 {
			v, _ := strconv.ParseUint(s, 10, 64)
			return v
		}
		return CPUStat{
			User:    parse(fields[1]),
			Nice:    parse(fields[2]),
			System:  parse(fields[3]),
			Idle:    parse(fields[4]),
			Iowait:  parse(fields[5]),
			Irq:     parse(fields[6]),
			Softirq: parse(fields[7]),
			Steal:   func() uint64 { if len(fields) > 8 { v, _ := strconv.ParseUint(fields[8], 10, 64); return v }; return 0 }(),
		}, nil
	}
	return CPUStat{}, fmt.Errorf("cpu line not found")
}

func readMemInfo() (total, avail uint64) {
	f, err := os.Open("/proc/meminfo")
	if err != nil {
		return 0, 0
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := sc.Text()
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		val, _ := strconv.ParseUint(fields[1], 10, 64)
		val *= 1024 // kB -> bytes
		switch fields[0] {
		case "MemTotal:":
			total = val
		case "MemAvailable:":
			avail = val
		}
	}
	return
}

func readNetDev() (rxBytes, txBytes uint64) {
	f, err := os.Open("/proc/net/dev")
	if err != nil {
		return
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	lineNum := 0
	for sc.Scan() {
		lineNum++
		if lineNum <= 2 {
			continue
		}
		line := sc.Text()
		idx := strings.Index(line, ":")
		if idx < 0 {
			continue
		}
		iface := strings.TrimSpace(line[:idx])
		if iface == "lo" {
			continue
		}
		fields := strings.Fields(line[idx+1:])
		if len(fields) < 9 {
			continue
		}
		rx, _ := strconv.ParseUint(fields[0], 10, 64)
		tx, _ := strconv.ParseUint(fields[8], 10, 64)
		rxBytes += rx
		txBytes += tx
	}
	return
}

func readUptime() uint64 {
	data, err := os.ReadFile("/proc/uptime")
	if err != nil {
		return 0
	}
	fields := strings.Fields(string(data))
	if len(fields) == 0 {
		return 0
	}
	v, _ := strconv.ParseFloat(fields[0], 64)
	return uint64(v)
}

func readLoadAvg() string {
	data, err := os.ReadFile("/proc/loadavg")
	if err != nil {
		return "0.00 0.00 0.00"
	}
	fields := strings.Fields(string(data))
	if len(fields) < 3 {
		return "0.00 0.00 0.00"
	}
	return strings.Join(fields[:3], " ")
}

func FormatBytes(b uint64) string {
	const unit = 1024
	if b < unit {
		return fmt.Sprintf("%d B", b)
	}
	div, exp := uint64(unit), 0
	for n := b / unit; n >= unit; n /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %cB", float64(b)/float64(div), "KMGTPE"[exp])
}

func FormatRate(r float64) string {
	if r < 1024 {
		return fmt.Sprintf("%.0f B/s", r)
	}
	if r < 1024*1024 {
		return fmt.Sprintf("%.1f KB/s", r/1024)
	}
	return fmt.Sprintf("%.1f MB/s", r/1024/1024)
}

func FormatUptime(secs uint64) string {
	days := secs / 86400
	hours := (secs % 86400) / 3600
	mins := (secs % 3600) / 60
	if days > 0 {
		return fmt.Sprintf("%dd %dh %dm", days, hours, mins)
	}
	return fmt.Sprintf("%dh %dm", hours, mins)
}
