package process

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"syscall"
	"time"

	"spp/src/config"
)

// killGroup kills the entire process group of cmd.
// When cmd was started with Setpgid=true, its PGID == its PID,
// so kill(-pgid, SIGKILL) terminates all children as well.
// Falls back to cmd.Process.Kill() if we cannot get the PGID.
func killGroup(cmd *exec.Cmd) {
	if cmd == nil || cmd.Process == nil {
		return
	}
	pid := cmd.Process.Pid
	// Negative PID targets the whole process group.
	if err := syscall.Kill(-pid, syscall.SIGKILL); err != nil {
		// Fallback: at least kill the direct process.
		_ = cmd.Process.Kill()
	}
}

type Status string

const (
	StatusStopped  Status = "stopped"
	StatusRunning  Status = "running"
	StatusCrashed  Status = "crashed"
	StatusStarting Status = "starting"
)

type LogLine struct {
	Time    time.Time `json:"time"`
	Content string    `json:"content"`
	Stream  string    `json:"stream"`
}

type Instance struct {
	cfg       config.Process
	cmd       *exec.Cmd
	status    Status
	pid       int
	startedAt time.Time
	logs      []LogLine
	logMu     sync.Mutex
	mu        sync.Mutex
	stopCh    chan struct{}
	stopped   bool         // guards against double-close of stopCh
	doneCh    chan struct{} // closed by run() when the goroutine exits
	logSubs   []chan LogLine
	subMu     sync.Mutex
	stdinW    *io.PipeWriter
	stdinMu   sync.Mutex
}

func (inst *Instance) pushLog(l LogLine) {
	inst.logMu.Lock()
	inst.logs = append(inst.logs, l)
	if len(inst.logs) > 2000 {
		inst.logs = inst.logs[len(inst.logs)-2000:]
	}
	inst.logMu.Unlock()
	inst.subMu.Lock()
	for _, ch := range inst.logSubs {
		select {
		case ch <- l:
		default:
		}
	}
	inst.subMu.Unlock()
}

func (inst *Instance) Logs() []LogLine {
	inst.logMu.Lock()
	defer inst.logMu.Unlock()
	cp := make([]LogLine, len(inst.logs))
	copy(cp, inst.logs)
	return cp
}

func (inst *Instance) Status() Status {
	inst.mu.Lock()
	defer inst.mu.Unlock()
	return inst.status
}

func (inst *Instance) PID() int {
	inst.mu.Lock()
	defer inst.mu.Unlock()
	return inst.pid
}

func (inst *Instance) StartedAt() time.Time {
	inst.mu.Lock()
	defer inst.mu.Unlock()
	return inst.startedAt
}

func (inst *Instance) Config() config.Process {
	return inst.cfg
}

type Manager struct {
	mu        sync.RWMutex
	instances map[string]*Instance
}

var mgr = &Manager{instances: make(map[string]*Instance)}

func GetManager() *Manager { return mgr }

func (m *Manager) Get(id string) (*Instance, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	inst, ok := m.instances[id]
	return inst, ok
}

func (m *Manager) All() []*Instance {
	m.mu.RLock()
	defer m.mu.RUnlock()
	list := make([]*Instance, 0, len(m.instances))
	for _, inst := range m.instances {
		list = append(list, inst)
	}
	return list
}

func (m *Manager) Ensure(cfg config.Process) *Instance {
	m.mu.Lock()
	defer m.mu.Unlock()
	if inst, ok := m.instances[cfg.ID]; ok {
		inst.cfg = cfg
		return inst
	}
	done := make(chan struct{})
	close(done)
	inst := &Instance{cfg: cfg, status: StatusStopped, stopCh: make(chan struct{}), doneCh: done}
	m.instances[cfg.ID] = inst
	return inst
}

func (m *Manager) Remove(id string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.instances, id)
}

