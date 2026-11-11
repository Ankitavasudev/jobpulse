package main

import (
	"bytes"
	"encoding/json"
	"strconv"
	"strings"
	"testing"
	"time"
)

func strPtr(value string) *string { return &value }
func intPtr(value int) *int       { return &value }
func boolPtr(value bool) *bool    { return &value }

func testJob(namespace, name, status string, gpus int) Job {
	job := Job{Metadata: ObjectMeta{Namespace: namespace, Name: name}}
	job.Spec.BackoffLimit = intPtr(3)
	job.Spec.Template.Spec.Containers = []Container{{Name: "main"}}
	job.Spec.Template.Spec.Containers[0].Resources.Requests = map[string]string{
		"cpu": "2", "memory": "4Gi",
	}
	if gpus > 0 {
		job.Spec.Template.Spec.Containers[0].Resources.Requests["nvidia.com/gpu"] = strconv.Itoa(gpus)
	}
	job.Status.StartTime = strPtr("2026-11-05T10:00:00Z")
	switch status {
	case "Succeeded":
		job.Status.Succeeded = 1
		job.Status.CompletionTime = strPtr("2026-11-05T10:10:00Z")
		job.Status.Conditions = []Condition{{Type: "Complete", Status: "True"}}
	case "Failed":
		job.Status.Failed = 4
		job.Status.Conditions = []Condition{{Type: "Failed", Status: "True", Message: "backoff limit reached"}}
	case "Running":
		job.Status.Active = 1
	case "Suspended":
		job.Spec.Suspend = boolPtr(true)
	}
	return job
}

func TestQuantityParsing(t *testing.T) {
	cpu := map[string]int64{"": 0, "2": 2000, "500m": 500, "0.5": 500, "1.5": 1500, "bad": 0}
	for input, want := range cpu {
		if got := quantityMillis(input); got != want {
			t.Errorf("quantityMillis(%q) = %d, want %d", input, got, want)
		}
	}
	memory := map[string]int64{
		"":       0,
		"512Mi":  512 * 1024 * 1024,
		"1Gi":    1024 * 1024 * 1024,
		"2G":     2 * 1000 * 1000 * 1000,
		"1024":   1024,
		"1.5Gi":  1610612736,
		"broken": 0,
	}
	for input, want := range memory {
		if got := quantityBytes(input); got != want {
			t.Errorf("quantityBytes(%q) = %d, want %d", input, got, want)
		}
	}
}

func TestFormatting(t *testing.T) {
	if got := formatCPU(2000); got != "2" {
		t.Errorf("formatCPU(2000) = %q", got)
	}
	if got := formatCPU(500); got != "0.5" {
		t.Errorf("formatCPU(500) = %q", got)
	}
	if got := formatCPU(0); got != "-" {
		t.Errorf("formatCPU(0) = %q", got)
	}
	if got := formatMemory(2 * 1024 * 1024 * 1024); got != "2.0Gi" {
		t.Errorf("formatMemory(2Gi) = %q", got)
	}
	if got := formatMemory(0); got != "-" {
		t.Errorf("formatMemory(0) = %q", got)
	}
}

func TestGPUCountIgnoresLabels(t *testing.T) {
	container := Container{}
	container.Resources.Requests = map[string]string{"nvidia.com/gpu": "2", "nvidia.com/gpu.present": "true"}
	if got := gpuCount(container.Resources.Requests, nil); got != 2 {
		t.Errorf("gpuCount() = %d, want 2", got)
	}
	if got := gpuCount(map[string]string{"cpu": "2"}, nil); got != 0 {
		t.Errorf("gpuCount() without a GPU = %d, want 0", got)
	}
}

func TestRequestsFallBackToLimits(t *testing.T) {
	job := testJob("ml", "j", "Running", 0)
	job.Spec.Template.Spec.Containers = []Container{{Name: "a"}, {Name: "b"}}
	job.Spec.Template.Spec.Containers[0].Resources.Limits = map[string]string{"cpu": "1", "memory": "1Gi"}
	job.Spec.Template.Spec.Containers[1].Resources.Requests = map[string]string{"cpu": "2", "memory": "2Gi"}
	cpu, memory, gpus := requests(job)
	if cpu != 3000 || memory != 3*1024*1024*1024 || gpus != 0 {
		t.Errorf("requests() = %d, %d, %d", cpu, memory, gpus)
	}
}

