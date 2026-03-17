package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"video_downloader/downloader"
	"video_downloader/task"

	"github.com/google/uuid"
)

const MaxRequestBodySize int64 = 1 * 1024 * 1024 // 1 MB

// Server holds API dependencies.
type Server struct {
	TaskMgr *task.Manager
	DL      *downloader.Downloader
}

// NewServer creates a new API server.
func NewServer(tm *task.Manager, dl *downloader.Downloader) *Server {
	return &Server{TaskMgr: tm, DL: dl}
}

// RegisterRoutes registers all API routes on the given mux.
func (s *Server) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("/healthz", s.HandleHealthz())
	mux.HandleFunc("/api/v1/download", s.HandleDownload())
	mux.HandleFunc("/api/v1/status/", s.HandleStatus())
	mux.HandleFunc("/api/v1/cancel/", s.HandleCancel())
	mux.HandleFunc("/api/v1/tasks", s.HandleListTasks())
	mux.HandleFunc("/api/v1/docs", s.HandleDocs())
	mux.HandleFunc("/api/v1/openapi.json", s.HandleOpenAPI())
}

// ── Handlers ────────────────────────────────────────────────────────────

func (s *Server) HandleHealthz() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		SendJSON(w, http.StatusOK, APIResponse{
			Success: true,
			Data:    map[string]string{"status": "ok"},
		})
	}
}


