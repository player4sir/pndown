package utils

import (
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"unicode"
)

// SanitizeFilename removes characters that are invalid in file names.
func SanitizeFilename(name string) string {
	re := regexp.MustCompile(`[<>:"/\\|?*]`)
	res := strings.TrimSpace(re.ReplaceAllString(name, "_"))
	if len(res) > 60 {
		res = res[:60]
	}
	return res
}

// GetUniqueFilename returns a file path that does not already exist.
func GetUniqueFilename(dir, name string) string {
	ext := filepath.Ext(name)
	base := strings.TrimSuffix(name, ext)
	target := filepath.Join(dir, name)
	if _, err := os.Stat(target); os.IsNotExist(err) {
		return target
	}
	for i := 1; ; i++ {
		target = filepath.Join(dir, fmt.Sprintf("%s (%d)%s", base, i, ext))
		if _, err := os.Stat(target); os.IsNotExist(err) {
			return target
		}
	}
}

// ParseURL parses a URL string, returning nil on error instead of panicking.
func ParseURL(s string) *url.URL {
	u, _ := url.Parse(s)
	return u
}

// CleanURL removes BOM, control characters, but preserves valid UTF-8 for
// internationalized URLs.
func CleanURL(s string) string {
	s = strings.TrimSpace(s)
	s = strings.TrimPrefix(s, "\ufeff")
	s = strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return -1
		}
		return r
	}, s)
	return s
}

// EnsureFFmpeg locates or downloads ffmpeg, returning its path.
func EnsureFFmpeg() (string, error) {
	path, err := exec.LookPath("ffmpeg")
	if err == nil {
		return path, nil
	}
	if runtime.GOOS == "windows" {
		if _, err := os.Stat("ffmpeg.exe"); err == nil {
			return "./ffmpeg.exe", nil
		}
		fmt.Println("FFmpeg not found. Downloading essential binaries...")
		const dlURL = "https://www.gyan.dev/ffmpeg/builds/ffmpeg-release-essentials.zip"
		_ = exec.Command("powershell", "-Command", fmt.Sprintf("Invoke-WebRequest -Uri '%s' -OutFile ffmpeg.zip", dlURL)).Run()
		_ = exec.Command("powershell", "-Command", "Expand-Archive -Path ffmpeg.zip -DestinationPath ffmpeg_temp -Force").Run()
		var found string
		filepath.Walk("ffmpeg_temp", func(p string, info os.FileInfo, err error) error {
			if err != nil {
				return nil
			}
			if !info.IsDir() && info.Name() == "ffmpeg.exe" {
				os.Rename(p, "ffmpeg.exe")
				found = "./ffmpeg.exe"
				return fmt.Errorf("stop")
			}
			return nil
		})
		os.Remove("ffmpeg.zip")
		os.RemoveAll("ffmpeg_temp")
		if found != "" {
			return found, nil
		}
	}
	return "", fmt.Errorf("ffmpeg not found")
}
