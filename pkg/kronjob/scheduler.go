package kronjob

import (
	"bytes"
	"errors"
	"fmt"
	"time"

	"github.com/Sirupsen/logrus"
	"gopkg.in/robfig/cron.v2"
	"gopkg.in/yaml.v2"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/pkg/api/unversioned"
	"k8s.io/client-go/pkg/api/v1"
	batchv1 "k8s.io/client-go/pkg/apis/batch/v1"
	"k8s.io/client-go/pkg/watch"
)

// Scheduler is responsible for executing the job template on a given cron schedule
type Scheduler struct {
	cfg       *Config
	client    *kubernetes.Clientset
	jobSpec   *batchv1.JobSpec
	namespace string
	running   int
}

// NewScheduler creates and initializes a Scheduler which can be used to execute the job template on a given cron schedule
func NewScheduler(cfg *Config) (*Scheduler, error) {
	client, err := GetKubeClient()
	if err != nil {
		return nil, err
	}

	scheduler := &Scheduler{
		cfg:    cfg,
		client: client,
	}

	logrus.WithField("namespace", scheduler.Namespace()).Info("Created scheduler")

	return scheduler, nil
}

// Run starts the cron schedule and blocks until an error is potentially returned in the job execution
func (s *Scheduler) Run() error {
	errChan := make(chan error)

	// We begin by executing the job once straight away
	err := s.Exec()
	if err != nil {
		return err
	}

	c := cron.New()
	c.AddFunc(s.cfg.Schedule, func() {
		errChan <- s.Exec()
	})
	c.Start()

	for {
		select {
		case err := <-errChan:
			if err != nil {
				return err
			}
		}
	}
}

// Exec is the method being called on the cronschedule. It creates a job, reads it's logs, waits for it to complete and then cleans it up
func (s *Scheduler) Exec() error {
	if !s.cfg.AllowParallel && s.HasRunningJob() {
		return nil
	}

	// Create a new job
	job, err := s.CreateJob()
	if err != nil {
		return err
	}

	// Tail the logs for the job
	stopChan, err := s.TailJob(job)
	if err != nil {
		return err
	}

	// Now wait for it to be completed
	err = s.WaitForJob(job, stopChan)
	if err != nil {
		return err
	}

	return nil
}

// CreateJob instantiates a new job in the kubernetes namespace
func (s *Scheduler) CreateJob() (*batchv1.Job, error) {
	name := fmt.Sprintf("%s-job-%d", s.cfg.ContainerName, time.Now().Unix())
	jobSpec := s.JobSpec()

	logrus.
		WithField("name", name).
		WithField("schedule", s.cfg.Schedule).
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

	logrus.WithField("job", name).Info("Job created")

	s.running = s.running + 1

	return job, nil
}

