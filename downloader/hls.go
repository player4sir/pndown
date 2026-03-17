package downloader

import (
	"context"
	"crypto/md5"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"video_downloader/utils"
)

const (
	RetryBaseDelay = 1 * time.Second
)

// ProgressReporter is called with progress values (0..100).
type ProgressReporter func(progress float32)

// MaxSegmentFailRate is the maximum fraction of segments that can fail
// before the entire download is considered failed. 0.2 = allow up to 20%.
const MaxSegmentFailRate = 0.20

// DownloadHLS downloads an HLS stream: fetches segments concurrently,
// then merges them with ffmpeg.
//
// Tolerant approach: individual segment 404s/failures are logged and
// skipped. The download only fails if more than MaxSegmentFailRate of
// segments fail, or if the context is canceled.
func (d *Downloader) DownloadHLS(ctx context.Context, m3u8URL, title, outputPath string, report ProgressReporter) error {
	segments, err := d.resolveSegments(ctx, m3u8URL)
	if err != nil {
		return fmt.Errorf("resolve segments: %w", err)
	}
	if len(segments) == 0 {
		return fmt.Errorf("no segments found in playlist")
	}

	hash := fmt.Sprintf("%x", md5.Sum([]byte(m3u8URL)))[:8]
	tempDir := filepath.Join(".", ".tmp_"+hash)
	os.MkdirAll(tempDir, 0755)
	defer os.RemoveAll(tempDir)

	files := make([]string, len(segments))
	total := int64(len(segments))

	// Track progress and failures atomically
	var completed int64
	var failed int64
	// Record which segments succeeded for the concat list
	success := make([]bool, len(segments))

	var wg sync.WaitGroup
	sem := make(chan struct{}, d.Workers)

	for i, segURL := range segments {
		if ctx.Err() != nil {
			break
		}
		idx := i
		target := segURL
		files[idx] = filepath.Join(tempDir, fmt.Sprintf("%05d.ts", idx))

		wg.Add(1)
		go func() {
			defer wg.Done()
			// Acquire semaphore
			select {
			case sem <- struct{}{}:
			case <-ctx.Done():
				atomic.AddInt64(&failed, 1)
				return
			}
			defer func() { <-sem }()

			var lastErr error
			for attempt := 0; attempt <= d.Retries; attempt++ {
				data, fetchErr := d.Fetch(ctx, target)
				if fetchErr == nil {
					if wErr := os.WriteFile(files[idx], data, 0644); wErr != nil {
						log.Printf("[WARN] segment %d: write error: %v", idx, wErr)
						atomic.AddInt64(&failed, 1)
						return
					}
					success[idx] = true
					done := atomic.AddInt64(&completed, 1)
					if report != nil {
						report(float32(done+atomic.LoadInt64(&failed)) / float32(total) * 90.0)
					}
					return
				}
				lastErr = fetchErr
				// If context canceled, stop immediately
				if ctx.Err() != nil {
					atomic.AddInt64(&failed, 1)
					return
				}
				select {
				case <-time.After(RetryBaseDelay):
				case <-ctx.Done():
					atomic.AddInt64(&failed, 1)
					return
				}
			}
			// All retries exhausted — record as failed but don't kill others
			log.Printf("[WARN] segment %d failed after %d retries: %v (skipping)", idx, d.Retries+1, lastErr)
			f := atomic.AddInt64(&failed, 1)
			if report != nil {
				report(float32(atomic.LoadInt64(&completed)+f) / float32(total) * 90.0)
			}
		}()
	}
	wg.Wait()

	if ctx.Err() != nil {
		return ctx.Err()
	}

	// Check failure rate
	failCount := atomic.LoadInt64(&failed)
	if failCount > 0 {
		failRate := float64(failCount) / float64(total)
		log.Printf("[INFO] %d/%d segments failed (%.1f%%)", failCount, total, failRate*100)
		if failRate > MaxSegmentFailRate {
			return fmt.Errorf("%d/%d segments failed (%.0f%% > %.0f%% threshold)",
				failCount, total, failRate*100, MaxSegmentFailRate*100)
		}
	}

	// Merge with ffmpeg — only include successful segments
	filelistPath := filepath.Join(tempDir, "filelist.txt")
	f, err := os.Create(filelistPath)
	if err != nil {
		return fmt.Errorf("create filelist: %w", err)
	}
	for idx, file := range files {
		if !success[idx] {
			continue // skip failed segments
		}
		abs, _ := filepath.Abs(file)
		fmt.Fprintf(f, "file '%s'\n", abs)
	}
	f.Close()

	if report != nil {
		report(92.0)
	}

	args := []string{
		"-f", "concat", "-safe", "0", "-i", filelistPath,
		"-metadata", "title=" + title,
		"-c", "copy", "-bsf:a", "aac_adtstoasc", "-movflags", "+faststart", "-y", outputPath,
	}

	cmd := exec.CommandContext(ctx, d.FFmpegPath, args...)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("ffmpeg failed: %s", string(out))
	}

	if report != nil {
		report(100.0)
	}
	if failCount > 0 {
		log.Printf("[INFO] Download completed with %d skipped segments", failCount)
	}
	return nil
}

