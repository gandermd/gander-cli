package main

import (
	"bufio"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"
)

func runLogs(args []string) error {
	fs := flag.NewFlagSet("logs", flag.ContinueOnError)
	follow := fs.Bool("follow", true, "follow new log lines (default true)")
	noFollow := fs.Bool("no-follow", false, "print existing log lines and exit")
	if err := fs.Parse(args); err != nil {
		return err
	}
	rest := fs.Args()

	home, err := runnerHomeForCLI()
	if err != nil {
		return err
	}

	idFilter := ""
	switch len(rest) {
	case 0:
	case 1:
		idFilter = rest[0]
	default:
		return fmt.Errorf("usage: gander logs [<id>] [--follow|--no-follow]")
	}

	logPath := filepath.Join(home, "runner.log")
	f, err := os.Open(logPath)
	if err != nil {
		return fmt.Errorf("no log at %s; is the runner running?", logPath)
	}
	defer f.Close()

	printFrom(f, idFilter)

	if !*follow || *noFollow {
		return nil
	}

	// tail -f style: every 500ms, read whatever's new.
	tick := time.NewTicker(500 * time.Millisecond)
	defer tick.Stop()
	last := readBytes(f)
	for range tick.C {
		cur := readBytes(f)
		if cur > last {
			f2, err := os.Open(logPath)
			if err != nil {
				continue
			}
			_, _ = f2.Seek(int64(last), io.SeekStart)
			printFrom(f2, idFilter)
			f2.Close()
			last = cur
		}
	}
	return nil
}

func readBytes(f *os.File) int64 {
	st, err := f.Stat()
	if err != nil {
		return 0
	}
	return st.Size()
}

func printFrom(f *os.File, idFilter string) {
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := scanner.Text()
		if idFilter != "" && !strings.Contains(line, idFilter) {
			continue
		}
		fmt.Println(line)
	}
}
