package main

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"runtime/debug"
	"strings"
	"time"

	"github.com/spf13/cobra"
)

var version = "v1.0.0"

func versionString() string {
	if info, ok := debug.ReadBuildInfo(); ok {
		v := info.Main.Version
		if v != "" && v != "(devel)" && !strings.HasPrefix(v, "v0.0.0-") && !strings.Contains(v, "+dirty") {
			return v
		}
	}
	return version
}

var (
	flagFrom      string
	flagJobs      string
	flagCronJobs  string
	flagNamespace string
	flagOutput    string
)

var rootCmd = &cobra.Command{
	Use:   "k8s-batch-analyzer",
	Short: "Summarise Kubernetes Jobs and CronJobs",
	Long: "Read Kubernetes Jobs and CronJobs and report completion, failures,\n" +
		"durations and requested resources.\n\n" +
		"Use --from with a saved capture, --jobs/--cronjobs with kubectl output,\n" +
		"or no flags to read the current kubectl context.",
	SilenceUsage: true,
	RunE: func(cmd *cobra.Command, args []string) error {
		capture, source, err := loadInput()
		if err != nil {
			return err
		}
		report := buildReport(capture, flagNamespace, time.Now())
		report.Generated = time.Now().Format("2006-01-02 15:04 MST")
		report.Source = source

		out := cmd.OutOrStdout()
		switch flagOutput {
		case "text":
			return renderText(out, report)
		case "json":
			return renderJSON(out, report)
		case "csv":
			return renderCSV(out, report)
		case "html":
			return renderHTML(out, report)
		default:
			return fmt.Errorf("unknown output %q: use text, json, csv or html", flagOutput)
		}
	},
}

var versionCmd = &cobra.Command{
	Use:   "version",
	Short: "Print the version",
	Run: func(cmd *cobra.Command, args []string) {
		fmt.Fprintln(cmd.OutOrStdout(), versionString())
	},
}

func loadInput() (Capture, string, error) {
	switch {
	case flagFrom != "":
		capture, err := loadCapture(flagFrom)
		return capture, flagFrom, err
	case flagJobs != "" || flagCronJobs != "":
		if flagJobs == "" || flagCronJobs == "" {
			return Capture{}, "", errors.New("--jobs and --cronjobs must be given together, or use --from with one capture")
		}
		jobs, err := loadJobs(flagJobs)
		if err != nil {
			return Capture{}, "", fmt.Errorf("reading jobs: %w", err)
		}
		cronjobs, err := loadCronJobs(flagCronJobs)
		if err != nil {
			return Capture{}, "", fmt.Errorf("reading cronjobs: %w", err)
		}
		return Capture{Jobs: jobs, CronJobs: cronjobs}, flagJobs + " + " + flagCronJobs, nil
	default:
		capture, err := collectLive()
		return capture, "live cluster (kubectl)", err
	}
}

func kubectlJSON(args ...string) ([]byte, error) {
	output, err := exec.Command("kubectl", args...).Output()
	if err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) && len(exitErr.Stderr) > 0 {
			return nil, fmt.Errorf("kubectl %s: %s", args[0], string(exitErr.Stderr))
		}
		return nil, fmt.Errorf("kubectl %s: %w (is kubectl installed and a context selected?)", args[0], err)
	}
	return output, nil
}

func collectLive() (Capture, error) {
	jobsJSON, err := kubectlJSON("get", "jobs", "-A", "-o", "json")
	if err != nil {
		return Capture{}, err
	}
	cronjobsJSON, err := kubectlJSON("get", "cronjobs", "-A", "-o", "json")
	if err != nil {
		return Capture{}, err
	}
	jobs, err := parseJobs(jobsJSON)
	if err != nil {
		return Capture{}, fmt.Errorf("parsing kubectl jobs output: %w", err)
	}
	cronjobs, err := parseCronJobs(cronjobsJSON)
	if err != nil {
		return Capture{}, fmt.Errorf("parsing kubectl cronjobs output: %w", err)
	}
	return Capture{Jobs: jobs, CronJobs: cronjobs}, nil
}

func init() {
	rootCmd.Flags().StringVar(&flagFrom, "from", "", "combined capture with jobs and cronjobs arrays")
	rootCmd.Flags().StringVar(&flagJobs, "jobs", "", "JobList JSON from kubectl get jobs -A -o json")
	rootCmd.Flags().StringVar(&flagCronJobs, "cronjobs", "", "CronJobList JSON from kubectl get cronjobs -A -o json")
	rootCmd.Flags().StringVarP(&flagNamespace, "namespace", "n", "", "only include jobs in this namespace")
	rootCmd.Flags().StringVarP(&flagOutput, "output", "o", "text", "output format: text, json, csv or html")
	rootCmd.AddCommand(versionCmd)
}

func main() {
	if err := rootCmd.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, "Error:", err)
		os.Exit(1)
	}
}
