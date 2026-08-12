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
	outFile := flag.String("outfile", "", "Optional: write HTML output to file instead of opening in browser")
	watch := flag.Bool("watch", false, "Watch the file for changes and live-reload the browser preview")
	flag.Parse()

	args := flag.Args()
	if len(args) == 0 {
		flag.Usage()
		fmt.Fprintf(flag.CommandLine.Output(), "\nUsage: mdp <file.md> [options]\n\nOptions:\n")
		flag.PrintDefaults()
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
	openBrowser(url)
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

func openBrowser(url string) {
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
	name := fmt.Sprintf("mdp-%x.html", sum[:8])
	path := filepath.Join(os.TempDir(), name)
	if err := writeHTMLTo(path, content); err != nil {
		return "", err
	}
	return path, nil
}