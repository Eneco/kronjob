package main

import (
	"os"
	"time"

	"github.com/Sirupsen/logrus"
	"github.com/eneco/kronjob/pkg/kronjob"
	"github.com/spf13/cobra"
	prefixed "github.com/x-cray/logrus-prefixed-formatter"
)

var cfg = &kronjob.Config{}

var rootCmd = &cobra.Command{
	Use:   "kronjob",
	Short: "A kubernetes job scheduler",
	PersistentPreRunE: func(cmd *cobra.Command, args []string) error {
		if cfg.Verbose {
			logrus.SetLevel(logrus.DebugLevel)
		}
		return nil
	},
	SilenceUsage: true,
}

func init() {
	_ = rootCmd.PersistentFlags()
	p := &prefixed.TextFormatter{
		ForceColors: true,
	}
	p.TimestampFormat = time.RFC3339
	logrus.SetFormatter(p)
}

func main() {
	if err := rootCmd.Execute(); err != nil {
		os.Exit(1)
	}
}
