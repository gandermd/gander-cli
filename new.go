package main

import (
	"bufio"
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

func runNew(args []string) error {
	return runNewWithCtx(context.Background(), args, defaultNewIO())
}

type newIO struct {
	stdin  io.Reader
	stdout io.Writer
	stderr io.Writer
	isTTY  bool
}

func defaultNewIO() newIO {
	return newIO{
		stdin:  os.Stdin,
		stdout: os.Stdout,
		stderr: os.Stderr,
		isTTY:  stdinIsTTY(),
	}
}

func runNewWithCtx(ctx context.Context, args []string, nio newIO) error {
	if _, err := requireAuth(); err != nil {
		return err
	}
	fs := flag.NewFlagSet("new", flag.ContinueOnError)
	if nio.stderr != nil {
		fs.SetOutput(nio.stderr)
	}
	silent := fs.Bool("silent", false, "create and share without opening a browser")
	foreground := fs.Bool("foreground", false, "keep the watch in-process instead of handing off to the runner")
	if err := fs.Parse(args); err != nil {
		return err
	}
	name, err := resolveNewName(fs.Args(), nio)
	if err != nil {
		return err
	}
	path, err := createNewMarkdown(name)
	if err != nil {
		return err
	}
	fmt.Fprintf(nio.out(), "Created %s\n", path)
	return runShareOpeningEditor(ctx, shareWatchArgs(*silent, *foreground, path))
}

func (n newIO) out() io.Writer {
	if n.stdout != nil {
		return n.stdout
	}
	return os.Stdout
}

func resolveNewName(args []string, nio newIO) (string, error) {
	switch len(args) {
	case 1:
		return args[0], nil
	case 0:
		if !nio.isTTY {
			return "", fmt.Errorf("usage: gander new [--silent] [--foreground] [filename.md]")
		}
		fmt.Fprint(nio.out(), "File name: ")
		in := nio.stdin
		if in == nil {
			in = os.Stdin
		}
		line, err := readLine(bufio.NewReader(in))
		if err != nil && strings.TrimSpace(line) == "" {
			if err == io.EOF {
				return "", fmt.Errorf("file name required")
			}
			return "", err
		}
		line = strings.TrimSpace(line)
		if line == "" {
			return "", fmt.Errorf("file name required")
		}
		return line, nil
	default:
		return "", fmt.Errorf("usage: gander new [--silent] [--foreground] [filename.md]")
	}
}

func createNewMarkdown(name string) (string, error) {
	name = strings.TrimSpace(name)
	if name == "" || strings.ContainsRune(name, 0) {
		return "", fmt.Errorf("invalid file name")
	}
	clean := filepath.Clean(name)
	if clean == "." || clean == string(filepath.Separator) {
		return "", fmt.Errorf("invalid file name")
	}
	if fi, err := os.Stat(clean); err == nil {
		if fi.IsDir() {
			return "", fmt.Errorf("%s is a directory", clean)
		}
		return "", fmt.Errorf("%s already exists", clean)
	} else if !os.IsNotExist(err) {
		return "", err
	}
	if dir := filepath.Dir(clean); dir != "." && dir != "" {
		if err := os.MkdirAll(dir, 0755); err != nil {
			return "", err
		}
	}
	if err := os.WriteFile(clean, []byte(newMarkdownBody(clean)), 0644); err != nil {
		return "", err
	}
	return clean, nil
}

func newMarkdownBody(path string) string {
	stem := strings.TrimSuffix(filepath.Base(path), filepath.Ext(path))
	stem = strings.NewReplacer("-", " ", "_", " ").Replace(stem)
	stem = strings.TrimSpace(stem)
	if stem == "" {
		stem = "Untitled"
	}
	return "# " + stem + "\n\n"
}

func shareWatchArgs(silent, foreground bool, path string) []string {
	args := []string{"--watch"}
	if silent {
		args = append(args, "--silent")
	}
	if foreground {
		args = append(args, "--foreground")
	}
	args = append(args, path)
	return args
}
