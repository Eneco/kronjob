package kronjob

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/Sirupsen/logrus"
	"github.com/armon/go-metrics"
	"github.com/armon/go-metrics/prometheus"
	"github.com/ghodss/yaml"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"gopkg.in/robfig/cron.v2"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/pkg/api/unversioned"
	"k8s.io/client-go/pkg/api/v1"
	batchv1 "k8s.io/client-go/pkg/apis/batch/v1"
	"k8s.io/client-go/pkg/watch"
)

// Scheduler is responsible for executing the job template on a given cron schedule
type Scheduler struct {
	cfg               *Config
	client            *kubernetes.Clientset
	cron              *cron.Cron
	currentExecutions map[string]*Execution
	jobSpec           *batchv1.JobSpec
	metrics           *metrics.Metrics
	namespace         string
	shutdownCh        chan error
	metricsSink       *metrics.Metrics
}

// NewScheduler creates and initializes a Scheduler which can be used to execute the job template on a given cron schedule
func NewScheduler(cfg *Config) (*Scheduler, error) {
	client, err := GetKubeClient()
	if err != nil {
		return nil, err
	}

	scheduler := &Scheduler{
		cfg:               cfg,
		client:            client,
		cron:              cron.New(),
		currentExecutions: map[string]*Execution{},
		shutdownCh:        makeShutdownCh(),
	}

	var sink metrics.MetricSink = &metrics.BlackholeSink{}

	if cfg.EnableMetricsPrometheus {
		sink, err = prometheus.NewPrometheusSink()
		if err != nil {
			return nil, err
		}
	}

	metricsSink, err := metrics.New(metrics.DefaultConfig("kronjob"), sink)
	if err != nil {
		return nil, err
	}

	scheduler.metricsSink = metricsSink

	if err != nil {
		return nil, err
	}

	logrus.WithField("namespace", scheduler.Namespace()).Info("Created scheduler")

	return scheduler, nil
}

// Run starts the cron schedule and blocks until an error is potentially returned in the job execution
func (s *Scheduler) Run() {
	if _, err := s.cron.AddFunc(s.cfg.PlainSchedule, func() {
		err := s.Exec()
		if err != nil {
			s.shutdownCh <- err
		}
		logrus.Infof("The next job is scheduled to be at %s", s.cfg.Schedule.Next(time.Now()).Format(time.RFC3339Nano))
	}); err != nil {
		logrus.Fatal(err)
	}

	logrus.Infof("The first job is scheduled to be at %s", s.cfg.Schedule.Next(time.Now()).Format(time.RFC3339Nano))
	s.cron.Start()

	if s.cfg.EnableMetricsPrometheus {
		go func() {
			http.Handle(s.cfg.PrometheusEndpointPath, promhttp.Handler())
			log.Fatal(http.ListenAndServe(fmt.Sprintf(":%d", s.cfg.PrometheusEndpointPort), nil))
		}()

		logrus.Infof("Exposing prometheus %s scraping endpoint on :%d", s.cfg.PrometheusEndpointPath, s.cfg.PrometheusEndpointPort)
	}

	err := <-s.shutdownCh
	if err != nil {
		s.Stop(err)
		logrus.Fatal(err)
	}

	logrus.Info("Initiating graceful shutdown")
	s.Stop(nil)
}

// Stop tries to do a graceful shutdown, cleaning up all currently running jobs
func (s *Scheduler) Stop(stopErr error) {
	s.cron.Stop()

	for _, execution := range s.currentExecutions {
		execution.Stop(stopErr)
		delete(s.currentExecutions, execution.Job.Name)

		err := s.CleanJob(execution)
		if err != nil {
			logrus.WithField("error", err).Warn("Potential jobs & pods remaining after unsuccessful graceful shutdown")
		}
	}
}

