package kronjob

import cron "gopkg.in/robfig/cron.v2"

// Config contains all the information about the k8s cluster and local configuration
type Config struct {
	AllowParallel           bool
	ContainerName           string
	Deadline                int
	EnableMetricsPrometheus bool
	Namespace               string
	PrometheusEndpointPath  string
	PrometheusEndpointPort  int
	PlainSchedule           string
	Schedule                cron.Schedule
	Template                string
	Verbose                 bool
}
