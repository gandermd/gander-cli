package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"
	"time"
)

var shareCommentNotifier = newCommentNotifier()

func listenShareComments(ctx context.Context, eventsURL, filename string, n *commentNotifier) {
	backoff := time.Second
	for {
		err := consumeCommentSSE(ctx, eventsURL, filename, n)
		if ctx.Err() != nil {
			return
		}
		if err != nil && err != io.EOF {
			log.Printf("comment sse %s: %v", filename, err)
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(backoff):
		}
		if backoff < 30*time.Second {
			backoff *= 2
		}
	}
}

func consumeCommentSSE(ctx context.Context, eventsURL, filename string, n *commentNotifier) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, eventsURL, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "text/event-stream")
	client := &http.Client{Timeout: 0}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	return readCommentSSE(resp.Body, filename, n)
}

func readCommentSSE(r io.Reader, filename string, n *commentNotifier) error {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64*1024), 8*1024*1024)
	var event string
	var data bytes.Buffer
	flush := func() {
		if event == "comment" && data.Len() > 0 {
			handleCommentEvent(data.Bytes(), filename, n)
		}
		event = ""
		data.Reset()
	}
	for sc.Scan() {
		line := sc.Text()
		if line == "" {
			flush()
			continue
		}
		if strings.HasPrefix(line, "event:") {
			event = strings.TrimSpace(strings.TrimPrefix(line, "event:"))
			continue
		}
		if strings.HasPrefix(line, "data:") {
			if data.Len() > 0 {
				data.WriteByte('\n')
			}
			data.WriteString(strings.TrimSpace(strings.TrimPrefix(line, "data:")))
		}
	}
	flush()
	return sc.Err()
}

func handleCommentEvent(raw []byte, filename string, n *commentNotifier) {
	var payload struct {
		Op     string `json:"op"`
		Thread struct {
			Comments []commentView `json:"comments"`
		} `json:"thread"`
	}
	if err := json.Unmarshal(raw, &payload); err != nil {
		return
	}
	if payload.Op != "created" && payload.Op != "replied" {
		return
	}
	if len(payload.Thread.Comments) == 0 {
		return
	}
	last := payload.Thread.Comments[len(payload.Thread.Comments)-1]
	if last.AuthorKind == "author" || last.AuthorKind == "agent" {
		return
	}
	name := last.AuthorName
	if name == "" {
		name = "Someone"
	}
	n.note(filename, filename, name)
}
