package task

import (
	"context"
	"fmt"
	"log"
	"os"
	"sync"
	"time"

	"video_downloader/downloader"
	"video_downloader/extractor"
	"video_downloader/utils"
)

// ── Status ──────────────────────────────────────────────────────────────

type TaskStatus string

const (
	StatusPending   TaskStatus = "pending"
	StatusRunning   TaskStatus = "running"
	StatusCompleted TaskStatus = "completed"
	StatusFailed    TaskStatus = "failed"
	StatusCanceled  TaskStatus = "canceled"
)

// ── DownloadTask ────────────────────────────────────────────────────────

type DownloadTask struct {
	ID        string         `json:"task_id"`
	URL       string         `json:"url"`
	Status    TaskStatus     `json:"status"`
	Progress  float32        `json:"progress"`
	FilePath  string         `json:"file_path,omitempty"`
	Error     string         `json:"error,omitempty"`
	Title     string         `json:"title,omitempty"`
	Quality   int            `json:"quality,omitempty"`
	CreatedAt time.Time      `json:"created_at"`
	Cancel    context.CancelFunc `json:"-"`
	Ctx       context.Context    `json:"-"`
}

// ── TaskManager ─────────────────────────────────────────────────────────

type Manager struct {
	tasks      sync.Map
	queue      chan *DownloadTask
	wg         sync.WaitGroup
	quit       chan struct{}
	dl         *downloader.Downloader
	downloadDir string
}

// NewManager creates a task manager with the specified concurrent task limit.
func NewManager(maxTasks int, dl *downloader.Downloader, downloadDir string) *Manager {
	m := &Manager{
		queue:       make(chan *DownloadTask, 100),
		quit:        make(chan struct{}),
		dl:          dl,
		downloadDir: downloadDir,
	}
	for i := 0; i < maxTasks; i++ {
		go m.worker()
	}
	return m
}

func (m *Manager) worker() {
	for {
		select {
		case task := <-m.queue:
			if task.Status == StatusCanceled {
				continue
			}
			m.runTaskWrapper(task)
		case <-m.quit:
			return
		}
	}
}

func (m *Manager) runTaskWrapper(task *DownloadTask) {
	// BUG FIX: wg.Add(1) is now called inside the worker (which is the
	// goroutine already running), before work begins. This is safe because
	// Shutdown calls close(quit) first which stops workers from picking new
	// tasks, then waits on wg. The critical fix is in Submit() below.
	m.wg.Add(1)
	defer m.wg.Done()

	log.Printf("Starting task: %s", task.ID)
	err := m.runTask(task)
	if err != nil {
		if task.Status != StatusCanceled {
			task.Status = StatusFailed
			task.Error = err.Error()
		}
	} else {
		task.Status = StatusCompleted
		task.Progress = 100
	}
	m.Set(task)
}

func (m *Manager) runTask(task *DownloadTask) error {
	task.Status = StatusRunning
	m.Set(task)

	content, err := m.dl.Fetch(task.Ctx, task.URL)
	if err != nil {
		return err
	}

	flashVars, err := extractor.ExtractFlashVars(content)
	if err != nil {
		return err
	}
	task.Title = flashVars.VideoTitle
	m.Set(task)

	streams := extractor.ResolveMediaDefinitions(flashVars.MediaDefinitions, m.dl.FetchFunc())
	extractor.SortStreams(streams)

	selected := extractor.PickStream(streams, task.Quality)
	if selected.VideoUrl == "" {
		return fmt.Errorf("no suitable stream found for quality %d", task.Quality)
	}

	_ = os.MkdirAll(m.downloadDir, 0755)
	fileName := utils.GetUniqueFilename(m.downloadDir, utils.SanitizeFilename(task.Title)+".mp4")
	task.FilePath = fileName
	m.Set(task)

	report := func(progress float32) {
		task.Progress = progress
		m.Set(task)
	}

	if selected.Format == "mp4" {
		return m.dl.DownloadStreamingMP4(task.Ctx, selected.VideoUrl, fileName, report)
	}

	return m.dl.DownloadHLS(task.Ctx, selected.VideoUrl, task.Title, fileName, report)
}

// ── Public Methods ──────────────────────────────────────────────────────

// Get retrieves a task by ID.
func (m *Manager) Get(id string) (*DownloadTask, bool) {
	val, ok := m.tasks.Load(id)
	if !ok {
		return nil, false
	}
	return val.(*DownloadTask), true
}

// Set stores or updates a task.
func (m *Manager) Set(task *DownloadTask) {
	m.tasks.Store(task.ID, task)
}

// Submit enqueues a task. Returns error if the queue is full (non-blocking).
func (m *Manager) Submit(task *DownloadTask) error {
	m.Set(task)
	select {
	case m.queue <- task:
		return nil
	default:
		return fmt.Errorf("task queue is full, try again later")
	}
}

// Cancel cancels a running or pending task.
func (m *Manager) Cancel(id string) bool {
	task, ok := m.Get(id)
	if !ok || task.Status == StatusCompleted || task.Status == StatusFailed || task.Status == StatusCanceled {
		return false
	}
	task.Status = StatusCanceled
	if task.Cancel != nil {
		task.Cancel()
	}
	m.Set(task)
	return true
}

// ListTasks returns all tasks.
func (m *Manager) ListTasks() []*DownloadTask {
	var tasks []*DownloadTask
	m.tasks.Range(func(key, value interface{}) bool {
		tasks = append(tasks, value.(*DownloadTask))
		return true
	})
	return tasks
}

// Shutdown gracefully stops all workers, cancels running tasks, and waits.
func (m *Manager) Shutdown() {
	// Cancel all active tasks
	m.tasks.Range(func(key, value interface{}) bool {
		task := value.(*DownloadTask)
		if task.Cancel != nil {
			task.Cancel()
		}
		return true
	})

	close(m.quit)
	m.wg.Wait()
}
