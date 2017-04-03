package main

import (
	"fmt"
	"time"

	"github.com/Sirupsen/logrus"
	"github.com/spf13/cobra"
	cron "gopkg.in/robfig/cron.v2"
)

var explainableSchedule string
var explainableSchedules int

var explainCmd = &cobra.Command{
	Use:   "explain",
	Short: "Explains the provided schedule by writing the next n dates to stdout",
	RunE: func(cmd *cobra.Command, args []string) error {
		s, err := cron.Parse(explainableSchedule)
		if err != nil {
			return fmt.Errorf("cannot parse the schedule: %s", err)
		}

		t := time.Now()

		for i := 0; i < explainableSchedules; i++ {
			t = s.Next(t)
			logrus.Infof("#%d: %s", i, t.Format(time.RFC3339Nano))
		}

		return nil
	},
}

func init() {
	explainCmd.Flags().StringVar(&explainableSchedule, "schedule", "", "the cron schedule to use")
	explainCmd.Flags().IntVarP(&explainableSchedules, "num", "n", 5, "how many dates to calculate during explanation")
	rootCmd.AddCommand(explainCmd)
}
