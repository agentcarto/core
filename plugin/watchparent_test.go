package plugin

// Process-tree test for the orphan watchdog: a plugin whose host dies without
// the graceful Kill must exit by itself once it is reparented.

import (
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

// TestHelperWatchParent is not a real test: re-executed as a subprocess by
// TestWatchParentExitsWhenParentDies, it runs the watchdog, reports readiness
// (the PPID is captured at the go statement, before the ready file appears),
// and then hangs. It exits 0 only if the watchdog fires.
func TestHelperWatchParent(t *testing.T) {
	if os.Getenv("GO_WATCHPARENT_HELPER") != "1" {
		t.Skip("helper mode only")
	}
	go watchParent(os.Getppid(), 20*time.Millisecond)
	if f := os.Getenv("GO_WATCHPARENT_READY"); f != "" {
		_ = os.WriteFile(f, nil, 0600)
	}
	time.Sleep(30 * time.Second)
	os.Exit(1) // the watchdog did not fire
}

func TestWatchParentExitsWhenParentDies(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("PPID does not change on windows")
	}
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	// The intermediate sh is the helper's parent; it backgrounds the helper,
	// prints its pid, waits until the helper has captured its real PPID, and
	// exits — orphaning the helper exactly like a host that died without Kill.
	ready := fmt.Sprintf("%s/ready", t.TempDir())
	script := fmt.Sprintf(`%q -test.run=TestHelperWatchParent >/dev/null 2>&1 & echo $!
i=0; while [ ! -e %q ] && [ $i -lt 100 ]; do sleep 0.05; i=$((i+1)); done`, exe, ready)
	cmd := exec.Command("sh", "-c", script)
	cmd.Env = append(os.Environ(), "GO_WATCHPARENT_HELPER=1", "GO_WATCHPARENT_READY="+ready)
	out, err := cmd.Output()
	if err != nil {
		t.Fatal(err)
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(out)))
	if err != nil {
		t.Fatalf("helper pid: %v (%q)", err, out)
	}
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		// Signal 0 probes existence without touching the process.
		if err := syscall.Kill(pid, 0); err != nil {
			return // helper exited: the watchdog fired
		}
		time.Sleep(50 * time.Millisecond)
	}
	_ = syscall.Kill(pid, syscall.SIGKILL)
	t.Fatal("orphaned helper still running: watchdog did not fire")
}
