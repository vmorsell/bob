package main

import (
	"bufio"
	"bytes"
	_ "embed"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/google/uuid"
)

//go:embed ui/index.html
var monitorHTML string

// EventType identifies the kind of monitoring event.
type EventType string

const (
	EventJobStarted        EventType = "job_started"
	EventLLMCall           EventType = "llm_call"
	EventLLMResponse       EventType = "llm_response"
	EventToolStarted       EventType = "tool_started"
	EventClaudeCodeLine    EventType = "claude_code_line"
	EventToolCompleted     EventType = "tool_completed"
	EventSlackNotification EventType = "slack_notification"
	EventJobCompleted      EventType = "job_completed"
	EventJobError          EventType = "job_error"
)

// Event is a single monitoring event.
type Event struct {
	ID        string         `json:"id"`
	JobID     string         `json:"job_id"`
	Type      EventType      `json:"type"`
	Timestamp time.Time      `json:"timestamp"`
	Data      map[string]any `json:"data"`
}

type sseClient struct {
	jobID string // empty = receive all events
	send  chan []byte
}

// Hub manages SSE clients, persists events to JSONL files, and fans out events.
type Hub struct {
	mu        sync.RWMutex
	clients   map[*sseClient]struct{}
	broadcast chan Event
	seq       uint64
	dataDir   string
	jobFiles  map[string]*os.File
}

// NewHub creates a Hub that persists events under dataDir and starts the run goroutine.
func NewHub(dataDir string) *Hub {
	if err := os.MkdirAll(dataDir, 0o755); err != nil {
		log.Printf("hub: failed to create data dir %s: %v", dataDir, err)
	}
	h := &Hub{
		clients:   make(map[*sseClient]struct{}),
		broadcast: make(chan Event, 4096),
		dataDir:   dataDir,
		jobFiles:  make(map[string]*os.File),
	}
	go h.run()
	return h
}

// Emit enqueues an event for the given job. No-ops if jobID is empty or hub is nil.
func (h *Hub) Emit(jobID string, t EventType, data map[string]any) {
	if h == nil || jobID == "" {
		return
	}
	id := atomic.AddUint64(&h.seq, 1)
	e := Event{
		ID:        fmt.Sprintf("%d", id),
		JobID:     jobID,
		Type:      t,
		Timestamp: time.Now(),
		Data:      data,
	}
	select {
	case h.broadcast <- e:
	default:
		log.Printf("hub: broadcast channel full, dropping %s for job %s", t, jobID)
	}
}

// run processes the broadcast channel — single goroutine owns jobFiles.
func (h *Hub) run() {
	for e := range h.broadcast {
		// Persist to JSONL file.
		if f, err := h.openJobFile(e.JobID); err != nil {
			log.Printf("hub: open file for job %s: %v", e.JobID, err)
		} else {
			line, _ := json.Marshal(e)
			f.Write(append(line, '\n'))
		}

		// Marshal once, fan out to matching clients.
		data, err := json.Marshal(e)
		if err != nil {
			log.Printf("hub: marshal event: %v", err)
			continue
		}
		h.mu.RLock()
		for c := range h.clients {
			if c.jobID == "" || c.jobID == e.JobID {
				select {
				case c.send <- data:
				default:
					// Client too slow, drop.
				}
			}
		}
		h.mu.RUnlock()
	}
}

func (h *Hub) openJobFile(jobID string) (*os.File, error) {
	if f, ok := h.jobFiles[jobID]; ok {
		return f, nil
	}
	path := filepath.Join(h.dataDir, jobID+".jsonl")
	f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return nil, err
	}
	h.jobFiles[jobID] = f
	return f, nil
}

func (h *Hub) add(c *sseClient) {
	h.mu.Lock()
	h.clients[c] = struct{}{}
	h.mu.Unlock()
}

func (h *Hub) remove(c *sseClient) {
	h.mu.Lock()
	if _, ok := h.clients[c]; ok {
		delete(h.clients, c)
		close(c.send)
	}
	h.mu.Unlock()
}

// ServeSSE handles GET /events?job={id} — streams live events to the browser.
func (h *Hub) ServeSSE(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming not supported", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")

	c := &sseClient{
		jobID: r.URL.Query().Get("job"),
		send:  make(chan []byte, 64),
	}
	h.add(c)
	defer h.remove(c)

	for {
		select {
		case msg, ok := <-c.send:
			if !ok {
				return
			}
			fmt.Fprintf(w, "data: %s\n\n", msg)
			flusher.Flush()
		case <-r.Context().Done():
			return
		}
	}
}

// ServeJobAPI handles GET /api/jobs/{id} — returns the full event history as JSON.
func (h *Hub) ServeJobAPI(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimPrefix(r.URL.Path, "/api/jobs/")
	if id == "" {
		http.Error(w, "missing job id", http.StatusBadRequest)
		return
	}

	path := filepath.Join(h.dataDir, id+".jsonl")
	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			http.Error(w, "job not found", http.StatusNotFound)
		} else {
			http.Error(w, "internal error", http.StatusInternalServerError)
		}
		return
	}
	defer f.Close()

	var events []Event
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 1024*1024), 1024*1024)
	for scanner.Scan() {
		var e Event
		if err := json.Unmarshal(scanner.Bytes(), &e); err != nil {
			continue
		}
		events = append(events, e)
	}
	if events == nil {
		events = []Event{}
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(events)
}

type jobSummary struct {
	ID        string    `json:"id"`
	Task      string    `json:"task"`
	StartedAt time.Time `json:"started_at"`
	Status    string    `json:"status"`
	CostUSD   float64   `json:"cost_usd"`
}

