package hardware

import (
	"fmt"
	"runtime"

	log "github.com/sirupsen/logrus"
)

// Info holds detected hardware capabilities.
type Info struct {
	OS            string `json:"os"`
	Arch          string `json:"arch"`
	TotalMemoryMB int64  `json:"total_memory_mb"`
	NumCPU        int    `json:"num_cpu"`
}

// Detect probes the current machine's hardware.
func Detect() Info {
	info := Info{
		OS:     runtime.GOOS,
		Arch:   runtime.GOARCH,
		NumCPU: runtime.NumCPU(),
	}

	mem, err := totalMemoryBytes()
	if err != nil {
		log.WithError(err).Warn("could not detect total memory, defaulting to 16GB")
		info.TotalMemoryMB = 16 * 1024
	} else {
		info.TotalMemoryMB = int64(mem / (1024 * 1024))
	}

	return info
}

// ModelSelection holds the recommended model tag for Ollama.
type ModelSelection struct {
	Tag         string `json:"tag"`
	DisplayName string `json:"display_name"`
	SizeMB      int64  `json:"size_mb"`
	Reason      string `json:"reason"`
}

// SelectModel picks the best Gemma 4 model for the detected hardware.
// On Apple Silicon, we prefer the mlx-bf16 variants for best Metal performance.
// The model must fit in memory with room for the OS and other processes (~8GB headroom).
func SelectModel(info Info) ModelSelection {
	availMB := info.TotalMemoryMB - 8*1024 // Reserve 8GB for OS/apps

	log.WithFields(log.Fields{
		"total_memory_mb":     info.TotalMemoryMB,
		"available_for_model": availMB,
		"os":                  info.OS,
		"arch":                info.Arch,
	}).Info("selecting model based on hardware")

	isAppleSilicon := info.OS == "darwin" && info.Arch == "arm64"

	// Model options ordered from best to most compact.
	// Sizes are approximate download sizes in MB.
	type candidate struct {
		tag         string
		displayName string
		sizeMB      int64
	}

	var candidates []candidate
	if isAppleSilicon {
		candidates = []candidate{
			{"gemma4:27b-mlx-bf16", "Gemma 4 27B (MLX bf16)", 53248},   // 52GB — needs 64GB+ machine
			{"gemma4:26b-a4b-it-q4_K_M", "Gemma 4 26B-A4B (Q4_K_M)", 18432}, // 18GB
			{"gemma4:e4b-mlx-bf16", "Gemma 4 E4B (MLX bf16)", 16384},  // 16GB
			{"gemma4:e4b-it-q4_K_M", "Gemma 4 E4B (Q4_K_M)", 9830},   // 9.6GB
			{"gemma4:e2b-mlx-bf16", "Gemma 4 E2B (MLX bf16)", 10240},  // 10GB
			{"gemma4:e2b-it-q4_K_M", "Gemma 4 E2B (Q4_K_M)", 7373},   // 7.2GB
		}
	} else {
		candidates = []candidate{
			{"gemma4:31b-it-q4_K_M", "Gemma 4 31B (Q4_K_M)", 20480},  // 20GB
			{"gemma4:26b-a4b-it-q4_K_M", "Gemma 4 26B-A4B (Q4_K_M)", 18432}, // 18GB
			{"gemma4:e4b-it-q8_0", "Gemma 4 E4B (Q8)", 12288},        // 12GB
			{"gemma4:e4b-it-q4_K_M", "Gemma 4 E4B (Q4_K_M)", 9830},  // 9.6GB
			{"gemma4:e2b-it-q8_0", "Gemma 4 E2B (Q8)", 8294},        // 8.1GB
			{"gemma4:e2b-it-q4_K_M", "Gemma 4 E2B (Q4_K_M)", 7373}, // 7.2GB
		}
	}

	for _, c := range candidates {
		if availMB >= c.sizeMB {
			return ModelSelection{
				Tag:         c.tag,
				DisplayName: c.displayName,
				SizeMB:      c.sizeMB,
				Reason:      fmt.Sprintf("fits in %dMB available (needs %dMB)", availMB, c.sizeMB),
			}
		}
	}

	// Fallback: smallest model
	last := candidates[len(candidates)-1]
	return ModelSelection{
		Tag:         last.tag,
		DisplayName: last.displayName,
		SizeMB:      last.sizeMB,
		Reason:      fmt.Sprintf("fallback to smallest model (%dMB available, model needs %dMB)", availMB, last.sizeMB),
	}
}
