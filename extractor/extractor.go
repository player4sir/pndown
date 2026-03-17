package extractor

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"regexp"
	"sort"
	"strings"
)

// ── Models ──────────────────────────────────────────────────────────────

type MediaDefinition struct {
	Group          int         `json:"group"`
	Height         int         `json:"height"`
	Width          int         `json:"width"`
	DefaultQuality bool        `json:"defaultQuality"`
	Format         string      `json:"format"`
	VideoUrl       string      `json:"videoUrl"`
	Quality        interface{} `json:"quality"`
	Remote         bool        `json:"remote"`
}

type FlashVars struct {
	MediaDefinitions []MediaDefinition `json:"mediaDefinitions"`
	VideoTitle       string            `json:"video_title"`
	LinkUrl          string            `json:"link_url"`
}

type GetMediaResponse []struct {
	Default  bool   `json:"default"`
	Format   string `json:"format"`
	Height   string `json:"height"`
	Quality  string `json:"quality"`
	VideoUrl string `json:"videoUrl"`
}

// FetchFunc is a function type for performing HTTP fetches, used for
// dependency injection so the extractor does not depend on the Downloader.
type FetchFunc func(ctx context.Context, url string) ([]byte, error)

// ── Extraction ──────────────────────────────────────────────────────────

// ExtractFlashVars parses the flashvars JSON from an HTML page body.
func ExtractFlashVars(content []byte) (*FlashVars, error) {
	re := regexp.MustCompile(`var \s*flashvars_\d+\s*=\s*(\{.*?\});`)
	match := re.FindSubmatch(content)
	var jsonData []byte
	if match != nil {
		jsonData = match[1]
	} else {
		reStart := regexp.MustCompile(`var \s*flashvars_\d+\s*=\s*\{`)
		loc := reStart.FindIndex(content)
		if loc == nil {
			return nil, fmt.Errorf("flashvars not found")
		}
		candidate := content[loc[1]-1:]
		depth := 0
		for i, b := range candidate {
			if b == '{' {
				depth++
			} else if b == '}' {
				depth--
				if depth == 0 {
					jsonData = candidate[:i+1]
					break
				}
			}
		}
	}
	if jsonData == nil {
		return nil, fmt.Errorf("extraction failed")
	}
	var f FlashVars
	err := json.Unmarshal(jsonData, &f)
	return &f, err
}

// ResolveMediaDefinitions resolves remote media definitions by fetching
// get_media endpoints, and collects all valid streams.
//
// BUG FIX: The original code had a flawed if/else-if that silently
// dropped remote HLS streams and non-remote non-mp4 streams.
func ResolveMediaDefinitions(base []MediaDefinition, fetch FetchFunc) []MediaDefinition {
	var extracted []MediaDefinition
	for _, stream := range base {
		// Remote MP4 streams with get_media endpoint → resolve to direct URLs
		if stream.Format == "mp4" && stream.Remote && strings.Contains(stream.VideoUrl, "get_media") {
			data, err := fetch(context.Background(), stream.VideoUrl)
			if err != nil {
				continue
			}
			var resp GetMediaResponse
			if err := json.Unmarshal(data, &resp); err != nil {
				continue
			}
			for _, r := range resp {
				h := 0
				fmt.Sscanf(r.Height, "%d", &h)
				extracted = append(extracted, MediaDefinition{
					Height:   h,
					Format:   r.Format,
					VideoUrl: r.VideoUrl,
					Quality:  r.Quality,
				})
			}
			continue
		}

		// Non-remote streams (both mp4 and hls) → keep directly
		if !stream.Remote && stream.VideoUrl != "" {
			extracted = append(extracted, stream)
			continue
		}

		// Remote HLS or other remote streams with valid URL → keep
		if stream.Remote && stream.VideoUrl != "" && stream.Format != "" {
			extracted = append(extracted, stream)
		}
	}
	return extracted
}

// SortStreams sorts media definitions by height (descending), preferring mp4.
func SortStreams(streams []MediaDefinition) {
	sort.Slice(streams, func(i, j int) bool {
		if streams[i].Height != streams[j].Height {
			return streams[i].Height > streams[j].Height
		}
		return streams[i].Format == "mp4"
	})
}

// PickStream selects the best matching stream for the given quality.
func PickStream(streams []MediaDefinition, quality int) MediaDefinition {
	if quality > 0 {
		for _, s := range streams {
			if s.Height == quality {
				return s
			}
		}
	}
	if len(streams) > 0 {
		return streams[0]
	}
	return MediaDefinition{}
}

// DynamicReferer extracts the scheme+host from a URL to use as referer.
func DynamicReferer(targetURL string) string {
	if targetURL == "" {
		return ""
	}
	parsed, err := http.NewRequest("GET", targetURL, nil)
	if err != nil || parsed.URL == nil {
		return ""
	}
	return parsed.URL.Scheme + "://" + parsed.URL.Host + "/"
}
