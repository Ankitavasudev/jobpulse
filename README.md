# jobpulse

Summarise Kubernetes Jobs and CronJobs: what succeeded, what failed, how long it
took, and what resources each Job's pod template asks for. Reads a saved
capture, raw `kubectl` output, or the current cluster.

```console
$ jobpulse --from examples/capture.json
Batch jobs — examples/capture.json
Generated 2026-11-05 00:12 IST

6 jobs · 2 succeeded · 1 failed · 1 running · 1 pending · 1 suspended

JOB                            STATUS       OK FAIL ACTIVE   CPU   MEM DURATION
--------------------------------------------------------------------------------
data/import-catalog            Pending       0    0      0     1 2.0Gi -
default/nightly-backup         Succeeded     1    0      0  0.25 256Mi 4m30s
ml-jobs/eval-bert              Failed        0    4      0     2 8.0Gi 5m0s
ml-jobs/gpu-batch              Suspended     0    0      0     8 32.0Gi -
ml-jobs/preprocess             Running       0    0      1   0.5 1.0Gi 6h12m
ml-jobs/train-resnet           Succeeded     1    0      0     4 16.0Gi 1h12m
```

## What it reads

Only standard `batch/v1` resources:

- **Job** — `status.succeeded`, `status.failed`, `status.active`, the
  `Complete`/`Failed` conditions, `spec.backoffLimit`, `spec.suspend`, start and
  completion times.
- **CronJob** — `spec.schedule`, `spec.suspend`, `status.lastScheduleTime`, and
  the number of active child jobs.

Status is derived in this order: a `Complete=True` condition is `Succeeded`, a
`Failed=True` condition is `Failed`, `spec.suspend` is `Suspended`, active pods
are `Running`, then succeeded/failed counts, otherwise `Pending`.

Resource numbers come from the Job's **pod template requests** (falling back to
limits), summed over all containers. They are what the Job intends to use, not
what its pods actually consumed. Actual usage needs metrics-server, Prometheus,
or another metrics source; this tool does not invent it.

Durations end at `status.completionTime` when present, otherwise at report
time. That means a running Job shows its age so far.

## Install

```bash
go install github.com/Ankitavasudev/jobpulse@latest
```

Requires Go 1.21 or newer. Or grab a prebuilt binary — every tag ships Linux,
macOS and Windows builds (amd64 + arm64) on the [releases page](https://github.com/Ankitavasudev/jobpulse/releases),
built with [GoReleaser](https://goreleaser.com).

## Usage

```bash
# A saved capture with jobs and cronjobs arrays
jobpulse --from examples/capture.json

# Raw kubectl output
kubectl get jobs -A -o json > jobs.json
kubectl get cronjobs -A -o json > cronjobs.json
jobpulse --jobs jobs.json --cronjobs cronjobs.json

# The current kubectl context
jobpulse

# One namespace
jobpulse --namespace ml-jobs

# Other formats
jobpulse --from examples/capture.json -o json
jobpulse --from examples/capture.json -o csv
jobpulse --from examples/capture.json -o html > batch-report.html
```

## Capture format

One JSON file with `jobs` and `cronjobs` arrays in the Kubernetes API shape.
Extra fields are ignored, so `kubectl get ... -o json` output can be pasted in
directly:

```json
{
  "jobs": [
    {
      "metadata": { "name": "train-resnet", "namespace": "ml-jobs" },
      "spec": {
        "backoffLimit": 3,
        "template": {
          "spec": {
            "containers": [
              {
                "name": "trainer",
                "resources": { "requests": { "cpu": "4", "memory": "16Gi", "nvidia.com/gpu": "1" } }
              }
            ]
          }
        }
      },
      "status": {
        "succeeded": 1,
        "startTime": "2026-11-01T08:00:00Z",
        "completionTime": "2026-11-01T09:12:00Z",
        "conditions": [{ "type": "Complete", "status": "True" }]
      }
    }
  ],
  "cronjobs": []
}
```

## Output

| Format | Use |
| --- | --- |
| `text` (default) | Summary, job table, namespace breakdown, CronJobs, failure reasons |
| `json` | Full report with per-job fields |
| `csv` | One row per Job, for spreadsheets |
| `html` | Single file with inline CSS, no external assets |

Jobs sort by `namespace/name`; namespaces sort by name. The same capture always
produces the same ordering.

## Demo

A generated report for the example capture is published at
<https://ankitavasudev.github.io/jobpulse/>.

## Limitations

- Standard `batch/v1` only. It does not read Volcano, Kueue, or other CRDs.
- No pod-level history or logs; the Job status is the source of truth.
- CPU and memory are parsed from Kubernetes quantities; unusual custom units
  are treated as zero.
- Live mode shells out to `kubectl` and uses the selected context.

## License

MIT
