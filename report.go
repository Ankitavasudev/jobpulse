package main

import (
	"encoding/csv"
	"encoding/json"
	"fmt"
	"html/template"
	"io"
	"sort"
	"strconv"
	"strings"
	"time"
)

type JobReport struct {
	Namespace    string `json:"namespace"`
	Name         string `json:"name"`
	Status       string `json:"status"`
	Succeeded    int    `json:"succeeded"`
	Failed       int    `json:"failed"`
	Active       int    `json:"active"`
	BackoffLimit int    `json:"backoff_limit"`
	CPU          string `json:"cpu"`
	Memory       string `json:"memory"`
	GPUs         int    `json:"gpus"`
	Duration     string `json:"duration"`
	Started      string `json:"started,omitempty"`
	Completed    string `json:"completed,omitempty"`
	Reason       string `json:"reason,omitempty"`
}

type CronJobReport struct {
	Namespace    string `json:"namespace"`
	Name         string `json:"name"`
	Schedule     string `json:"schedule"`
	Suspended    bool   `json:"suspended"`
	LastSchedule string `json:"last_schedule,omitempty"`
	ActiveJobs   int    `json:"active_jobs"`
}

type NamespaceReport struct {
	Name      string `json:"name"`
	Jobs      int    `json:"jobs"`
	Succeeded int    `json:"succeeded"`
	Failed    int    `json:"failed"`
	Running   int    `json:"running"`
}

type Summary struct {
	Jobs          int `json:"jobs"`
	Succeeded     int `json:"succeeded"`
	Failed        int `json:"failed"`
	Running       int `json:"running"`
	Pending       int `json:"pending"`
	Suspended     int `json:"suspended"`
	CronJobs      int `json:"cronjobs"`
	SuspendedCron int `json:"suspended_cronjobs"`
	Namespaces    int `json:"namespaces"`
	FailedPods    int `json:"failed_pods"`
	SucceededPods int `json:"succeeded_pods"`
}

type Report struct {
	Generated  string            `json:"generated"`
	Source     string            `json:"source"`
	Namespace  string            `json:"namespace,omitempty"`
	Summary    Summary           `json:"summary"`
	Jobs       []JobReport       `json:"jobs"`
	CronJobs   []CronJobReport   `json:"cronjobs"`
	Namespaces []NamespaceReport `json:"namespaces"`
}

func jobKey(namespace, name string) string { return namespace + "/" + name }

func defaultBackoff(job Job) int {
	if job.Spec.BackoffLimit != nil {
		return *job.Spec.BackoffLimit
	}
	return 6
}

func jobStatus(job Job) string {
	for _, condition := range job.Status.Conditions {
		if condition.Status != "True" {
			continue
		}
		switch condition.Type {
		case "Complete":
			return "Succeeded"
		case "Failed":
			return "Failed"
		}
	}
	if job.Spec.Suspend != nil && *job.Spec.Suspend {
		return "Suspended"
	}
	if job.Status.Active > 0 {
		return "Running"
	}
	if job.Status.Succeeded > 0 {
		return "Succeeded"
	}
	if job.Status.Failed > 0 {
		return "Failed"
	}
	return "Pending"
}

func jobReason(job Job) string {
	for _, condition := range job.Status.Conditions {
		if condition.Status == "True" && condition.Type == "Failed" {
			if condition.Message != "" {
				return condition.Message
			}
			if condition.Reason != "" {
				return condition.Reason
			}
		}
	}
	if job.Status.Failed > 0 {
		return fmt.Sprintf("%d pod(s) failed; backoff limit %d", job.Status.Failed, defaultBackoff(job))
	}
	return ""
}

func formatTime(value *string) string {
	if value == nil || *value == "" {
		return ""
	}
	parsed, err := time.Parse(time.RFC3339, *value)
	if err != nil {
		return *value
	}
	return parsed.Format("2006-01-02 15:04")
}

func jobDuration(job Job, now time.Time) string {
	if job.Status.StartTime == nil || *job.Status.StartTime == "" {
		return "-"
	}
	start, err := time.Parse(time.RFC3339, *job.Status.StartTime)
	if err != nil {
		return "-"
	}
	end := now
	if job.Status.CompletionTime != nil && *job.Status.CompletionTime != "" {
		if parsed, err := time.Parse(time.RFC3339, *job.Status.CompletionTime); err == nil {
			end = parsed
		}
	}
	if end.Before(start) {
		return "-"
	}
	d := end.Sub(start)
	switch {
	case d >= 24*time.Hour:
		return fmt.Sprintf("%dd%dh", int(d.Hours())/24, int(d.Hours())%24)
	case d >= time.Hour:
		return fmt.Sprintf("%dh%dm", int(d.Hours()), int(d.Minutes())%60)
	case d >= time.Minute:
		return fmt.Sprintf("%dm%ds", int(d.Minutes()), int(d.Seconds())%60)
	default:
		return fmt.Sprintf("%ds", int(d.Seconds()))
	}
}