// resolveSegments recursively resolves an M3U8 playlist to a list of
// segment URLs. For master playlists, it picks the highest bandwidth variant.
func (d *Downloader) resolveSegments(ctx context.Context, m3u8URL string) ([]string, error) {
	data, err := d.Fetch(ctx, m3u8URL)
	if err != nil {
		return nil, err
	}
	content := string(data)
	u, _ := url.Parse(m3u8URL)

	log.Printf("[DEBUG] resolveSegments URL: %s", m3u8URL)
	log.Printf("[DEBUG] M3U8 content (first 500 chars): %.500s", content)

	// Master playlist → select highest bandwidth variant
	if strings.Contains(content, "#EXT-X-STREAM-INF") {
		bestVariant := selectBestVariant(content)
		if bestVariant == "" {
			return nil, fmt.Errorf("no variant found in master playlist")
		}
		variantURL := u.ResolveReference(utils.ParseURL(bestVariant)).String()
		log.Printf("[DEBUG] Master playlist detected, selected variant: %s → %s", bestVariant, variantURL)
		return d.resolveSegments(ctx, variantURL)
	}

	// Media playlist → collect segment URLs
	lines := strings.Split(content, "\n")
	var segments []string
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line != "" && !strings.HasPrefix(line, "#") {
			segments = append(segments, u.ResolveReference(utils.ParseURL(line)).String())
		}
	}
	if len(segments) > 0 {
		log.Printf("[DEBUG] Media playlist: %d segments, first: %s", len(segments), segments[0])
	}
	return segments, nil
}

// selectBestVariant parses #EXT-X-STREAM-INF tags and returns the URI
// of the variant with the highest BANDWIDTH.
func selectBestVariant(content string) string {
	lines := strings.Split(content, "\n")
	bestBandwidth := -1
	bestURI := ""

	re := regexp.MustCompile(`BANDWIDTH=(\d+)`)

	for i, line := range lines {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "#EXT-X-STREAM-INF") {
			continue
		}
		matches := re.FindStringSubmatch(line)
		if matches == nil {
			continue
		}
		bw, err := strconv.Atoi(matches[1])
		if err != nil {
			continue
		}
		// Next non-empty, non-comment line is the URI
		for j := i + 1; j < len(lines); j++ {
			uri := strings.TrimSpace(lines[j])
			if uri != "" && !strings.HasPrefix(uri, "#") {
				if bw > bestBandwidth {
					bestBandwidth = bw
					bestURI = uri
				}
				break
			}
		}
	}
	return bestURI
}

// DownloadStreamingMP4 downloads a direct MP4 file with progress tracking.
// Progress is reported in the 0..100 range.
func (d *Downloader) DownloadStreamingMP4(ctx context.Context, uri, path string, report ProgressReporter) error {
	req, err := newGetRequest(ctx, uri)
	if err != nil {
		return err
	}
	resp, err := d.Client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		return fmt.Errorf("HTTP %s", resp.Status)
	}

	size := resp.ContentLength
	out, err := os.Create(path)
	if err != nil {
		return err
	}
	defer out.Close()

	buffer := make([]byte, 32*1024)
	var downloaded int64
	for {
		n, readErr := resp.Body.Read(buffer)
		if n > 0 {
			_, writeErr := out.Write(buffer[:n])
			if writeErr != nil {
				return writeErr
			}
			downloaded += int64(n)
			if size > 0 && report != nil {
				report(float32(downloaded) / float32(size) * 100.0)
			}
		}
		if readErr == io.EOF {
			break
		}
		if readErr != nil {
			return readErr
		}
	}
	if report != nil {
		report(100.0)
	}
	return nil
}

func newGetRequest(ctx context.Context, uri string) (*http.Request, error) {
	req, err := http.NewRequestWithContext(ctx, "GET", uri, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/124.0.0.0 Safari/537.36")
	req.Header.Set("Referer", "https://www.pornhub.com/")
	return req, nil
}
