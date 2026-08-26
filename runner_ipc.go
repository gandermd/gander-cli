package main

import (
	"encoding/json"
	"fmt"
	"log"
	"net"
	"os"
	"time"
)

type ipcRequest struct {
	Op      string `json:"op"`
	ID      string `json:"id,omitempty"`
	Path    string `json:"path,omitempty"`
	Mode    string `json:"mode,omitempty"`
	All     bool   `json:"all,omitempty"`
	ShortID string `json:"short_id,omitempty"`
	UUID    string `json:"uuid,omitempty"`
	ShareURL string `json:"share_url,omitempty"`
}

type ipcResponse struct {
	OK       bool       `json:"ok"`
	Error    string     `json:"error,omitempty"`
	ID       string     `json:"id,omitempty"`
	URL      string     `json:"url,omitempty"`
	ShareURL string     `json:"share_url,omitempty"`
	ShortID  string     `json:"short_id,omitempty"`
	UUID     string     `json:"uuid,omitempty"`
	Watches  []watchOut `json:"watches,omitempty"`
	Removed  []string   `json:"removed,omitempty"`
	Version  string     `json:"version,omitempty"`
	UptimeS  int64      `json:"uptime_s,omitempty"`
}

type watchOut struct {
	ID        string `json:"id"`
	Path      string `json:"path"`
	Mode      string `json:"mode"`
	URL       string `json:"url,omitempty"`
	ShareURL  string `json:"share_url,omitempty"`
	ShortID   string `json:"short_id,omitempty"`
	UUID      string `json:"uuid,omitempty"`
	StartedAt string `json:"started_at"`
}

type ipcServer struct {
	ln      net.Listener
	mgr     *watchManager
	version string
	started time.Time
}

func newIPCSrv(ln net.Listener, mgr *watchManager, version string) *ipcServer {
	return &ipcServer{ln: ln, mgr: mgr, version: version, started: time.Now()}
}

func (s *ipcServer) serve() {
	for {
		c, err := s.ln.Accept()
		if err != nil {
			return
		}
		go s.handle(c)
	}
}

func (s *ipcServer) handle(c net.Conn) {
	defer c.Close()

	uc, ok := c.(*net.UnixConn)
	if !ok {
		return
	}
	sc, err := uc.SyscallConn()
	if err != nil {
		return
	}
	var peerErr error
	if err := sc.Control(func(fd uintptr) {
		uid, err := peerUID(int(fd))
		if err != nil {
			peerErr = err
			return
		}
		if uid != uint32(os.Geteuid()) {
			peerErr = fmt.Errorf("peer UID %d != ours %d", uid, os.Geteuid())
		}
	}); err != nil {
		return
	}
	if peerErr != nil {
		log.Printf("ipc: rejecting connection: %v", peerErr)
		return
	}

	c.SetDeadline(time.Now().Add(5 * time.Second))

	dec := json.NewDecoder(c)
	dec.DisallowUnknownFields()
	var req ipcRequest
	if err := dec.Decode(&req); err != nil {
		return
	}

	resp := s.route(req)
	if err := json.NewEncoder(c).Encode(resp); err != nil {
		log.Printf("ipc: encode: %v", err)
	}
}

func (s *ipcServer) route(req ipcRequest) ipcResponse {
	switch req.Op {
	case "ping":
		return ipcResponse{OK: true, Version: s.version, UptimeS: int64(time.Since(s.started).Seconds())}
	case "watch":
		if req.Path == "" {
			return ipcResponse{Error: "path required"}
		}
		info, err := s.mgr.register(req.Path, req.Mode, shareRef{UUID: req.UUID, ShortID: req.ShortID, URL: req.ShareURL})
		if err != nil {
			return ipcResponse{Error: err.Error()}
		}
		return ipcResponse{OK: true, ID: info.ID, URL: info.URL, ShareURL: info.ShareURL}
	case "stop":
		var removed []string
		if req.All {
			removed = s.mgr.stopAll()
		} else if req.ID != "" {
			if s.mgr.stopByID(req.ID) {
				removed = []string{req.ID}
			}
		} else if req.Path != "" {
			removed = s.mgr.stopByPath(req.Path)
		} else {
			return ipcResponse{Error: "id, path, or all required"}
		}
		return ipcResponse{OK: true, Removed: removed}
	case "list":
		return ipcResponse{OK: true, Watches: s.mgr.list(), Version: s.version, UptimeS: int64(time.Since(s.started).Seconds())}
	case "shutdown":
		go func() {
			time.Sleep(50 * time.Millisecond)
			p, _ := os.FindProcess(os.Getpid())
			if p != nil {
				_ = p.Signal(os.Interrupt)
			}
		}()
		return ipcResponse{OK: true}
	default:
		return ipcResponse{Error: "unknown op: " + req.Op}
	}
}

func peerUID(fd int) (uint32, error) {
	return peerUIDPlatform(fd)
}