func (s *Server) HandleDownload() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			SendJSON(w, http.StatusMethodNotAllowed, APIResponse{Message: "Method not allowed"})
			return
		}

		var req struct {
			URL     string `json:"url"`
			Quality int    `json:"quality"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			SendJSON(w, http.StatusBadRequest, APIResponse{Message: "Invalid request body"})
			return
		}

		if err := validateURL(req.URL); err != nil {
			SendJSON(w, http.StatusBadRequest, APIResponse{Message: err.Error()})
			return
		}

		ctx, cancel := context.WithCancel(context.Background())
		taskID := uuid.New().String()
		t := &task.DownloadTask{
			ID:        taskID,
			URL:       req.URL,
			Status:    task.StatusPending,
			Quality:   req.Quality,
			CreatedAt: time.Now(),
			Ctx:       ctx,
			Cancel:    cancel,
		}

		if err := s.TaskMgr.Submit(t); err != nil {
			cancel()
			SendJSON(w, http.StatusServiceUnavailable, APIResponse{
				Message: "Server is busy: " + err.Error(),
			})
			return
		}

		SendJSON(w, http.StatusAccepted, APIResponse{
			Success: true,
			Data:    map[string]string{"task_id": taskID},
		})
	}
}

func (s *Server) HandleStatus() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := strings.TrimPrefix(r.URL.Path, "/api/v1/status/")
		if id == "" {
			SendJSON(w, http.StatusBadRequest, APIResponse{Message: "Missing task ID"})
			return
		}

		t, ok := s.TaskMgr.Get(id)
		if !ok {
			SendJSON(w, http.StatusNotFound, APIResponse{Message: "Task not found"})
			return
		}

		SendJSON(w, http.StatusOK, APIResponse{
			Success: true,
			Data:    t,
		})
	}
}

func (s *Server) HandleCancel() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			SendJSON(w, http.StatusMethodNotAllowed, APIResponse{Message: "Method not allowed"})
			return
		}

		id := strings.TrimPrefix(r.URL.Path, "/api/v1/cancel/")
		if s.TaskMgr.Cancel(id) {
			SendJSON(w, http.StatusOK, APIResponse{Success: true, Message: "Task canceled"})
		} else {
			SendJSON(w, http.StatusNotFound, APIResponse{Message: "Task not found or already finished"})
		}
	}
}

func (s *Server) HandleListTasks() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			SendJSON(w, http.StatusMethodNotAllowed, APIResponse{Message: "Method not allowed"})
			return
		}
		tasks := s.TaskMgr.ListTasks()
		SendJSON(w, http.StatusOK, APIResponse{
			Success: true,
			Data:    tasks,
		})
	}
}

func (s *Server) HandleDocs() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		fmt.Fprint(w, `
<!DOCTYPE html>
<html lang="en">
<head>
    <meta charset="utf-8" />
    <meta name="viewport" content="width=device-width, initial-scale=1" />
    <title>Video Downloader API - Swagger UI</title>
    <link rel="stylesheet" href="https://unpkg.com/swagger-ui-dist@5/swagger-ui.css" />
    <style>
        html { box-sizing: border-box; overflow-y: scroll; }
        *, *:before, *:after { box-sizing: inherit; }
        body { margin: 0; background: #fafafa; }
    </style>
</head>
<body>
    <div id="swagger-ui"></div>
    <script src="https://unpkg.com/swagger-ui-dist@5/swagger-ui-bundle.js"></script>
    <script>
        window.onload = () => {
            window.ui = SwaggerUIBundle({
                url: '/api/v1/openapi.json',
                dom_id: '#swagger-ui',
                deepLinking: true,
                presets: [SwaggerUIBundle.presets.apis],
                layout: "BaseLayout"
            });
        };
    </script>
</body>
</html>
`)
	}
}

func (s *Server) HandleOpenAPI() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		spec := `{
  "openapi": "3.0.0",
  "info": {
    "title": "Go Video Downloader API",
    "description": "High-performance enterprise-grade video downloader service supporting HLS and direct MP4.",
    "version": "2.0.0"
  },
  "paths": {
    "/healthz": {
      "get": {
        "summary": "Health Check",
        "responses": {
          "200": {
            "description": "Service is healthy",
            "content": {
              "application/json": {
                "schema": {
                  "type": "object",
                  "properties": {
                    "success": { "type": "boolean" },
                    "data": {
                      "type": "object",
                      "properties": {
                        "status": { "type": "string", "example": "ok" }
                      }
                    }
                  }
                }
              }
            }
          }
        }
      }
    },
    "/api/v1/download": {
      "post": {
        "summary": "Start Download",
        "description": "Trigger an asynchronous download task.",
        "requestBody": {
          "required": true,
          "content": {
            "application/json": {
              "schema": {
                "type": "object",
                "properties": {
                  "url": { "type": "string" },
                  "quality": { "type": "integer", "example": 1080 }
                },
                "required": ["url"]
              }
            }
          }
        },
        "responses": {
          "202": { "description": "Task accepted" },
          "400": { "description": "Invalid request" },
          "503": { "description": "Server busy, queue full" }
        }
      }
    },
    "/api/v1/status/{id}": {
      "get": {
        "summary": "Check Task Status",
        "parameters": [
          { "name": "id", "in": "path", "required": true, "schema": { "type": "string" } }
        ],
        "responses": {
          "200": {
            "description": "Success",
            "content": {
              "application/json": {
                "schema": {
                  "type": "object",
                  "properties": {
                    "success": { "type": "boolean" },
                    "data": {
                      "type": "object",
                      "properties": {
                        "task_id": { "type": "string" },
                        "url": { "type": "string" },
                        "status": { "type": "string", "enum": ["pending", "running", "completed", "failed", "canceled"] },
                        "progress": { "type": "number" },
                        "file_path": { "type": "string" },
                        "error": { "type": "string" },
                        "title": { "type": "string" },
                        "quality": { "type": "integer" },
                        "created_at": { "type": "string", "format": "date-time" }
                      }
                    }
                  }
                }
              }
            }
          },
          "404": { "description": "Task not found" }
        }
      }
    },
    "/api/v1/cancel/{id}": {
      "post": {
        "summary": "Cancel Task",
        "parameters": [
          { "name": "id", "in": "path", "required": true, "schema": { "type": "string" } }
        ],
        "responses": {
          "200": { "description": "Task canceled" },
          "404": { "description": "Task not found or already finished" }
        }
      }
    },
    "/api/v1/tasks": {
      "get": {
        "summary": "List All Tasks",
        "responses": {
          "200": {
            "description": "Success",
            "content": {
              "application/json": {
                "schema": {
                  "type": "object",
                  "properties": {
                    "success": { "type": "boolean" },
                    "data": {
                      "type": "array",
                      "items": { "$ref": "#/paths/~1api~1v1~1status~1%7Bid%7D/get/responses/200/content/application~1json/schema/properties/data" }
                    }
                  }
                }
              }
            }
          }
        }
      }
    }
  }
}`
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(spec))
	}
}

// ── Validators ──────────────────────────────────────────────────────────

func validateURL(rawURL string) error {
	if rawURL == "" {
		return fmt.Errorf("url is required")
	}
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return fmt.Errorf("invalid url: %w", err)
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return fmt.Errorf("url must use http or https scheme")
	}
	if parsed.Host == "" {
		return fmt.Errorf("url must have a valid host")
	}
	return nil
}
