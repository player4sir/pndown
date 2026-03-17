# Video Downloader API - Project Context

## Project Overview
This project is a high-performance enterprise-grade video downloader service supporting HLS and direct MP4 downloads. It is written in Go and exposes an asynchronous HTTP REST API for submitting, monitoring, and cancelling download tasks. 

**Main Technologies:**
- **Language:** Go 1.21+
- **External Dependencies:** `github.com/google/uuid` for task IDs, `ffmpeg` for video processing and muxing.
- **Architecture:** 
  - **API Layer (`api/`):** HTTP server (using standard library `net/http.ServeMux`), handles request validation, routing, and serves OpenAPI/Swagger documentation.
  - **Task Management (`task/`):** Manages concurrent download tasks, queuing, and state tracking.
  - **Downloader (`downloader/`):** HTTP client logic handling proxies, retries, cookies, and size-limited response bodies.
  - **Extractor (`extractor/`):** Extracts video metadata and download links.

## Building and Running

**Prerequisites:**
- Go 1.21 or higher.
- `ffmpeg.exe` must be present in the directory or system PATH.

**Commands:**
- **Run locally:**
  ```bash
  go run .
  ```
  Optional flags:
  ```bash
  go run . --port 8080 --workers 16 --retries 3 --cookies "cookie.txt" --proxy "http://proxy:8080"
  ```
- **Build executable:**
  ```bash
  go build -o main.exe .
  ```

**API Endpoints:**
The server listens on `http://localhost:8080` by default.
- `GET /api/v1/docs` - Swagger UI Documentation.
- `POST /api/v1/download` - Submit a download task (JSON: `{"url": "...", "quality": 1080}`).
- `GET /api/v1/status/{id}` - Check task status and progress.
- `POST /api/v1/cancel/{id}` - Cancel an active download task.
- `GET /api/v1/tasks` - List all tasks.

## Docker Deployment

The application includes a `Dockerfile` and `docker-compose.yml` for easy deployment. The Docker image is based on Alpine Linux and has `ffmpeg` pre-installed.

**Commands:**
- **Build and start with Docker Compose:**
  ```bash
  docker-compose up -d --build
  ```
- **Stop and remove containers:**
  ```bash
  docker-compose down
  ```
- **View logs:**
  ```bash
  docker-compose logs -f
  ```

Downloaded files will be mapped to the `downloads/` directory on the host machine. You can modify startup flags (like workers or retries) in `docker-compose.yml`.

## Development Conventions
- **Standard Go Layout:** Code is organized into functional packages (`api`, `downloader`, `task`, `utils`, `extractor`).
- **Concurrency & Context:** Heavy use of `context.Context` for managing graceful shutdowns, request timeouts, and cancelling concurrent worker goroutines.
- **Dependency Injection:** No global state is used for core dependencies. Components like `Manager` (task) and `Downloader` are injected into the `Server` struct.
- **API Responses:** A consistent JSON response structure (`APIResponse`) is returned for all endpoints.
- **Graceful Shutdown:** Intercepts `SIGINT`/`SIGTERM` to stop the HTTP server, cancel active background tasks, and clean up `.tmp_*` directories before exiting.