// buildReport is pure: the caller supplies "now", so tests and demos can pin a
// clock and get the same durations every run.
func buildReport(capture Capture, namespace string, now time.Time) Report {
	report := Report{
		Namespace: namespace,
		Jobs:      []JobReport{},
		CronJobs:  []CronJobReport{},
	}
	namespaces := map[string]*NamespaceReport{}

	for _, job := range capture.Jobs {
		if namespace != "" && job.Metadata.Namespace != namespace {
			continue
		}
		cpu, memory, gpus := requests(job)
		item := JobReport{
			Namespace:    job.Metadata.Namespace,
			Name:         job.Metadata.Name,
			Status:       jobStatus(job),
			Succeeded:    job.Status.Succeeded,
			Failed:       job.Status.Failed,
			Active:       job.Status.Active,
			BackoffLimit: defaultBackoff(job),
			CPU:          formatCPU(cpu),
			Memory:       formatMemory(memory),
			GPUs:         gpus,
			Duration:     jobDuration(job, now),
			Started:      formatTime(job.Status.StartTime),
			Completed:    formatTime(job.Status.CompletionTime),
			Reason:       jobReason(job),
		}
		report.Jobs = append(report.Jobs, item)

		ns := namespaces[item.Namespace]
		if ns == nil {
			ns = &NamespaceReport{Name: item.Namespace}
			namespaces[item.Namespace] = ns
		}
		ns.Jobs++
		switch item.Status {
		case "Succeeded":
			ns.Succeeded++
		case "Failed":
			ns.Failed++
		case "Running":
			ns.Running++
		}

		report.Summary.Jobs++
		report.Summary.SucceededPods += item.Succeeded
		report.Summary.FailedPods += item.Failed
		switch item.Status {
		case "Succeeded":
			report.Summary.Succeeded++
		case "Failed":
			report.Summary.Failed++
		case "Running":
			report.Summary.Running++
		case "Suspended":
			report.Summary.Suspended++
		default:
			report.Summary.Pending++
		}
	}

	for _, cronjob := range capture.CronJobs {
		if namespace != "" && cronjob.Metadata.Namespace != namespace {
			continue
		}
		item := CronJobReport{
			Namespace:    cronjob.Metadata.Namespace,
			Name:         cronjob.Metadata.Name,
			Schedule:     cronjob.Spec.Schedule,
			Suspended:    cronjob.Spec.Suspend,
			LastSchedule: formatTime(cronjob.Status.LastScheduleTime),
			ActiveJobs:   len(cronjob.Status.Active),
		}
		report.CronJobs = append(report.CronJobs, item)
		report.Summary.CronJobs++
		if item.Suspended {
			report.Summary.SuspendedCron++
		}
	}

	sort.SliceStable(report.Jobs, func(i, j int) bool {
		return jobKey(report.Jobs[i].Namespace, report.Jobs[i].Name) < jobKey(report.Jobs[j].Namespace, report.Jobs[j].Name)
	})
	sort.SliceStable(report.CronJobs, func(i, j int) bool {
		return jobKey(report.CronJobs[i].Namespace, report.CronJobs[i].Name) < jobKey(report.CronJobs[j].Namespace, report.CronJobs[j].Name)
	})

	names := make([]string, 0, len(namespaces))
	for name := range namespaces {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		report.Namespaces = append(report.Namespaces, *namespaces[name])
	}
	report.Summary.Namespaces = len(report.Namespaces)
	return report
}