// Exec is the method being called on the cronschedule. It creates a job, reads it's logs, waits for it to complete and then cleans it up
func (s *Scheduler) Exec() error {
	if !s.cfg.AllowParallel && s.IsRunning() {
		return nil
	}

	// Create a new job
	job, err := s.CreateJob()
	if err != nil {
		return err
	}

	execution := NewExecution(job)
	s.currentExecutions[execution.Job.Name] = execution

	// Start watching the job
	err = s.MonitorJob(execution)
	if err != nil {
		return err
	}

	// Start watching any pods created for the job
	err = s.MonitorPods(execution)
	if err != nil {
		return err
	}

	return nil
}

// IsRunning checks if there is already a job running
func (s *Scheduler) IsRunning() bool {
	return len(s.currentExecutions) > 0
}

// CreateJob instantiates a new job in the kubernetes namespace
func (s *Scheduler) CreateJob() (*batchv1.Job, error) {
	name := fmt.Sprintf("%s-job-%d", s.cfg.ContainerName, time.Now().Unix())
	jobSpec := s.JobSpec()

	logrus.WithField("name", name).
		WithField("schedule", s.cfg.PlainSchedule).
		WithField("job", name).
		Info("Creating job")

	job, err := s.client.Batch().Jobs(s.Namespace()).Create(&batchv1.Job{
		ObjectMeta: v1.ObjectMeta{
			Name: name,
		},
		Spec: *jobSpec,
	})
	if err != nil {
		return nil, err
	}

	logrus.WithField("job", name).Info("Job started")
	s.metricsSink.IncrCounter([]string{"jobs_started"}, 1)

	return job, nil
}

// CleanJob removes the job and it's associated pods from kubernetes
func (s *Scheduler) CleanJob(execution *Execution) error {
	if execution.Cleaned {
		return nil
	}

	logrus.WithField("job", execution.Job.Name).Info("Deleting job...")
	err := s.client.Batch().Jobs(s.Namespace()).Delete(execution.Job.Name, v1.NewDeleteOptions(0))
	if err != nil {
		logrus.WithField("error", err).WithField("job", execution.Job.Name).Warn("Unable to clean up job!")
	}

	logrus.WithField("job", execution.Job.Name).Info("Retrieving pods for job...")
	pods, err := s.JobPods(execution.Job)
	if err != nil {
		return err
	}

	if len(execution.Pods) != len(pods) {
		logrus.WithField("created", execution.Pods).WithField("cleaning", pods).Warn("Created pods and cleaning pods is unequal in length")
	}

	for _, pod := range pods {
		logrus.WithField("pod", pod.Name).Info("Deleting pod...")
		err := s.client.Core().Pods(s.Namespace()).Delete(pod.Name, v1.NewDeleteOptions(0))
		if err != nil {
			logrus.WithField("pod", pod.Name).Warn("Unable to clean pod!")
		}
	}

	logrus.WithField("job", execution.Job.Name).Info("Job successfully cleaned up")
	execution.Cleaned = true

	s.metricsSink.IncrCounter([]string{"jobs_cleaned"}, 1)

	return nil
}

// JobPods retrieves the pods in kubernetes associated with a specific job
func (s *Scheduler) JobPods(job *batchv1.Job) ([]v1.Pod, error) {
	pods, err := s.client.Core().Pods(s.Namespace()).List(v1.ListOptions{
		LabelSelector: fmt.Sprintf("job-name=%s", job.Name),
	})
	if err != nil {
		return nil, err
	}

	return pods.Items, nil
}

// MonitorJob creates a watcher in kubernetes, and waits for the job to have completed successfully
func (s *Scheduler) MonitorJob(execution *Execution) error {
	watcher, err := s.client.Batch().Jobs(s.Namespace()).Watch(v1.ListOptions{
		FieldSelector: fmt.Sprintf("metadata.name=%s", execution.Job.Name),
	})
	if err != nil {
		return err
	}

	go func() {
		logrus.WithField("job", execution.Job.Name).Info("Start monitoring job")

		event := watch.Event{}
		for {
			select {
			case <-execution.StopJobWatchCh:
				logrus.WithField("job", execution.Job.Name).Info("Stop monitoring job")
				s.metricsSink.IncrCounter([]string{"job_watches_stopped"}, 1)
				return

			case event = <-watcher.ResultChan():
				s.HandleJobEvent(&event, execution)
			}
		}
	}()

	return nil
}

