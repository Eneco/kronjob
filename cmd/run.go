package main

import (
	"errors"
	"os"
	"strconv"
	"strings"

	"github.com/Sirupsen/logrus"
	"github.com/eneco/kronjob/pkg/kronjob"
	"github.com/spf13/cobra"
)

var runCmd = &cobra.Command{
	Use:   "run",
	Short: "Starts the kronjob scheduler",
	RunE: func(cmd *cobra.Command, args []string) error {
		if cfg.Schedule == "" {
			return errors.New("a schedule is required through either the environment or the --schedule parameter")
		}
		if cfg.Template == "" {
			return errors.New("a job template is required through either the environment or the --template parameter")
		}

		v := kronjob.GetVersion()
		scheduler, err := kronjob.NewScheduler(cfg)
		if err != nil {
			return err
		}

		logrus.WithFields(logrus.Fields{"tag": v.GitTag, "commit": v.GitCommit}).Infof("This is Kronjob v%s", v.SemVer)
		logrus.WithFields(logrus.Fields{"schedule": cfg.Schedule, "verbose": cfg.Verbose}).Info("Start the scheduler")

		scheduler.Run()

		return nil
	},
}

func init() {
	f := runCmd.Flags()

	schedule := os.Getenv("SCHEDULE")
	template := os.Getenv("TEMPLATE")
	deadline := os.Getenv("DEADLINE")
	containerName := os.Getenv("HOSTNAME")
	namespace := os.Getenv("NAMESPACE")
	allowParalellVal := os.Getenv("ALLOW_PARALLEL")
	enableMetricsPrometheusVal := os.Getenv("ENABLE_METRICS_PROMETHEUS")

	allowParallel := len(allowParalellVal) == 0 || strings.ToLower(allowParalellVal) != "false"
	enableMetricsPrometheus := len(enableMetricsPrometheusVal) == 0 || strings.ToLower(enableMetricsPrometheusVal) != "false"

	if deadline == "" {
		deadline = "60"
	}

	deadlineInt, _ := strconv.Atoi(deadline)

	f.BoolVarP(&cfg.Verbose, "verbose", "v", false, "be verbose. defaults to false")
	f.StringVar(&cfg.Schedule, "schedule", schedule, "the cron schedule to use")
	f.StringVar(&cfg.Template, "template", template, "the job template to use")
	f.IntVar(&cfg.Deadline, "deadline", deadlineInt, "the jobs deadline in seconds. defaults to 60")
	f.StringVar(&cfg.ContainerName, "container-name", containerName, "the name of the container that runs kronjob. this is automatically set by kubernetes in each pod. used to find which namespace the jobs should run in")
	f.StringVar(&cfg.Namespace, "namespace", namespace, "the namespace the jobs should be run in")
	f.BoolVarP(&cfg.AllowParallel, "allow-parallel", "p", allowParallel, "allow jobs to run in parallel. defaults to false")
	f.BoolVar(&cfg.EnableMetricsPrometheus, "enable-metrics-prometheus", enableMetricsPrometheus, "enable the collection of metrics and expose them through a /metrics prometheus scraping endpoint")

	rootCmd.AddCommand(runCmd)
}
