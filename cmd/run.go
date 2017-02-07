package main

import (
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
		v := kronjob.GetVersion()

		scheduler, err := kronjob.NewScheduler(cfg)
		if err != nil {
			return err
		}

		logrus.WithFields(logrus.Fields{"tag": v.GitTag, "commit": v.GitCommit}).Infof("This is Kronjob v%s", v.SemVer)
		logrus.WithFields(logrus.Fields{"schedule": cfg.Schedule, "verbose": cfg.Verbose}).Info("Start the scheduler")

		err = scheduler.Run()
		if err != nil {
			return err
		}

		return nil
	},
}

func init() {
	f := runCmd.Flags()

	schedule := os.Getenv("SCHEDULE")
	template := os.Getenv("TEMPLATE")
	deadline := os.Getenv("DEADLINE")
	restartPolicy := os.Getenv("RESTART_POLICY")
	containerName := os.Getenv("HOSTNAME")
	allowParallel := strings.ToLower(os.Getenv("ALLOW_PARALLEL")) == "true"

	if deadline == "" {
		deadline = "60"
	}

	deadlineInt, _ := strconv.Atoi(deadline)

	f.BoolVarP(&cfg.Verbose, "verbose", "v", false, "be verbose. defaults to false")
	f.BoolVarP(&cfg.AllowParallel, "allow-parallel", "p", allowParallel, "allow jobs to run in parallel. defaults to false")
	f.StringVar(&cfg.Schedule, "schedule", schedule, "the cron schedule to use")
	f.StringVar(&cfg.Template, "template", template, "the job template to use")
	f.IntVar(&cfg.Deadline, "deadline", deadlineInt, "the jobs deadline in seconds. defaults to 60")
	f.StringVar(&cfg.RestartPolicy, "restart-policy", restartPolicy, "the restartPolicy to use")
	f.StringVar(&cfg.ContainerName, "container-name", containerName, "the name of the container that runs kronjob. this is automatically set by kubernetes in each pod. used to find which namespace the jobs should run in")

	rootCmd.AddCommand(runCmd)
}