// CleanJob removes the job and it's associated pods from kubernetes
func (s *Scheduler) CleanJob(job *batchv1.Job) error {
	logrus.WithField("job", job.Name).Info("Deleting job...")
	err := s.client.Batch().Jobs(s.Namespace()).Delete(job.Name, v1.NewDeleteOptions(0))
	if err != nil {
		logrus.WithField("error", err).WithField("job", job).Warn("Unable to clean up job!")
	}

	s.running = s.running - 1

	logrus.WithField("job", job.Name).Info("Retrieving pods for job...")
	pods, err := s.JobPods(job)
	if err != nil {
		return err
	}

	for _, pod := range pods {
		logrus.WithField("pod", pod.Name).Info("Deleting pod...")
		err := s.client.Core().Pods(s.Namespace()).Delete(pod.Name, v1.NewDeleteOptions(0))
		if err != nil {
			logrus.WithField("pod", pod.Name).Warn("Unable to clean pod!")
		}
	}

	logrus.WithField("job", job.Name).Info("Job successfully cleaned up")
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

// WaitForJob creates a watcher in kubernetes, and waits for the job to have completed successfully
func (s *Scheduler) WaitForJob(job *batchv1.Job, stopChan chan bool) error {
	watcher, err := s.client.Batch().Jobs(s.Namespace()).Watch(v1.ListOptions{
		FieldSelector: fmt.Sprintf("metadata.name=%s", job.Name),
	})
	if err != nil {
		return err
	}

	currentFailures := int32(0)
	event := watch.Event{}
	startTime := time.Now()
	for {
		select {
		case event = <-watcher.ResultChan():
			latestJob := event.Object.(*batchv1.Job)
			if event.Type == watch.Modified {
				if latestJob.Status.Succeeded > 0 || time.Since(startTime).Seconds() > float64(s.cfg.Deadline) {
					if latestJob.Status.Succeeded > 0 {
						logrus.WithField("job", job.Name).Info("Completed job...")
					} else {
						logrus.WithField("job", job.Name).Error("Failed to complete job within deadline...")
					}

					stopChan <- true

					// Clean up the job's pods and the job itself. For some unknown reason if I do this instantly
					// sometimes the Kube API does not return the last pod, so I add a delay to this
					go func() {
						time.Sleep(5 * time.Second)
						err := s.CleanJob(job)
						if err != nil {
							logrus.WithField("job", job.Name).WithField("error", err).Warn("Unable to clean job")
						}
					}()

					return nil
				}

				if latestJob.Status.Failed > currentFailures {
					logrus.
						WithField("failed", latestJob.Status.Failed).
						WithField("job", latestJob.Name).
						Infof("Job execution failed")
					currentFailures = latestJob.Status.Failed
					continue
				}

				if latestJob.Status.Active > 0 {
					logrus.
						WithField("job", latestJob.Name).
						Info("Job activated...")
				}
			}
		}
	}
}

// TailJob will tail the logs for the job's pod and write them to our own stdout
func (s *Scheduler) TailJob(job *batchv1.Job) (chan bool, error) {
	stopChan := make(chan bool, 1)

	watcher, err := s.client.Core().Pods(s.Namespace()).Watch(v1.ListOptions{
		LabelSelector: fmt.Sprintf("job-name=%s", job.Name),
	})
	if err != nil {
		return nil, err
	}

	createdPods := make(map[string]bool)
	processedPods := make(map[string]bool)

	// We spin up a new routine that watched for events happening related to pods for the job
	go func() {
		event := watch.Event{}
		for {
			select {
			case <-stopChan:
				logrus.WithField("job", job.Name).Info("Stop tailing pods for job...")
				return
			case event = <-watcher.ResultChan():
				// We sometimes seem to receive events with a nil Object
				if event.Object == nil {
					continue
				}

				eventPod := event.Object.(*v1.Pod)

				// If this pod was already processed (either failed or succeeded) then discard the event
				if processedPods[eventPod.Name] {
					continue
				}

				if eventPod.Status.Phase == v1.PodRunning && !createdPods[eventPod.Name] {
					logrus.WithField("job", job.Name).WithField("pod", eventPod.Name).Info("New pod for job created")
					createdPods[eventPod.Name] = true
				}

				if eventPod.Status.Phase == v1.PodFailed || eventPod.Status.Phase == v1.PodSucceeded {
					// We keep a log of pods we have processed to ensure we only retrieve the logs for it once
					processedPods[eventPod.Name] = true

					// Now retrieve the actual logs
					logs := s.PodLogs(eventPod)

					if eventPod.Status.Phase == v1.PodFailed {
						logrus.WithField("logs", "\n"+logs).Error("Pod execution failed")
					} else {
						logrus.WithField("logs", "\n"+logs).Info("Pod execution succeeded")
					}
				}
			}
		}
	}()

	return stopChan, nil
}

// PodLogs retrieves the pods for a pod in kubernetes
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

// HasRunningJob checks if there is already a job running
func (s *Scheduler) HasRunningJob() bool {
	return s.running > 0
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
	err := yaml.Unmarshal([]byte(s.cfg.Template), &jobSpec.Template.Spec)
	if err != nil {
		logrus.Fatal(err)
	}

	jobSpec.Template.Spec.RestartPolicy = v1.RestartPolicyNever

	s.jobSpec = jobSpec

	return jobSpec
}