// MonitorPods will tail the logs for the job's pod and write them to our own stdout
func (s *Scheduler) MonitorPods(execution *Execution) error {
	watcher, err := s.client.Core().Pods(s.Namespace()).Watch(v1.ListOptions{
		LabelSelector: fmt.Sprintf("job-name=%s", execution.Job.Name),
	})
	if err != nil {
		return err
	}

	// We spin up a new routine that watched for events happening related to pods for the job
	go func() {
		logrus.WithField("job", execution.Job.Name).Info("Start tailing pods for job")

		event := watch.Event{}
		for {
			select {
			case <-execution.StopPodWatchCh:
				logrus.WithField("job", execution.Job.Name).Info("Stop tailing pods for job...")
				s.metricsSink.IncrCounter([]string{"job_pod_watches_stopped"}, 1)
				return
			case event = <-watcher.ResultChan():
				s.HandlePodEvent(&event, execution)
			}
		}
	}()

	return nil
}

// HandleJobEvent expects a kubernetes watch event for a Job and handles deadlines, success/failure and activation of jobs
func (s *Scheduler) HandleJobEvent(event *watch.Event, execution *Execution) {
	// The events for this watcher contain a Job as their object, so cast it to a Job
	latestJob := event.Object.(*batchv1.Job)

	if event.Type != watch.Modified {
		return
	}

	if latestJob.Status.Succeeded > 0 || time.Since(execution.StartTime).Seconds() > float64(s.cfg.Deadline) {
		if latestJob.Status.Succeeded > 0 {
			logrus.WithField("job", execution.Job.Name).Info("Completed job...")

			s.metricsSink.IncrCounter([]string{"jobs_completed"}, 1)
			s.metricsSink.MeasureSince([]string{"last_job_duration"}, s.currentExecutions[latestJob.Name].StartTime)
			s.metricsSink.SetGauge([]string{"last_job_success_unixtime"}, float32(time.Now().Unix()))
		} else {
			logrus.WithField("job", execution.Job.Name).Error("Failed to complete job within deadline...")

			s.metricsSink.IncrCounter([]string{"jobs_timed_out"}, 1)
		}

		execution.Stop(nil)
		delete(s.currentExecutions, execution.Job.Name)
		s.CleanJob(execution)
		return
	}

	if latestJob.Status.Failed > execution.Failures {
		logrus.
			WithField("failed", latestJob.Status.Failed).
			WithField("job", latestJob.Name).
			Infof("Job execution failed")

		execution.Failures = latestJob.Status.Failed
		s.metricsSink.IncrCounter([]string{"jobs_failed"}, 1)
		s.metricsSink.SetGauge([]string{"last_job_failure_unixtime"}, float32(time.Now().Unix()))

		return
	}

	if latestJob.Status.Active > 0 {
		logrus.
			WithField("job", latestJob.Name).
			Info("Job activated...")

		s.metricsSink.IncrCounter([]string{"jobs_activated"}, 1)

		return
	}
}