func (m *Manager) Start(id string) error {
	inst, ok := m.Get(id)
	if !ok {
		return fmt.Errorf("process %s not found", id)
	}
	inst.mu.Lock()
	if inst.status == StatusRunning || inst.status == StatusStarting {
		inst.mu.Unlock()
		return fmt.Errorf("process already running")
	}
	inst.status = StatusStarting
	inst.stopCh = make(chan struct{})
	inst.stopped = false
	inst.doneCh = make(chan struct{})
	inst.mu.Unlock()
	go inst.run(false)
	return nil
}

func (m *Manager) Stop(id string) error {
	inst, ok := m.Get(id)
	if !ok {
		return fmt.Errorf("process %s not found", id)
	}
	inst.mu.Lock()
	if inst.status != StatusRunning && inst.status != StatusStarting {
		inst.mu.Unlock()
		return nil
	}
	if !inst.stopped {
		inst.stopped = true
		close(inst.stopCh)
	}
	cmd := inst.cmd
	doneCh := inst.doneCh
	inst.mu.Unlock()
	if cmd != nil && cmd.Process != nil {
		killGroup(cmd)
	}
	// Wait for the run() goroutine to fully exit before returning.
	<-doneCh
	return nil
}

func (m *Manager) Restart(id string) error {
	// Stop() blocks until the goroutine exits, so no sleep needed.
	_ = m.Stop(id)
	return m.Start(id)
}

func (m *Manager) WriteInput(id string, data []byte) error {
	inst, ok := m.Get(id)
	if !ok {
		return fmt.Errorf("not found")
	}
	inst.stdinMu.Lock()
	w := inst.stdinW
	inst.stdinMu.Unlock()
	if w == nil {
		return fmt.Errorf("process has no stdin (not running?)")
	}
	_, err := w.Write(data)
	return err
}

func (m *Manager) BootAutoRestart() {
	cfg := config.Get()
	for _, p := range cfg.Processes {
		if p.AutoRestart == config.AutoRestartAlways {
			m.Ensure(p)
			go func(proc config.Process) {
				time.Sleep(500 * time.Millisecond)
				_ = m.Start(proc.ID)
			}(p)
		}
	}
}