func renderText(w io.Writer, report Report) error {
	fmt.Fprintf(w, "Batch jobs — %s\n", report.Source)
	fmt.Fprintf(w, "Generated %s", report.Generated)
	if report.Namespace != "" {
		fmt.Fprintf(w, " · namespace %s", report.Namespace)
	}
	fmt.Fprintln(w)

	if report.Summary.Jobs == 0 {
		fmt.Fprintln(w, "\nNo Jobs in this capture.")
	} else {
		fmt.Fprintf(w, "\n%d jobs · %d succeeded · %d failed · %d running · %d pending · %d suspended\n",
			report.Summary.Jobs, report.Summary.Succeeded, report.Summary.Failed,
			report.Summary.Running, report.Summary.Pending, report.Summary.Suspended)
		fmt.Fprintln(w)
		fmt.Fprintf(w, "%-30s %-10s %4s %4s %6s %5s %5s %-9s\n",
			"JOB", "STATUS", "OK", "FAIL", "ACTIVE", "CPU", "MEM", "DURATION")
		fmt.Fprintln(w, strings.Repeat("-", 80))
		for _, job := range report.Jobs {
			fmt.Fprintf(w, "%-30s %-10s %4d %4d %6d %5s %5s %-9s\n",
				job.Namespace+"/"+job.Name, job.Status, job.Succeeded, job.Failed,
				job.Active, job.CPU, job.Memory, job.Duration)
		}
		fmt.Fprintln(w, strings.Repeat("-", 80))
	}

	if len(report.Namespaces) > 0 {
		fmt.Fprintln(w, "\nNamespaces")
		for _, ns := range report.Namespaces {
			fmt.Fprintf(w, "  %-24s %3d jobs  %3d ok  %3d failed  %3d running\n",
				ns.Name, ns.Jobs, ns.Succeeded, ns.Failed, ns.Running)
		}
	}

	if len(report.CronJobs) > 0 {
		fmt.Fprintf(w, "\nCronJobs (%d)\n", len(report.CronJobs))
		for _, cronjob := range report.CronJobs {
			state := "active"
			if cronjob.Suspended {
				state = "suspended"
			}
			last := cronjob.LastSchedule
			if last == "" {
				last = "never"
			}
			fmt.Fprintf(w, "  %-30s %-12s %-10s last %s\n",
				cronjob.Namespace+"/"+cronjob.Name, cronjob.Schedule, state, last)
		}
	}

	for _, job := range report.Jobs {
		if job.Reason != "" {
			fmt.Fprintf(w, "\n%s: %s\n", jobKey(job.Namespace, job.Name), job.Reason)
		}
	}
	return nil
}

func renderJSON(w io.Writer, report Report) error {
	encoder := json.NewEncoder(w)
	encoder.SetIndent("", "  ")
	return encoder.Encode(report)
}

func renderCSV(w io.Writer, report Report) error {
	cw := csv.NewWriter(w)
	if err := cw.Write([]string{
		"namespace", "job", "status", "succeeded", "failed", "active",
		"backoff_limit", "cpu", "memory", "gpus", "duration", "started", "completed", "reason",
	}); err != nil {
		return err
	}
	for _, job := range report.Jobs {
		if err := cw.Write([]string{
			job.Namespace, job.Name, job.Status,
			strconv.Itoa(job.Succeeded), strconv.Itoa(job.Failed), strconv.Itoa(job.Active),
			strconv.Itoa(job.BackoffLimit), job.CPU, job.Memory, strconv.Itoa(job.GPUs),
			job.Duration, job.Started, job.Completed, job.Reason,
		}); err != nil {
			return err
		}
	}
	cw.Flush()
	return cw.Error()
}

