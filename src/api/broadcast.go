package api

import (
	"time"

	"spp/src/process"
	"spp/src/system"
)

func StartBroadcasters() {
	// Metrics broadcaster
	go func() {
		for {
			time.Sleep(1 * time.Second)
			m := system.Get()
			GetHub().Publish("metrics", m)
		}
	}()

	// Process log broadcaster
	go broadcastLogs()
}

func broadcastLogs() {
	// Poll all running instances and forward new logs via SSE
	// We track last log index per instance
	lastIdx := make(map[string]int)

	for {
		time.Sleep(200 * time.Millisecond)
		mgr := process.GetManager()
		for _, inst := range mgr.All() {
			id := inst.Config().ID
			logs := inst.Logs()
			prev := lastIdx[id]
			if len(logs) > prev {
				for _, line := range logs[prev:] {
					GetHub().Publish("logs:"+id, line)
				}
				lastIdx[id] = len(logs)
			}
		}
	}
}
