package hardware_test

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/Blink-Build-Studios/little-tyke/internal/hardware"
)

func TestDetect(t *testing.T) {
	info := hardware.Detect()
	assert.NotEmpty(t, info.OS)
	assert.NotEmpty(t, info.Arch)
	assert.Greater(t, info.TotalMemoryMB, int64(0))
	assert.Greater(t, info.NumCPU, 0)
}

func TestSelectModel(t *testing.T) {
	tests := []struct {
		name     string
		info     hardware.Info
		wantTag  string
	}{
		{
			name:    "large apple silicon machine",
			info:    hardware.Info{OS: "darwin", Arch: "arm64", TotalMemoryMB: 96 * 1024},
			wantTag: "gemma4:27b-mlx-bf16",
		},
		{
			name:    "36GB apple silicon",
			info:    hardware.Info{OS: "darwin", Arch: "arm64", TotalMemoryMB: 36 * 1024},
			wantTag: "gemma4:26b-a4b-it-q4_K_M",
		},
		{
			name:    "24GB apple silicon",
			info:    hardware.Info{OS: "darwin", Arch: "arm64", TotalMemoryMB: 24 * 1024},
			wantTag: "gemma4:e4b-mlx-bf16",
		},
		{
			name:    "16GB apple silicon",
			info:    hardware.Info{OS: "darwin", Arch: "arm64", TotalMemoryMB: 16 * 1024},
			wantTag: "gemma4:e2b-it-q4_K_M",
		},
		{
			name:    "32GB linux",
			info:    hardware.Info{OS: "linux", Arch: "amd64", TotalMemoryMB: 32 * 1024},
			wantTag: "gemma4:31b-it-q4_K_M",
		},
		{
			name:    "16GB linux",
			info:    hardware.Info{OS: "linux", Arch: "amd64", TotalMemoryMB: 16 * 1024},
			wantTag: "gemma4:e2b-it-q4_K_M",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			sel := hardware.SelectModel(tt.info)
			assert.Equal(t, tt.wantTag, sel.Tag)
			assert.NotEmpty(t, sel.DisplayName)
			assert.NotEmpty(t, sel.Reason)
		})
	}
}