func TestJobStatus(t *testing.T) {
	cases := []struct {
		name string
		job  Job
		want string
	}{
		{"complete", testJob("ml", "j", "Succeeded", 0), "Succeeded"},
		{"failed", testJob("ml", "j", "Failed", 0), "Failed"},
		{"running", testJob("ml", "j", "Running", 0), "Running"},
		{"suspended", testJob("ml", "j", "Suspended", 0), "Suspended"},
		{"pending", testJob("ml", "j", "Pending", 0), "Pending"},
	}
	for _, c := range cases {
		if got := jobStatus(c.job); got != c.want {
			t.Errorf("%s: jobStatus() = %q, want %q", c.name, got, c.want)
		}
	}
}

func TestJobDuration(t *testing.T) {
	now := time.Date(2026, 11, 5, 12, 0, 0, 0, time.UTC)
	job := testJob("ml", "j", "Succeeded", 0)
	if got := jobDuration(job, now); got != "10m0s" {
		t.Errorf("completed duration = %q", got)
	}
	running := testJob("ml", "j", "Running", 0)
	if got := jobDuration(running, now); got != "2h0m" {
		t.Errorf("running duration = %q", got)
	}
	running.Status.StartTime = nil
	if got := jobDuration(running, now); got != "-" {
		t.Errorf("missing start duration = %q", got)
	}
}

func TestBuildReportSummaryAndSorting(t *testing.T) {
	now := time.Date(2026, 11, 5, 12, 0, 0, 0, time.UTC)
	capture := Capture{
		Jobs: []Job{
			testJob("z", "last", "Running", 1),
			testJob("a", "first", "Succeeded", 2),
			testJob("a", "failed", "Failed", 0),
			testJob("a", "sleeping", "Suspended", 0),
			testJob("a", "waiting", "Pending", 0),
		},
		CronJobs: []CronJob{
			{Metadata: ObjectMeta{Namespace: "a", Name: "nightly"}},
		},
	}
	capture.CronJobs[0].Spec.Schedule = "0 2 * * *"
	capture.CronJobs[0].Spec.Suspend = true

	report := buildReport(capture, "", now)
	s := report.Summary
	if s.Jobs != 5 || s.Succeeded != 1 || s.Failed != 1 || s.Running != 1 || s.Suspended != 1 || s.Pending != 1 {
		t.Errorf("summary = %+v", s)
	}
	if s.CronJobs != 1 || s.SuspendedCron != 1 || s.Namespaces != 2 {
		t.Errorf("cron/namespace summary = %+v", s)
	}
	if s.FailedPods != 4 {
		t.Errorf("failed pods = %d, want 4", s.FailedPods)
	}
	if report.Jobs[0].Name != "failed" || report.Jobs[1].Name != "first" {
		t.Errorf("jobs are not sorted by namespace/name: %+v", report.Jobs)
	}
	if report.Namespaces[0].Name != "a" || report.Namespaces[1].Name != "z" {
		t.Errorf("namespaces are not sorted: %+v", report.Namespaces)
	}
	if report.Namespaces[0].Jobs != 4 || report.Namespaces[0].Running != 0 {
		t.Errorf("namespace counts = %+v", report.Namespaces[0])
	}
}

func TestBuildReportNamespaceFilter(t *testing.T) {
	now := time.Now()
	capture := Capture{Jobs: []Job{
		testJob("ml", "keep", "Running", 0),
		testJob("other", "drop", "Running", 0),
	}}
	report := buildReport(capture, "ml", now)
	if len(report.Jobs) != 1 || report.Jobs[0].Name != "keep" {
		t.Errorf("namespace filter = %+v", report.Jobs)
	}
}

func TestBuildReportIsDeterministic(t *testing.T) {
	now := time.Date(2026, 11, 5, 12, 0, 0, 0, time.UTC)
	capture := Capture{Jobs: []Job{
		testJob("b", "two", "Running", 0),
		testJob("a", "one", "Running", 0),
	}}
	first := buildReport(capture, "", now)
	second := buildReport(capture, "", now)
	if first.Jobs[0].Name != second.Jobs[0].Name || first.Jobs[1].Name != second.Jobs[1].Name {
		t.Error("buildReport is not deterministic")
	}
}

