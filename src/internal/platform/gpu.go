package platform

import (
	"ocf/internal/common"
	"os/exec"
	"strconv"
	"strings"
)

func GetGPUInfo() []common.GPUSpec {
	cmd := exec.Command("nvidia-smi", "--query-gpu=name,memory.total,memory.used", "--format=csv,noheader,nounits")
	out, err := cmd.Output()
	if err != nil {
		common.Logger.Info("Error running nvidia-smi: ", err, " - GPU info will be unavailable (this is expected if no NVIDIA GPU is present)")
		return []common.GPUSpec{}
	}
	lines := strings.Split(strings.TrimSpace(string(out)), "\n")
	var gpus []common.GPUSpec
	for _, line := range lines {
		fields := strings.Split(line, ",")
		if len(fields) < 3 {
			continue
		}
		name := strings.TrimSpace(fields[0])
		total, _ := strconv.ParseInt(strings.TrimSpace(fields[1]), 10, 64)
		used, _ := strconv.ParseInt(strings.TrimSpace(fields[2]), 10, 64)
		gpus = append(gpus, common.GPUSpec{
			Name:        name,
			TotalMemory: total,
			UsedMemory:  used,
		})
	}
	return gpus
}
