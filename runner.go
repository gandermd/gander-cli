package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strconv"
	"syscall"
	"time"
)

const (
	runnerSockName = "runner.sock"
	runnerLockName = "runner.lock"
	runnerPIDName  = "runner.pid"
	runnerLogName  = "runner.log"
	runnerErrName  = "runner.err"
)

func runRunner(args []string) error {
	home, err := runnerHome()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(home, 0700); err != nil {
		return err
	}

	lockF, err := os.OpenFile(filepath.Join(home, runnerLockName), os.O_RDWR|os.O_CREATE, 0600)
	if err != nil {
		return err
	}
	defer lockF.Close()
	if err := syscall.Flock(int(lockF.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		log.Printf("runner: another daemon holds the lock; exiting")
		return nil
	}

	sockPath := filepath.Join(home, runnerSockName)
	if existing, _ := os.Stat(sockPath); existing != nil {
		if conn, derr := net.DialTimeout("unix", sockPath, 200*time.Millisecond); derr == nil {
			conn.Close()
			log.Printf("runner: UDS already answerable at %s; exiting", sockPath)
			return nil
		}
		os.Remove(sockPath)
	}
	ipcLn, err := net.Listen("unix", sockPath)
	if err != nil {
		return fmt.Errorf("listen %s: %w", sockPath, err)
	}
	if err := os.Chmod(sockPath, 0600); err != nil {
		ipcLn.Close()
		return fmt.Errorf("chmod socket: %w", err)
	}
	defer os.Remove(sockPath)

	pidPath := filepath.Join(home, runnerPIDName)
	if err := os.WriteFile(pidPath, []byte(strconv.Itoa(os.Getpid())), 0600); err != nil {
		ipcLn.Close()
		return fmt.Errorf("write pid: %w", err)
	}
	defer os.Remove(pidPath)

	mgr := newWatchManager(home)
	if err := mgr.load(); err != nil {
		log.Printf("runner: watches: %v", err)
	}
	if err := mgr.ensureDaemonToken(); err != nil {
		ipcLn.Close()
		os.Remove(sockPath)
		return fmt.Errorf("daemon token: %w", err)
	}

	http, err := newRunnerHTTP(mgr)
	if err != nil {
		ipcLn.Close()
		os.Remove(sockPath)
		return fmt.Errorf("http: %w", err)
	}

	ipc := newIPCSrv(ipcLn, mgr, Version)
	go ipc.serve()
	go http.serve()

	mgr.resumeAll()
	if err := mgr.persist(); err != nil {
		log.Printf("runner: persist after resume: %v", err)
	}

	sigCtx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	<-sigCtx.Done()

	log.Printf("runner: shutting down")
	http.shutdown()
	mgr.persist()
	return nil
}

func runnerHome() (string, error) {
	return configPath()
}

func ensureRunner(home string) (string, error) {
	sockPath := filepath.Join(home, runnerSockName)
	if c, err := net.DialTimeout("unix", sockPath, 200*time.Millisecond); err == nil {
		c.Close()
		return sockPath, nil
	}
	os.Remove(sockPath)

	exe, err := os.Executable()
	if err != nil {
		return "", err
	}
	devnull, err := os.Open(os.DevNull)
	if err != nil {
		return "", err
	}
	logF, lerr := os.OpenFile(filepath.Join(home, runnerLogName), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0600)
	if lerr != nil {
		logF = nil
	}
	errF, lerr := os.OpenFile(filepath.Join(home, runnerErrName), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0600)
	if lerr != nil {
		errF = nil
	}
	cmd := exec.Command(exe, "_serve")
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Stdin = devnull
	if logF != nil {
		cmd.Stdout = logF
	} else {
		cmd.Stdout = devnull
	}
	// Go's default log package writes to os.Stderr; route it to runner.log
	// so `gander logs` picks up the same lines as plain stderr noise.
	if logF != nil {
		cmd.Stderr = logF
	} else if errF != nil {
		cmd.Stderr = errF
	} else {
		cmd.Stderr = devnull
	}
	if err := cmd.Start(); err != nil {
		devnull.Close()
		return "", err
	}
	autoInstallIfNeeded()

	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if c, err := net.DialTimeout("unix", sockPath, 100*time.Millisecond); err == nil {
			c.Close()
			devnull.Close()
			return sockPath, nil
		}
		time.Sleep(50 * time.Millisecond)
	}
	devnull.Close()
	return "", fmt.Errorf("runner did not bind UDS within 5s (see %s)", filepath.Join(home, runnerLogName))
}

func dialRunner(home string) (net.Conn, error) {
	sock := filepath.Join(home, runnerSockName)
	return net.DialTimeout("unix", sock, 1*time.Second)
}

// ipcRoundTrip is exported for tests; production code uses a thin helper around it.
func ipcRoundTrip(home string, req ipcRequest) (ipcResponse, error) {
	conn, err := dialRunner(home)
	if err != nil {
		return ipcResponse{}, err
	}
	defer conn.Close()
	conn.SetDeadline(time.Now().Add(5 * time.Second))
	if err := json.NewEncoder(conn).Encode(req); err != nil {
		return ipcResponse{}, err
	}
	var resp ipcResponse
	if err := json.NewDecoder(conn).Decode(&resp); err != nil {
		if errors.Is(err, io.EOF) {
			return ipcResponse{}, fmt.Errorf("runner closed connection without responding")
		}
		return ipcResponse{}, err
	}
	if !resp.OK && resp.Error == "" {
		resp.Error = "unknown error"
	}
	return resp, nil
}

// runnerHomeForCLI returns the runner home dir the CLI side uses. Same
// resolution as the daemon — keeps GANDER_CONFIG respected on both sides.
func runnerHomeForCLI() (string, error) {
	return runnerHome()
}

func handOffWatch(path string) error {
	home, err := runnerHomeForCLI()
	if err != nil {
		return err
	}
	if _, err := ensureRunner(home); err != nil {
		return err
	}
	canonical, err := canonicalPath(path)
	if err != nil {
		return err
	}
	resp, err := ipcRoundTrip(home, ipcRequest{Op: "watch", Path: canonical, Mode: "local"})
	if err != nil {
		return err
	}
	if !resp.OK {
		return fmt.Errorf("runner rejected watch: %s", resp.Error)
	}
	fmt.Printf("Preview at: %s\n", resp.URL)
	if err := openBrowser(resp.URL); err != nil {
		log.Printf("warning: could not open browser: %v", err)
	}
	return nil
}
