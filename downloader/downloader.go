package downloader

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"os"
	"strings"
	"time"

	"video_downloader/utils"
)

const (
	DefaultMaxResponseSize = 256 * 1024 * 1024 // 256 MB
)

// Downloader manages HTTP fetching with cookies, proxy, and retry logic.
type Downloader struct {
	Client          *http.Client
	Workers         int
	Retries         int
	FFmpegPath      string
	MaxResponseSize int64
}

// NewDownloader creates a Downloader with cookie and proxy support.
func NewDownloader(workers, retries int, cookieInput, proxyStr, ffmpegPath string) *Downloader {
	jar, _ := cookiejar.New(nil)

	transport := &http.Transport{
		MaxIdleConns:        100,
		MaxIdleConnsPerHost: 32,
		MaxConnsPerHost:     0, // unlimited
		IdleConnTimeout:     90 * time.Second,
	}
	if proxyStr != "" {
		proxyURL, err := url.Parse(proxyStr)
		if err == nil {
			transport.Proxy = http.ProxyURL(proxyURL)
		}
	}

	d := &Downloader{
		Client: &http.Client{
			Jar:       jar,
			Timeout:   0, // Long downloads, handled by context
			Transport: transport,
		},
		Workers:         workers,
		Retries:         retries,
		FFmpegPath:      ffmpegPath,
		MaxResponseSize: DefaultMaxResponseSize,
	}

	if cookieInput != "" {
		d.loadCookies(cookieInput)
	}

	return d
}

// Fetch performs an HTTP GET with context, size-limited response body,
// dynamic Referer header, and clean URL handling.
func (d *Downloader) Fetch(ctx context.Context, target string) ([]byte, error) {
	target = utils.CleanURL(target)
	req, err := http.NewRequestWithContext(ctx, "GET", target, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/124.0.0.0 Safari/537.36")

	// Fixed Referer — CDN validates this must be the origin site, not the CDN domain.
	// Using the CDN host as Referer causes 404 on all segments.
	req.Header.Set("Referer", "https://www.pornhub.com/")

	resp, err := d.Client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("HTTP %s", resp.Status)
	}

	// Size-limited read to prevent OOM
	reader := io.LimitReader(resp.Body, d.MaxResponseSize)
	return io.ReadAll(reader)
}

// FetchFunc returns a function matching the extractor.FetchFunc type.
func (d *Downloader) FetchFunc() func(ctx context.Context, url string) ([]byte, error) {
	return d.Fetch
}

// loadCookies loads cookies from a Netscape cookie file or raw cookie string.
func (d *Downloader) loadCookies(input string) {
	if _, err := os.Stat(input); err == nil {
		// Netscape cookie file
		file, fErr := os.Open(input)
		if fErr != nil {
			return
		}
		defer file.Close()
		scanner := bufio.NewScanner(file)
		for scanner.Scan() {
			line := strings.TrimSpace(scanner.Text())
			if line == "" || strings.HasPrefix(line, "#") {
				continue
			}
			parts := strings.Split(line, "\t")
			if len(parts) >= 7 {
				domain := strings.TrimPrefix(parts[0], ".")
				u := &url.URL{Scheme: "https", Host: domain}
				cookie := &http.Cookie{Name: parts[5], Value: parts[6]}
				d.Client.Jar.SetCookies(u, []*http.Cookie{cookie})
			}
		}
	} else {
		// Raw cookie string "key1=val1; key2=val2"
		pairs := strings.Split(input, ";")
		// Extract domain from referer pattern – default fallback
		u := &url.URL{Scheme: "https", Host: "www.pornhub.com"}
		var cookies []*http.Cookie
		for _, pair := range pairs {
			parts := strings.SplitN(strings.TrimSpace(pair), "=", 2)
			if len(parts) == 2 {
				cookies = append(cookies, &http.Cookie{Name: parts[0], Value: parts[1]})
			}
		}
		d.Client.Jar.SetCookies(u, cookies)
	}
}
