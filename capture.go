package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
)

// Capture is the saved shape of the two batch API responses. A Job alone is
// enough for the job report; CronJobs add schedule and suspension context.
type Capture struct {
	Jobs     []Job     `json:"jobs"`
	CronJobs []CronJob `json:"cronjobs"`
}

type ObjectMeta struct {
	Name      string            `json:"name"`
	Namespace string            `json:"namespace"`
	Labels    map[string]string `json:"labels"`
}

type Job struct {
	Metadata ObjectMeta `json:"metadata"`
	Spec     struct {
		BackoffLimit *int  `json:"backoffLimit"`
		Suspend      *bool `json:"suspend"`
		Template     struct {
			Spec PodSpec `json:"spec"`
		} `json:"template"`
	} `json:"spec"`
	Status struct {
		Active         int         `json:"active"`
		Succeeded      int         `json:"succeeded"`
		Failed         int         `json:"failed"`
		StartTime      *string     `json:"startTime"`
		CompletionTime *string     `json:"completionTime"`
		Conditions     []Condition `json:"conditions"`
	} `json:"status"`
}

type PodSpec struct {
	Containers []Container `json:"containers"`
}

type Container struct {
	Name      string `json:"name"`
	Resources struct {
		Requests map[string]string `json:"requests"`
		Limits   map[string]string `json:"limits"`
	} `json:"resources"`
}

type Condition struct {
	Type    string `json:"type"`
	Status  string `json:"status"`
	Reason  string `json:"reason"`
	Message string `json:"message"`
}

type CronJob struct {
	Metadata ObjectMeta `json:"metadata"`
	Spec     struct {
		Schedule string `json:"schedule"`
		Suspend  bool   `json:"suspend"`
	} `json:"spec"`
	Status struct {
		LastScheduleTime *string `json:"lastScheduleTime"`
		Active           []struct {
			Name string `json:"name"`
		} `json:"active"`
	} `json:"status"`
}

func parseJobs(data []byte) ([]Job, error) {
	var list struct {
		Items []Job `json:"items"`
	}
	if err := json.Unmarshal(data, &list); err == nil && list.Items != nil {
		return list.Items, nil
	}
	var wrapped struct {
		Jobs []Job `json:"jobs"`
	}
	if err := json.Unmarshal(data, &wrapped); err == nil && wrapped.Jobs != nil {
		return wrapped.Jobs, nil
	}
	var single Job
	if err := json.Unmarshal(data, &single); err == nil && single.Metadata.Name != "" {
		return []Job{single}, nil
	}
	return nil, errors.New("does not look like a JobList, a Job, or a capture with a jobs array")
}

func parseCronJobs(data []byte) ([]CronJob, error) {
	var list struct {
		Items []CronJob `json:"items"`
	}
	if err := json.Unmarshal(data, &list); err == nil && list.Items != nil {
		return list.Items, nil
	}
	var wrapped struct {
		CronJobs []CronJob `json:"cronjobs"`
	}
	if err := json.Unmarshal(data, &wrapped); err == nil && wrapped.CronJobs != nil {
		return wrapped.CronJobs, nil
	}
	var single CronJob
	if err := json.Unmarshal(data, &single); err == nil && single.Metadata.Name != "" {
		return []CronJob{single}, nil
	}
	return nil, errors.New("does not look like a CronJobList, a CronJob, or a capture with a cronjobs array")
}

func loadCapture(path string) (Capture, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Capture{}, err
	}
	var capture Capture
	if err := json.Unmarshal(data, &capture); err != nil {
		return Capture{}, fmt.Errorf("%s is not a capture: %w", path, err)
	}
	if capture.Jobs == nil && capture.CronJobs == nil {
		return Capture{}, fmt.Errorf("%s is not a capture: expected jobs or cronjobs arrays", path)
	}
	return capture, nil
}

func loadJobs(path string) ([]Job, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	jobs, err := parseJobs(data)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	return jobs, nil
}

func loadCronJobs(path string) ([]CronJob, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	cronjobs, err := parseCronJobs(data)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	return cronjobs, nil
}