// ServeJobList handles GET /api/jobs — returns a summary of all known jobs.
func (h *Hub) ServeJobList(w http.ResponseWriter, r *http.Request) {
	entries, err := os.ReadDir(h.dataDir)
	if err != nil {
		if os.IsNotExist(err) {
			w.Header().Set("Content-Type", "application/json")
			w.Write([]byte("[]"))
			return
		}
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	var jobs []jobSummary
	for _, entry := range entries {
		if !strings.HasSuffix(entry.Name(), ".jsonl") {
			continue
		}
		id := strings.TrimSuffix(entry.Name(), ".jsonl")
		summary := jobSummary{ID: id, Status: "running"}

		path := filepath.Join(h.dataDir, entry.Name())
		f, err := os.Open(path)
		if err != nil {
			continue
		}

		scanner := bufio.NewScanner(f)
		scanner.Buffer(make([]byte, 1024*1024), 1024*1024)
		var cost float64
		first := true
		for scanner.Scan() {
			var e Event
			if err := json.Unmarshal(scanner.Bytes(), &e); err != nil {
				continue
			}
			if first {
				if task, ok := e.Data["task"].(string); ok {
					summary.Task = task
				}
				summary.StartedAt = e.Timestamp
				first = false
			}
			switch e.Type {
			case EventLLMResponse:
				if v, ok := e.Data["cost_usd"].(float64); ok {
					cost += v
				}
			case EventJobCompleted:
				summary.Status = "completed"
				if v, ok := e.Data["total_cost_usd"].(float64); ok {
					cost = v // authoritative total
				}
			case EventJobError:
				summary.Status = "error"
				if v, ok := e.Data["total_cost_usd"].(float64); ok {
					cost = v
				}
			}
		}
		f.Close()
		summary.CostUSD = cost
		jobs = append(jobs, summary)
	}

	// Sort by started_at descending (most recent first).
	for i := 1; i < len(jobs); i++ {
		for j := i; j > 0 && jobs[j].StartedAt.After(jobs[j-1].StartedAt); j-- {
			jobs[j], jobs[j-1] = jobs[j-1], jobs[j]
		}
	}

	if jobs == nil {
		jobs = []jobSummary{}
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(jobs)
}

type statsResponse struct {
	TotalJobs             int     `json:"total_jobs"`
	CompletedJobs         int     `json:"completed_jobs"`
	ErrorJobs             int     `json:"error_jobs"`
	RunningJobs           int     `json:"running_jobs"`
	TotalCostUSD          float64 `json:"total_cost_usd"`
	TotalInputTokens      int64   `json:"total_input_tokens"`
	TotalOutputTokens     int64   `json:"total_output_tokens"`
	TotalCacheReadTokens  int64   `json:"total_cache_read_tokens"`
	TotalCacheWriteTokens int64   `json:"total_cache_write_tokens"`
}

// ServeStats handles GET /api/stats — returns aggregate cost and token stats.
func (h *Hub) ServeStats(w http.ResponseWriter, r *http.Request) {
	entries, err := os.ReadDir(h.dataDir)
	if err != nil {
		if os.IsNotExist(err) {
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(statsResponse{})
			return
		}
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	var stats statsResponse
	for _, entry := range entries {
		if !strings.HasSuffix(entry.Name(), ".jsonl") {
			continue
		}
		stats.TotalJobs++

		path := filepath.Join(h.dataDir, entry.Name())
		f, err := os.Open(path)
		if err != nil {
			continue
		}

		scanner := bufio.NewScanner(f)
		scanner.Buffer(make([]byte, 1024*1024), 1024*1024)
		status := "running"
		for scanner.Scan() {
			var e Event
			if err := json.Unmarshal(scanner.Bytes(), &e); err != nil {
				continue
			}
			switch e.Type {
			case EventLLMResponse:
				if v, ok := e.Data["input_tokens"].(float64); ok {
					stats.TotalInputTokens += int64(v)
				}
				if v, ok := e.Data["output_tokens"].(float64); ok {
					stats.TotalOutputTokens += int64(v)
				}
				if v, ok := e.Data["cache_read_tokens"].(float64); ok {
					stats.TotalCacheReadTokens += int64(v)
				}
				if v, ok := e.Data["cache_write_tokens"].(float64); ok {
					stats.TotalCacheWriteTokens += int64(v)
				}
				if v, ok := e.Data["cost_usd"].(float64); ok {
					stats.TotalCostUSD += v
				}
			case EventJobCompleted:
				status = "completed"
			case EventJobError:
				status = "error"
			}
		}
		f.Close()

		switch status {
		case "completed":
			stats.CompletedJobs++
		case "error":
			stats.ErrorJobs++
		default:
			stats.RunningJobs++
		}
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(stats)
}

// serveUI returns the single-page monitoring app for all UI routes.
func serveUI(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Write([]byte(monitorHTML))
}

// streamingWriter pipes command output to hub events and buffers the full output.
type streamingWriter struct {
	hub   *Hub
	jobID string
	buf   *bytes.Buffer
	line  []byte
}

func newStreamingWriter(hub *Hub, jobID string) *streamingWriter {
	return &streamingWriter{
		hub:   hub,
		jobID: jobID,
		buf:   &bytes.Buffer{},
	}
}

func (w *streamingWriter) Write(p []byte) (int, error) {
	w.buf.Write(p)
	for _, b := range p {
		if b == '\n' {
			if w.hub != nil && w.jobID != "" {
				w.hub.Emit(w.jobID, EventClaudeCodeLine, map[string]any{
					"text": string(w.line),
				})
			}
			w.line = w.line[:0]
		} else {
			w.line = append(w.line, b)
		}
	}
	return len(p), nil
}

// generateJobID returns a new UUID v4 string.
func generateJobID() string {
	return uuid.New().String()
}

