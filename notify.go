package main

import (
	"fmt"
	"log"
	"os/exec"
	"runtime"
	"strconv"
	"sync"
	"time"
)

const notifyDebounce = 5 * time.Second

type commentNotifier struct {
	mu      sync.Mutex
	pending map[string]*notifyBurst
}

type notifyBurst struct {
	timer    *time.Timer
	filename string
	name     string
	n        int
}

func newCommentNotifier() *commentNotifier {
	return &commentNotifier{pending: map[string]*notifyBurst{}}
}

func (n *commentNotifier) note(key, filename, name string) {
	n.mu.Lock()
	defer n.mu.Unlock()
	b, ok := n.pending[key]
	if !ok {
		b = &notifyBurst{filename: filename, name: name, n: 1}
		b.timer = time.AfterFunc(notifyDebounce, func() {
			n.flush(key)
		})
		n.pending[key] = b
		return
	}
	b.n++
	b.name = name
	b.filename = filename
	b.timer.Reset(notifyDebounce)
}

func (n *commentNotifier) flush(key string) {
	n.mu.Lock()
	b, ok := n.pending[key]
	delete(n.pending, key)
	n.mu.Unlock()
	if !ok {
		return
	}
	body := fmt.Sprintf("%s commented on %s", b.name, b.filename)
	if b.n > 1 {
		body = fmt.Sprintf("%d comments on %s", b.n, b.filename)
	}
	log.Printf("notify: %s", body)
	sendOSNotification("Gander", body)
}

func sendOSNotification(title, body string) {
	switch runtime.GOOS {
	case "darwin":
		script := fmt.Sprintf("display notification %s with title %s", strconv.Quote(body), strconv.Quote(title))
		_ = exec.Command("osascript", "-e", script).Run()
	case "linux":
		_ = exec.Command("notify-send", title, body).Run()
	}
}
