package main

import (
	"crypto/sha256"
	"flag"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
)

func main() {
	log.SetFlags(0)

	if len(os.Args) > 1 {
		switch os.Args[1] {
		case "signup":
			if err := runSignup(os.Args[2:]); err != nil {
				fmt.Fprintf(os.Stderr, "signup: %v\n", err)
				os.Exit(1)
			}
			return
		case "share":
			if err := runShare(os.Args[2:]); err != nil {
				fmt.Fprintf(os.Stderr, "share: %v\n", err)
				os.Exit(1)
			}
			return
		case "remove":
			if err := runRemove(os.Args[2:]); err != nil {
				fmt.Fprintf(os.Stderr, "remove: %v\n", err)
				os.Exit(1)
			}
			return
		case "list":
			if err := runList(os.Args[2:]); err != nil {
				fmt.Fprintf(os.Stderr, "list: %v\n", err)
				os.Exit(1)
			}
			return
		case "--help", "-h", "help":
			printUsage(os.Stdout)
			return
		}
	}

	if len(os.Args) > 1 && (os.Args[1] == "--upgrade" || os.Args[1] == "upgrade") {
		if len(os.Args) > 2 {
			fmt.Fprintln(os.Stderr, "error: --upgrade takes no arguments")
			os.Exit(1)
		}
		if err := runUpgrade(); err != nil {
			log.Fatalf("upgrade: %v", err)
		}
		return
	}

	outFile := flag.String("outfile", "", "Optional: write HTML output to file instead of opening in browser")
	watch := flag.Bool("watch", false, "Watch the file for changes and live-reload the browser preview")
	upgrade := flag.Bool("upgrade", false, "Download and install the latest release, then exit")
	flag.Parse()

	if *upgrade {
		if err := runUpgrade(); err != nil {
			log.Fatalf("upgrade: %v", err)
		}
		return
	}

	args := flag.Args()
	if len(args) == 0 {
		printUsage(os.Stderr)
		os.Exit(1)
	}

	inFile := args[0]
	absPath, err := filepath.Abs(inFile)
	if err != nil {
		log.Fatalf("Failed to resolve file path: %v", err)
	}

	if *outFile != "" && *watch {
		fmt.Fprintln(os.Stderr, "error: -outfile and --watch cannot be used together")
		os.Exit(1)
	}

	cfg, err := LoadConfig()
	if err != nil {
		log.Printf("config: %v (using defaults)", err)
	}

	useWatch := *watch
	if !flagWasSet("watch") {
		useWatch = cfg.Watch
	}

	if useWatch {
		if err := runWatch(absPath, cfg); err != nil {
			log.Fatalf("watch: %v", err)
		}
		fmt.Println("Stopped.")
		return
	}

	content, err := os.ReadFile(absPath)
	if err != nil {
		log.Fatalf("Failed to read file: %v", err)
	}

	if *outFile != "" {
		if err := writeHTMLTo(*outFile, content); err != nil {
			log.Fatalf("Failed to write HTML: %v", err)
		}
		fmt.Printf("HTML written to: %s\n", *outFile)
		return
	}

	tmpPath, err := writeHTMLToTemp(content)
	if err != nil {
		log.Fatalf("Failed to write temp HTML: %v", err)
	}
	url := "file://" + tmpPath
	fmt.Printf("Preview at: %s\n", url)
	openBrowserLocal(url)
}

func printUsage(w *os.File) {
	cfg, _ := LoadConfig()
	authed := cfg.APIToken != ""

	fmt.Fprintln(w, "gander — render Markdown, optionally share it on gander.md")
	fmt.Fprintln(w)
	fmt.Fprintln(w, "Usage:")
	fmt.Fprintln(w, "  gander <file.md> [options]      Render and open locally")
	fmt.Fprintln(w, "  gander signup --email <addr>    Create a gander.md account and save the API token")
	if authed {
		fmt.Fprintln(w, "  gander share [--watch] <file>   Upload to gander.md and open the share link")
		fmt.Fprintln(w, "  gander remove [--all] [<file>]  Delete a share from gander.md")
		fmt.Fprintln(w, "  gander list                     List shares currently on gander.md")
	}
	fmt.Fprintln(w, "  gander --upgrade                Download and install the latest release")
	fmt.Fprintln(w)
	fmt.Fprintln(w, "Render options:")
	fmt.Fprintln(w, "  -outfile string   Write HTML to a file instead of opening in browser")
	fmt.Fprintln(w, "  -watch            Live-reload the local browser preview on save")
	if !authed {
		fmt.Fprintln(w)
		fmt.Fprintln(w, "Run `gander signup --email you@example.com` to enable share / remove / list.")
	}
}

func flagWasSet(name string) bool {
	found := false
	flag.Visit(func(f *flag.Flag) {
		if f.Name == name {
			found = true
		}
	})
	return found
}

func openBrowserLocal(url string) {
	cmd := exec.Command("open", url)
	if err := cmd.Start(); err != nil {
		log.Printf("Warning: could not open browser: %v", err)
	}
}

func writeHTMLTo(outPath string, content []byte) error {
	html, headings := renderMarkdownWithIDs(string(content))
	page := buildHTML(html, headings, false)
	return os.WriteFile(outPath, []byte(page), 0644)
}

func writeHTMLToTemp(content []byte) (string, error) {
	sum := sha256.Sum256(content)
	name := fmt.Sprintf("gander-%x.html", sum[:8])
	path := filepath.Join(os.TempDir(), name)
	if err := writeHTMLTo(path, content); err != nil {
		return "", err
	}
	return path, nil
}