// requests sums the pod template's requests, falling back to limits for each
// resource. A Job has no pod status, so this is what the Job intends to use,
// not what it used.
func requests(job Job) (cpuMillis int64, memoryBytes int64, gpus int) {
	for _, container := range job.Spec.Template.Spec.Containers {
		cpuMillis += quantityMillis(pick(container.Resources.Requests, container.Resources.Limits, "cpu"))
		memoryBytes += quantityBytes(pick(container.Resources.Requests, container.Resources.Limits, "memory"))
		gpus += gpuCount(container.Resources.Requests, container.Resources.Limits)
	}
	return cpuMillis, memoryBytes, gpus
}

func pick(requests, limits map[string]string, key string) string {
	if value := strings.TrimSpace(requests[key]); value != "" {
		return value
	}
	return strings.TrimSpace(limits[key])
}

func gpuCount(requests, limits map[string]string) int {
	total := 0
	for _, resources := range []map[string]string{requests, limits} {
		for key, value := range resources {
			if !isGPUResource(key) {
				continue
			}
			if n, err := strconv.Atoi(strings.TrimSpace(value)); err == nil && n > 0 {
				total += n
			}
		}
		if total > 0 {
			return total
		}
	}
	return 0
}

func isGPUResource(key string) bool {
	k := strings.ToLower(strings.TrimSpace(key))
	if strings.HasPrefix(k, "gpu.intel.com/") {
		return true
	}
	name := k
	if slash := strings.LastIndex(k, "/"); slash >= 0 {
		name = k[slash+1:]
	}
	return name == "gpu" || strings.HasPrefix(name, "mig-") || strings.HasPrefix(name, "ascend")
}

// quantityMillis parses Kubernetes CPU quantities: 2, 500m, 0.5, 1.5.
func quantityMillis(value string) int64 {
	value = strings.TrimSpace(value)
	if value == "" {
		return 0
	}
	if strings.HasSuffix(value, "m") {
		n, err := strconv.ParseInt(strings.TrimSuffix(value, "m"), 10, 64)
		if err != nil {
			return 0
		}
		return n
	}
	f, err := strconv.ParseFloat(value, 64)
	if err != nil {
		return 0
	}
	return int64(f * 1000)
}

var memoryUnits = []struct {
	suffix string
	scale  float64
}{
	{"Ki", 1024},
	{"Mi", 1024 * 1024},
	{"Gi", 1024 * 1024 * 1024},
	{"Ti", 1024 * 1024 * 1024 * 1024},
	{"K", 1000},
	{"M", 1000 * 1000},
	{"G", 1000 * 1000 * 1000},
	{"T", 1000 * 1000 * 1000 * 1000},
}

// quantityBytes parses Kubernetes memory quantities, including the binary
// suffixes (Ki, Mi, Gi, Ti) and the decimal ones (K, M, G, T).
func quantityBytes(value string) int64 {
	value = strings.TrimSpace(value)
	if value == "" {
		return 0
	}
	for _, unit := range memoryUnits {
		if strings.HasSuffix(value, unit.suffix) {
			f, err := strconv.ParseFloat(strings.TrimSuffix(value, unit.suffix), 64)
			if err != nil {
				return 0
			}
			return int64(f * unit.scale)
		}
	}
	f, err := strconv.ParseFloat(value, 64)
	if err != nil {
		return 0
	}
	return int64(f)
}

func formatCPU(millis int64) string {
	if millis == 0 {
		return "-"
	}
	if millis%1000 == 0 {
		return strconv.FormatInt(millis/1000, 10)
	}
	return strconv.FormatFloat(float64(millis)/1000, 'f', -1, 64)
}

func formatMemory(bytes int64) string {
	if bytes == 0 {
		return "-"
	}
	const gi = 1024 * 1024 * 1024
	const mi = 1024 * 1024
	if bytes >= gi {
		return strconv.FormatFloat(float64(bytes)/gi, 'f', 1, 64) + "Gi"
	}
	return strconv.FormatFloat(float64(bytes)/mi, 'f', 0, 64) + "Mi"
}
