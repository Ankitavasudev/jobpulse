# k8s-batch-analyzer

Kubernetes batch job analyzer for monitoring job queues, tracking completion rates, and analyzing resource patterns. Works with Volcano, Job, and CronJob resources.

## Features

- Monitor batch job queues in real-time
- Track job completion rates and failure patterns
- Analyze resource requests vs actual usage
- Export job history for analysis
- Support for Volcano Queue, Job, and PodGroup
- Alert on job failures

## Installation

```bash
go install github.com/Ankitavasudev/k8s-batch-analyzer@latest
```

## Usage

```bash
# Watch all jobs
k8s-batch-analyzer watch

# Watch specific queue (Volcano)
k8s-batch-analyzer watch --queue training-queue

# Get job statistics
k8s-batch-analyzer stats --namespace ml-jobs

# Export job history
k8s-batch-analyzer export --format json --output job-history.json

# Alert on failures
k8s-batch-analyzer watch --alert-on-failure
```

## Example Output

```
QUEUE: training-queue
=================================================
JOB NAME          STATUS    COMPLETION   RESOURCES
-------------------------------------------------
train-bert-v1     Running   45/100       4 CPU, 16Gi
train-resnet-v2   Running   89/100       8 CPU, 32Gi
train-gpt-v3      Pending   0/100        16 CPU, 64Gi
inference-batch   Complete  100/100      2 CPU, 8Gi
=================================================
Total: 4 jobs | Running: 2 | Pending: 1 | Complete: 1
Resource Usage: 30 CPU, 120Gi
```

## Volcano Integration

Works with Volcano scheduler for batch workload analysis:

```bash
# Monitor Volcano queues
k8s-batch-analyzer volcano queues

# Get queue resource allocation
k8s-batch-analyzer volcano resources --queue training-queue

# Analyze job scheduling patterns
k8s-batch-analyzer volcano scheduling --queue training-queue
```

## Building

```bash
git clone https://github.com/Ankitavasudev/k8s-batch-analyzer.git
cd k8s-batch-analyzer
go build -o k8s-batch-analyzer .
```

## License

MIT
