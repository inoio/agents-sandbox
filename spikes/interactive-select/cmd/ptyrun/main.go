package main

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"sync"
	"time"

	"github.com/creack/pty"
)

func main() {
	if len(os.Args) < 3 {
		fmt.Fprintln(os.Stderr, "usage: ptyrun <script-file> <cmd> [args...]")
		os.Exit(2)
	}

	timeout := 6000 * time.Millisecond
	if v := os.Getenv("PTYRUN_TIMEOUT_MS"); v != "" {
		if ms, err := strconv.Atoi(v); err == nil {
			timeout = time.Duration(ms) * time.Millisecond
		}
	}

	script, err := os.ReadFile(os.Args[1])
	if err != nil {
		fmt.Fprintf(os.Stderr, "read script: %v\n", err)
		os.Exit(2)
	}

	cmd := exec.Command(os.Args[2], os.Args[3:]...)
	cmd.Env = os.Environ()

	ptmx, err := pty.StartWithSize(cmd, &pty.Winsize{Rows: 24, Cols: 80})
	if err != nil {
		fmt.Fprintf(os.Stderr, "start pty: %v\n", err)
		os.Exit(2)
	}

	var captured bytes.Buffer
	var outputMu sync.Mutex
	outputDone := make(chan struct{})
	go func() {
		buf := make([]byte, 4096)
		for {
			n, readErr := ptmx.Read(buf)
			if n > 0 {
				outputMu.Lock()
				captured.Write(buf[:n])
				outputMu.Unlock()
			}
			if readErr != nil {
				break
			}
		}
		close(outputDone)
	}()

	go func() {
		time.Sleep(400 * time.Millisecond)
		for _, chunk := range bytes.Split(script, []byte("\n")) {
			if len(chunk) == 0 {
				continue
			}
			_, _ = ptmx.Write(chunk)
			time.Sleep(200 * time.Millisecond)
		}
	}()

	timedOut := false
	waitDone := make(chan error, 1)
	go func() { waitDone <- cmd.Wait() }()

	select {
	case <-waitDone:
	case <-time.After(timeout):
		timedOut = true
		_ = cmd.Process.Kill()
		<-waitDone
	}

	_ = ptmx.Close()
	select {
	case <-outputDone:
	case <-time.After(time.Second):
	}

	outputMu.Lock()
	raw := captured.String()
	outputMu.Unlock()

	exit := -1
	if cmd.ProcessState != nil {
		exit = cmd.ProcessState.ExitCode()
	}
	fmt.Fprintf(os.Stderr, "---PTYRUN exit=%d timedOut=%v---\n", exit, timedOut)
	fmt.Printf("PTYRUN_RAW=%q\n", raw)
	if path := os.Getenv("PTYRUN_RAW_FILE"); path != "" {
		_ = os.WriteFile(path, []byte(raw), 0o600)
	}
}