var reportTemplate = template.Must(template.New("report").Funcs(template.FuncMap{
	"statusClass": func(status string) string {
		switch status {
		case "Succeeded":
			return "ok"
		case "Failed":
			return "bad"
		case "Running":
			return "run"
		case "Suspended":
			return "muted"
		}
		return "warn"
	},
}).Parse(`<!DOCTYPE html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>jobpulse report</title>
<style>
:root{--bg:#0b0f14;--panel:#121b23;--card:#16232d;--line:#23343f;--text:#eaf1f8;--muted:#8ba3b5;--accent:#00adb5;--ok:#3fb950;--warn:#d29922;--bad:#f85149;--run:#58a6ff}
*{box-sizing:border-box}
body{margin:0;background:radial-gradient(1000px 500px at 50% -10%,#123039 0%,var(--bg) 55%);color:var(--text);font:15px/1.5 ui-sans-serif,system-ui,-apple-system,"Segoe UI",Roboto,sans-serif}
main{max-width:1120px;margin:0 auto;padding:40px 20px 64px}
h1{font-size:26px;margin:0 0 4px}
.sub{color:var(--muted);font-size:13px;margin-bottom:24px}
.cards{display:grid;grid-template-columns:repeat(5,minmax(0,1fr));gap:12px;margin-bottom:22px}
@media(max-width:860px){.cards{grid-template-columns:repeat(2,minmax(0,1fr))}}
.card{background:var(--panel);border:1px solid var(--line);border-radius:12px;padding:14px}
.card .k{color:var(--muted);font-size:12px;text-transform:uppercase;letter-spacing:.06em}
.card .v{font-size:26px;font-weight:700;margin-top:2px}
.card .v.ok{color:var(--ok)}.card .v.warn{color:var(--warn)}.card .v.bad{color:var(--bad)}.card .v.run{color:var(--run)}
.panel{background:var(--panel);border:1px solid var(--line);border-radius:12px;padding:16px;margin-bottom:14px}
.panel h2{font-size:16px;margin:0 0 10px}
table{width:100%;border-collapse:collapse;font-size:13px}
th,td{text-align:left;padding:7px 8px;border-bottom:1px solid var(--line);vertical-align:top}
th{color:var(--muted);font-weight:500;font-size:11px;text-transform:uppercase;letter-spacing:.05em}
td.num{font-family:ui-monospace,Consolas,monospace}
code{font-family:ui-monospace,SFMono-Regular,Consolas,monospace;background:#0b141a;border:1px solid var(--line);border-radius:4px;padding:1px 6px;font-size:12px}
.badge{display:inline-block;font-size:11px;border-radius:999px;padding:1px 8px;border:1px solid var(--line)}
.badge.ok{color:var(--ok);border-color:var(--ok)}.badge.bad{color:var(--bad);border-color:var(--bad)}
.badge.run{color:var(--run);border-color:var(--run)}.badge.warn{color:var(--warn);border-color:var(--warn)}.badge.muted{color:var(--muted)}
.muted{color:var(--muted)}
.reason{color:var(--muted);font-size:12px;margin-top:3px}
footer{color:var(--muted);font-size:12px;margin-top:30px}
.empty{color:var(--muted);font-size:13px}
</style>
</head>
<body>
<main>
<h1>Batch jobs</h1>
<div class="sub">Source: {{.Source}} · generated {{.Generated}}{{if .Namespace}} · namespace {{.Namespace}}{{end}}</div>

<div class="cards">
  <div class="card"><div class="k">Jobs</div><div class="v">{{.Summary.Jobs}}</div></div>
  <div class="card"><div class="k">Succeeded</div><div class="v ok">{{.Summary.Succeeded}}</div></div>
  <div class="card"><div class="k">Failed</div><div class="v {{if .Summary.Failed}}bad{{else}}ok{{end}}">{{.Summary.Failed}}</div></div>
  <div class="card"><div class="k">Running</div><div class="v run">{{.Summary.Running}}</div></div>
  <div class="card"><div class="k">Pending / suspended</div><div class="v warn">{{.Summary.Pending}} / {{.Summary.Suspended}}</div></div>
</div>

<div class="panel">
<h2>Jobs</h2>
{{if .Jobs}}<table>
<thead><tr><th>Job</th><th>Status</th><th>OK</th><th>Fail</th><th>Active</th><th>CPU</th><th>Memory</th><th>GPU</th><th>Duration</th></tr></thead>
<tbody>{{range .Jobs}}<tr>
<td><code>{{.Namespace}}/{{.Name}}</code>{{if .Reason}}<div class="reason">{{.Reason}}</div>{{end}}</td>
<td><span class="badge {{statusClass .Status}}">{{.Status}}</span></td>
<td class="num">{{.Succeeded}}</td><td class="num">{{.Failed}}</td><td class="num">{{.Active}}</td>
<td class="num">{{.CPU}}</td><td class="num">{{.Memory}}</td><td class="num">{{.GPUs}}</td><td class="num">{{.Duration}}</td>
</tr>{{end}}</tbody>
</table>{{else}}<p class="empty">No Jobs in this capture.</p>{{end}}
</div>

{{if .Namespaces}}<div class="panel">
<h2>Namespaces</h2>
<table><thead><tr><th>Namespace</th><th>Jobs</th><th>Succeeded</th><th>Failed</th><th>Running</th></tr></thead>
<tbody>{{range .Namespaces}}<tr><td>{{.Name}}</td><td class="num">{{.Jobs}}</td><td class="num">{{.Succeeded}}</td><td class="num">{{.Failed}}</td><td class="num">{{.Running}}</td></tr>{{end}}</tbody></table>
</div>{{end}}

{{if .CronJobs}}<div class="panel">
<h2>CronJobs</h2>
<table><thead><tr><th>CronJob</th><th>Schedule</th><th>State</th><th>Last schedule</th><th>Active jobs</th></tr></thead>
<tbody>{{range .CronJobs}}<tr><td><code>{{.Namespace}}/{{.Name}}</code></td><td><code>{{.Schedule}}</code></td><td>{{if .Suspended}}<span class="badge muted">suspended</span>{{else}}<span class="badge ok">active</span>{{end}}</td><td>{{if .LastSchedule}}{{.LastSchedule}}{{else}}<span class="muted">never</span>{{end}}</td><td class="num">{{.ActiveJobs}}</td></tr>{{end}}</tbody></table>
</div>{{end}}

<footer>Generated by jobpulse {{.Version}}. Resource numbers come from the Job pod template, not from pod usage. Durations end at the completion time, or at report time for unfinished jobs.</footer>
</main>
</body>
</html>
`))

func renderHTML(w io.Writer, report Report) error {
	return reportTemplate.Execute(w, struct {
		Report
		Version string
	}{report, versionString()})
}
