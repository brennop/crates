package player

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"
)

// DefaultPidFile is where the app keeps the pidfile: the user cache dir.
func DefaultPidFile() (string, error) {
	dir, err := os.UserCacheDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "crates", "mpv.pid"), nil
}

// The pidfile holds two lines: the mpv PID and its socket path. The socket
// path is what makes the orphan check exact.
func writePidfile(path string, pid int, sock string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, fmt.Appendf(nil, "%d\n%s\n", pid, sock), 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

func removePidfile(path string) {
	if path != "" {
		os.Remove(path)
	}
}

func readPidfile(path string) (pid int, sock string, err error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return 0, "", err
	}
	lines := strings.SplitN(strings.TrimRight(string(b), "\n"), "\n", 2)
	if len(lines) != 2 || lines[1] == "" {
		return 0, "", fmt.Errorf("player: malformed pidfile %s", path)
	}
	pid, err = strconv.Atoi(lines[0])
	if err != nil || pid <= 1 {
		return 0, "", fmt.Errorf("player: malformed pidfile %s", path)
	}
	return pid, lines[1], nil
}

// CleanOrphan reads the pidfile left by an earlier run. It kills the PID
// only when that process is alive, is an orphan (its parent is launchd,
// PID 1), and its command is mpv with this app's --input-ipc-server socket.
// Anything else is left alone. The pidfile is removed either way. It never
// kills by name or pattern.
func CleanOrphan(pidfile string) (killed bool, err error) {
	pid, sock, err := readPidfile(pidfile)
	if os.IsNotExist(err) {
		return false, nil
	}
	defer os.Remove(pidfile)
	if err != nil {
		return false, nil // unreadable: nothing to trust in it
	}
	if !isOrphanMPV(pid, sock) {
		return false, nil
	}
	if err := syscall.Kill(pid, syscall.SIGTERM); err != nil {
		if err == syscall.ESRCH {
			return false, nil
		}
		return false, fmt.Errorf("player: kill orphan mpv %d: %w", pid, err)
	}
	if !waitGone(pid, 2*time.Second) {
		syscall.Kill(pid, syscall.SIGKILL)
		waitGone(pid, time.Second)
	}
	if dir := filepath.Dir(sock); strings.HasPrefix(filepath.Base(dir), socketDirPrefix) {
		os.RemoveAll(dir)
	}
	return true, nil
}

// isOrphanMPV asks ps for the parent PID and command of pid.
func isOrphanMPV(pid int, sock string) bool {
	out, err := exec.Command("ps", "-p", strconv.Itoa(pid), "-o", "ppid=,command=").Output()
	if err != nil {
		return false // ps exits 1 when there is no such process
	}
	ppid, cmdline, ok := strings.Cut(strings.TrimSpace(string(bytes.TrimSpace(out))), " ")
	if !ok || strings.TrimSpace(ppid) != "1" {
		return false
	}
	cmdline = strings.TrimSpace(cmdline)
	bin, _, _ := strings.Cut(cmdline, " ")
	return filepath.Base(bin) == "mpv" && strings.Contains(cmdline, " --input-ipc-server="+sock)
}

func waitGone(pid int, d time.Duration) bool {
	deadline := time.Now().Add(d)
	for time.Now().Before(deadline) {
		if syscall.Kill(pid, 0) == syscall.ESRCH {
			return true
		}
		time.Sleep(20 * time.Millisecond)
	}
	return false
}