func (inst *Instance) run(_ bool) {
	cfg := inst.cfg
	if cfg.WorkDir == "" {
		cfg.WorkDir, _ = os.Getwd()
	}

	// closeDone always closes the *current* doneCh, read fresh under lock.
	closeDone := func() {
		inst.mu.Lock()
		ch := inst.doneCh
		inst.mu.Unlock()
		select {
		case <-ch:
		default:
			close(ch)
		}
	}

	isRestart := false
	for {
		sysMsg := "started"
		if isRestart {
			sysMsg = "restarted"
		}
		inst.pushLog(LogLine{Time: time.Now(), Content: fmt.Sprintf("[system] process %s", sysMsg), Stream: "system"})

		cmd := exec.Command(cfg.Command, cfg.Args...)
		cmd.Dir = cfg.WorkDir
		colorEnv := []string{
			"TERM=xterm-256color",
			"COLORTERM=truecolor",
			"FORCE_COLOR=1",
			"CLICOLOR_FORCE=1",
		}
		cmd.Env = append(os.Environ(), colorEnv...)
		cmd.Env = append(cmd.Env, cfg.Env...)
		// Own process group → killGroup() can kill all children at once.
		cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}

		stdout, _ := cmd.StdoutPipe()
		stderr, _ := cmd.StderrPipe()
		stdinR, stdinW := io.Pipe()
		cmd.Stdin = stdinR

		inst.stdinMu.Lock()
		inst.stdinW = stdinW
		inst.stdinMu.Unlock()

		inst.mu.Lock()
		inst.cmd = cmd
		// Snapshot stopCh under lock so we never race with Stop().
		stopCh := inst.stopCh
		inst.mu.Unlock()

		if err := cmd.Start(); err != nil {
			inst.mu.Lock()
			inst.status = StatusCrashed
			inst.cmd = nil
			inst.mu.Unlock()
			inst.stdinMu.Lock()
			inst.stdinW = nil
			inst.stdinMu.Unlock()
			_ = stdinW.Close()
			_ = stdinR.Close()
			inst.pushLog(LogLine{Time: time.Now(), Content: fmt.Sprintf("[system] failed to start: %v", err), Stream: "system"})
			closeDone()
			return
		}

		inst.mu.Lock()
		inst.status = StatusRunning
		inst.pid = cmd.Process.Pid
		inst.startedAt = time.Now()
		inst.mu.Unlock()

		go func() {
			sc := bufio.NewScanner(stdout)
			for sc.Scan() {
				inst.pushLog(LogLine{Time: time.Now(), Content: sc.Text(), Stream: "stdout"})
			}
		}()
		go func() {
			sc := bufio.NewScanner(stderr)
			for sc.Scan() {
				inst.pushLog(LogLine{Time: time.Now(), Content: sc.Text(), Stream: "stderr"})
			}
		}()

		waitCh := make(chan error, 1)
		go func() { waitCh <- cmd.Wait() }()

		cleanup := func() {
			inst.stdinMu.Lock()
			inst.stdinW = nil
			inst.stdinMu.Unlock()
			_ = stdinW.Close()
			_ = stdinR.Close()
		}

		select {
		case err := <-waitCh:
			cleanup()
			// Check if the exit was triggered by a Stop() call.
			wasStop := false
			select {
			case <-stopCh:
				wasStop = true
			default:
			}
			inst.mu.Lock()
			inst.cmd = nil
			if wasStop {
				inst.status = StatusStopped
				inst.mu.Unlock()
				inst.pushLog(LogLine{Time: time.Now(), Content: "[system] process stopped", Stream: "system"})
				closeDone()
				return
			}
			inst.status = StatusCrashed
			inst.mu.Unlock()

			exitMsg := "[system] process exited cleanly"
			if err != nil {
				exitMsg = fmt.Sprintf("[system] process crashed: %v", err)
			}
			inst.pushLog(LogLine{Time: time.Now(), Content: exitMsg, Stream: "system"})

			autoRestart := cfg.AutoRestart
			if autoRestart != config.AutoRestartAlways && autoRestart != config.AutoRestartOnCrash {
				closeDone()
				return
			}

			// Auto-restart: wait 3 s but bail immediately if Stop() fires.
			inst.pushLog(LogLine{Time: time.Now(), Content: "[system] auto-restarting in 3s...", Stream: "system"})
			inst.mu.Lock()
			currentStop := inst.stopCh
			inst.mu.Unlock()
			select {
			case <-currentStop:
				// Stop() was called during the sleep — honour it.
				inst.mu.Lock()
				inst.status = StatusStopped
				inst.mu.Unlock()
				inst.pushLog(LogLine{Time: time.Now(), Content: "[system] process stopped", Stream: "system"})
				closeDone()
				return
			case <-time.After(3 * time.Second):
			}

			// Check again after the sleep.
			inst.mu.Lock()
			select {
			case <-inst.stopCh:
				inst.status = StatusStopped
				inst.mu.Unlock()
				inst.pushLog(LogLine{Time: time.Now(), Content: "[system] process stopped", Stream: "system"})
				closeDone()
				return
			default:
			}
			inst.status = StatusStarting
			inst.mu.Unlock()
			isRestart = true
			// continue the loop → restart

		case <-stopCh:
			killGroup(cmd)
			<-waitCh
			cleanup()
			inst.mu.Lock()
			inst.status = StatusStopped
			inst.cmd = nil
			inst.mu.Unlock()
			inst.pushLog(LogLine{Time: time.Now(), Content: "[system] process stopped", Stream: "system"})
			closeDone()
			return
		}
	}
}

func (inst *Instance) DiskUsage() (int64, error) {
	dir := inst.cfg.WorkDir
	if dir == "" {
		return 0, nil
	}
	var size int64
	err := filepath.Walk(dir, func(_ string, info os.FileInfo, err error) error {
		if err != nil {
			return nil
		}
		if !info.IsDir() {
			size += info.Size()
		}
		return nil
	})
	return size, err
}
