package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"video_downloader/api"
	"video_downloader/downloader"
	"video_downloader/task"
	"video_downloader/utils"
)

const (
	DefaultWorkers     = 16
	DefaultRetries     = 3
	MaxConcurrentTasks = 3
	DownloadDir        = "downloads"
)

func main() {
	port := flag.Int("port", 8080, "Port to listen on")
	workers := flag.Int("workers", DefaultWorkers, "Concurrent download workers per task")
	retries := flag.Int("retries", DefaultRetries, "Number of retries per segment")
	cookieInput := flag.String("cookies", "", "Cookie file (Netscape) or raw cookie string")
	proxyStr := flag.String("proxy", "", "HTTP/SOCKS proxy URL")
	flag.Parse()

	// Locate FFmpeg
	ffmpegPath, err := utils.EnsureFFmpeg()
	if err != nil {
		log.Fatalf("FFmpeg error: %v", err)
	}

	// Create core dependencies (no global variables)
	dl := downloader.NewDownloader(*workers, *retries, *cookieInput, *proxyStr, ffmpegPath)
	tm := task.NewManager(MaxConcurrentTasks, dl, DownloadDir)

	// Register API routes
	mux := http.NewServeMux()
	srv := api.NewServer(tm, dl)
	srv.RegisterRoutes(mux)
	
	// Serve downloaded files statically
	fileServer := http.FileServer(http.Dir(DownloadDir))
	mux.Handle("/downloads/", http.StripPrefix("/downloads/", fileServer))

	// Apply middleware chain: MaxBodySize → CORS → routes
	handler := api.MaxBodySize(api.MaxRequestBodySize, api.CORSMiddleware(mux))

	httpSrv := &http.Server{
		Addr:         fmt.Sprintf(":%d", *port),
		Handler:      handler,
		ReadTimeout:  30 * time.Second,
		WriteTimeout: 0, // streaming downloads can be long
		IdleTimeout:  120 * time.Second,
	}

	// Graceful shutdown
	go func() {
		sigChan := make(chan os.Signal, 1)
		signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)
		<-sigChan

		log.Println("Shutting down gracefully...")

		// 1. Stop accepting new HTTP requests
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		httpSrv.Shutdown(shutdownCtx)

		// 2. Cancel all active tasks and wait for workers
		tm.Shutdown()

		// 3. Cleanup temp folders
		matches, _ := filepath.Glob(".tmp_*")
		for _, m := range matches {
			os.RemoveAll(m)
		}

		log.Println("Shutdown complete.")
	}()

	fmt.Printf("┌──────────────────────────────────────────┐\n")
	fmt.Printf("│  Video Downloader API v2.0.0             │\n")
	fmt.Printf("│  Listening on :%d                       │\n", *port)
	fmt.Printf("│  Workers: %d  │  Max Tasks: %d           │\n", *workers, MaxConcurrentTasks)
	fmt.Printf("│  Docs: http://localhost:%d/api/v1/docs   │\n", *port)
	fmt.Printf("└──────────────────────────────────────────┘\n")

	if err := httpSrv.ListenAndServe(); err != http.ErrServerClosed {
		log.Fatalf("Server error: %v", err)
	}
}