func TestRenderText(t *testing.T) {
	now := time.Date(2026, 11, 5, 12, 0, 0, 0, time.UTC)
	report := buildReport(Capture{
		Jobs:     []Job{testJob("ml", "train", "Succeeded", 2)},
		CronJobs: []CronJob{{Metadata: ObjectMeta{Namespace: "ml", Name: "nightly"}}},
	}, "", now)
	var out bytes.Buffer
	if err := renderText(&out, report); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"Batch jobs", "ml/train", "Succeeded", "CronJobs", "nightly"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("text output is missing %q", want)
		}
	}
}

func TestRenderJSONRoundTrips(t *testing.T) {
	report := buildReport(Capture{Jobs: []Job{testJob("ml", "train", "Succeeded", 1)}}, "", time.Now())
	var out bytes.Buffer
	if err := renderJSON(&out, report); err != nil {
		t.Fatal(err)
	}
	var decoded Report
	if err := json.Unmarshal(out.Bytes(), &decoded); err != nil {
		t.Fatalf("output is not valid JSON: %v", err)
	}
	if decoded.Summary.Jobs != 1 || decoded.Jobs[0].GPUs != 1 {
		t.Errorf("decoded report = %+v", decoded)
	}
}

func TestRenderCSV(t *testing.T) {
	report := buildReport(Capture{Jobs: []Job{testJob("ml", "train", "Succeeded", 1)}}, "", time.Now())
	var out bytes.Buffer
	if err := renderCSV(&out, report); err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(out.String()), "\n")
	if len(lines) != 2 {
		t.Fatalf("expected a header and one row, got %d lines", len(lines))
	}
	if !strings.HasPrefix(lines[0], "namespace,job,status") {
		t.Errorf("header = %q", lines[0])
	}
	if !strings.Contains(lines[1], "ml,train,Succeeded,1,0,0,3") {
		t.Errorf("row = %q", lines[1])
	}
}

func TestRenderHTMLIsSelfContainedAndEscaped(t *testing.T) {
	job := testJob("ml", "<script>alert(1)</script>", "Failed", 1)
	job.Status.Conditions[0].Message = "<b>boom</b>"
	report := buildReport(Capture{Jobs: []Job{job}}, "", time.Now())
	var out bytes.Buffer
	if err := renderHTML(&out, report); err != nil {
		t.Fatal(err)
	}
	html := out.String()
	for _, want := range []string{"<!DOCTYPE html>", "<style>", "Batch jobs", "Generated by jobpulse"} {
		if !strings.Contains(html, want) {
			t.Errorf("HTML is missing %q", want)
		}
	}
	if strings.Contains(html, "<script") {
		t.Error("HTML contains a script tag")
	}
	if strings.Contains(html, "<b>boom</b>") {
		t.Error("condition message was not escaped")
	}
	if !strings.Contains(html, "&lt;script&gt;alert(1)&lt;/script&gt;") {
		t.Error("job name was not escaped")
	}
	if strings.Contains(html, `<link`) || strings.Contains(html, `src="http`) {
		t.Error("HTML loads an external resource")
	}
}

func TestExampleCaptureLoads(t *testing.T) {
	capture, err := loadCapture("examples/capture.json")
	if err != nil {
		t.Fatal(err)
	}
	report := buildReport(capture, "", time.Date(2026, 11, 5, 12, 0, 0, 0, time.UTC))
	if report.Summary.Jobs != 6 {
		t.Errorf("example has %d jobs, want 6", report.Summary.Jobs)
	}
	if report.Summary.Succeeded != 2 || report.Summary.Failed != 1 || report.Summary.Running != 1 {
		t.Errorf("example summary = %+v", report.Summary)
	}
	if report.Summary.CronJobs != 2 || report.Summary.SuspendedCron != 1 {
		t.Errorf("example cronjobs = %+v", report.Summary)
	}
	if report.Summary.FailedPods != 4 {
		t.Errorf("example failed pods = %d, want 4", report.Summary.FailedPods)
	}
}

func TestVersionString(t *testing.T) {
	if strings.TrimSpace(versionString()) == "" {
		t.Error("versionString() is empty")
	}
}