// HandlePodEvent expects a kubernetes watch event for a Pod and handles success/failure and log processing for a pod
func (s *Scheduler) HandlePodEvent(event *watch.Event, execution *Execution) {
	// We sometimes seem to receive events with a nil Object
	if event.Object == nil {
		return
	}

	// The events for this watcher contain a Pod as their object, so cast it to be one
	eventPod := event.Object.(*v1.Pod)

	// If this pod was already processed (either failed or succeeded) then discard the event
	if _, processed := execution.ProcessedPods[eventPod.Name]; processed {
		return
	}

	if _, created := execution.Pods[eventPod.Name]; !created && eventPod.Status.Phase == v1.PodRunning {
		logrus.WithField("job", execution.Job.Name).WithField("pod", eventPod.Name).Info("New pod for job created")
		execution.Pods[eventPod.Name] = eventPod

		s.metricsSink.IncrCounter([]string{"job_pods_created"}, 1)

		return
	}

	if eventPod.Status.Phase == v1.PodFailed || eventPod.Status.Phase == v1.PodSucceeded {
		// We keep a log of pods we have processed to ensure we only retrieve the logs for it once
		execution.ProcessedPods[eventPod.Name] = true

		// Now retrieve the actual logs
		logs := s.PodLogs(eventPod)

		if eventPod.Status.Phase == v1.PodFailed {
			logrus.WithField("logs", "\n"+logs).Error("Pod execution failed")
		} else {
			logrus.WithField("logs", "\n"+logs).Info("Pod execution succeeded")
		}

		return
	}
}

// PodLogs retrieves the logs for a pod in kubernetes
func (s *Scheduler) PodLogs(pod *v1.Pod) string {
	sinceTime := unversioned.NewTime(time.Now().Add(time.Duration(-1 * time.Hour)))
	reader, err := s.client.Core().Pods(s.Namespace()).GetLogs(pod.Name, &v1.PodLogOptions{
		SinceTime: &sinceTime,
	}).Stream()
	if err != nil {
		logrus.WithField("error", err).WithField("pod", pod.Name).Error("Failed retrieving logs for job pod")
		return ""
	}
	defer reader.Close()

	buf := new(bytes.Buffer)
	buf.ReadFrom(reader)

	return buf.String()
}

// Namespace retrieves the namespace kronjob is running in to determine where to run jobs
func (s *Scheduler) Namespace() string {
	if s.cfg.Namespace != "" {
		return s.cfg.Namespace
	}

	// Figure out which namespace we are running in
	pods, err := s.client.Core().Pods("").List(v1.ListOptions{
		FieldSelector: fmt.Sprintf("metadata.name=%s", s.cfg.ContainerName),
	})
	if err != nil {
		logrus.Fatal(err)
	}
	if len(pods.Items) == 0 {
		logrus.Fatal(errors.New("Failed to find pod namespace. Are you sure you are running kronjob inside a kubernetes container?"))
	}

	s.cfg.Namespace = pods.Items[0].Namespace

	return s.cfg.Namespace
}

// JobSpec creates a new kubernetes Job spec based on the configured template
func (s *Scheduler) JobSpec() *batchv1.JobSpec {
	if s.jobSpec != nil {
		return s.jobSpec
	}

	deadline := int64(s.cfg.Deadline)
	parallelism := int32(1)

	logrus.WithField("deadline", deadline).WithField("template", "\n"+s.cfg.Template).Info("Creating job spec")

	jobSpec := &batchv1.JobSpec{
		ActiveDeadlineSeconds: &deadline,
		Parallelism:           &parallelism,
		Template:              v1.PodTemplateSpec{},
	}

	// Apparently there are some issues when unmarshalling yaml straight into a PodTemplateSpec
	// Thus we first conver tto json, and then unmarshal from there
	// Back to json
	jsonSpec, err := yaml.YAMLToJSON([]byte(s.cfg.Template))
	if err != nil {
		logrus.Fatal(err)
	}

	err = json.Unmarshal(jsonSpec, &jobSpec.Template.Spec)
	if err != nil {
		logrus.Fatal(err)
	}

	jobSpec.Template.Spec.RestartPolicy = v1.RestartPolicyNever

	s.jobSpec = jobSpec

	return jobSpec
}

// makeShutdownCh creates an interrupt listener and returns a channel.
// A message will be sent on the channel for every interrupt received.
func makeShutdownCh() chan error {
	resultCh := make(chan error)
	signalCh := make(chan os.Signal, 2)
	signal.Notify(signalCh, os.Interrupt, syscall.SIGTERM)

	go func() {
		for {
			<-signalCh
			logrus.Info("Shutdown signal received")
			resultCh <- nil
		}
	}()

	return resultCh
}
