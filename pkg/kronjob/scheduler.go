package kronjob

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/Sirupsen/logrus"
	"github.com/robfig/cron"
	"gopkg.in/yaml.v2"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/pkg/api/v1"
	batchv1 "k8s.io/client-go/pkg/apis/batch/v1"
	"k8s.io/client-go/pkg/watch"
)

type Scheduler struct {
	cfg       *Config
	client    *kubernetes.Clientset
	jobSpec   batchv1.JobSpec
	namespace string
	running   int
}

func NewScheduler(cfg *Config) (*Scheduler, error) {
	jobSpec := batchv1.JobSpec{
		Template: v1.PodTemplateSpec{},
	}
	err := yaml.Unmarshal([]byte(cfg.Template), &jobSpec.Template.Spec)
	if err != nil {
		return nil, err
	}

	switch strings.ToLower(cfg.RestartPolicy) {
	case "always":
		jobSpec.Template.Spec.RestartPolicy = v1.RestartPolicyAlways
	case "onfailure":
		jobSpec.Template.Spec.RestartPolicy = v1.RestartPolicyOnFailure
	case "never":
		jobSpec.Template.Spec.RestartPolicy = v1.RestartPolicyNever
	}

	client, err := GetKubeClient()
	if err != nil {
		return nil, err
	}

	// We begin by finding which namespace we are running in
	pods, err := client.Core().Pods("").List(v1.ListOptions{
		FieldSelector: fmt.Sprintf("metadata.name=%s", cfg.ContainerName),
	})
	if err != nil {
		return nil, err
	}
	if len(pods.Items) == 0 {
		return nil, errors.New("Failed to find pod namespace. Are you sure you are running kronjob inside a kubernetes container?")
	}

	logrus.
		WithField("template", jobSpec).
		WithField("namespace", pods.Items[0].Namespace).Info("Sucessfuly created scheduler")

	return &Scheduler{
		cfg:       cfg,
		client:    client,
		jobSpec:   jobSpec,
		namespace: pods.Items[0].Namespace,
	}, nil
}

func (s *Scheduler) Run() error {
	errChan := make(chan error)

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

	return nil
}

func (s *Scheduler) Exec() error {
	if !s.cfg.AllowParallel && s.running > 0 {
		return nil
	}

	name := fmt.Sprintf("%s-job-%d", s.cfg.ContainerName, time.Now().Unix())

	logrus.
		WithField("name", name).
		WithField("schedule", s.cfg.Schedule).
		WithField("jobSpec", s.jobSpec).
		Info("Start job")

	job, err := s.client.Batch().Jobs(s.namespace).Create(&batchv1.Job{
		ObjectMeta: v1.ObjectMeta{
			Name: name,
		},
		Spec: s.jobSpec,
	})

	logrus.WithField("job", name).Info("Started job")

	watcher, err := s.client.Batch().Jobs(s.namespace).Watch(v1.ListOptions{
		FieldSelector: fmt.Sprintf("metadata.name=%s", name),
	})
	if err != nil {
		return err
	}

	s.running = s.running + 1

	event := watch.Event{}
	for {
		select {
		case event = <-watcher.ResultChan():
			latestJob := event.Object.(*batchv1.Job)
			if latestJob.Status.Succeeded == 1 {
				logrus.WithField("job", name).Info("Completed job...")

				pods, err := s.client.Core().Pods(s.namespace).List(v1.ListOptions{
					LabelSelector: fmt.Sprintf("job-name=%s", name),
				})
				if err != nil {
					return err
				}

				for _, pod := range pods.Items {
					logrus.WithField("pod", pod.Name).Info("Deleting pod...")
					err := s.client.Core().Pods(s.namespace).Delete(pod.Name, v1.NewDeleteOptions(0))
					if err != nil {
						logrus.WithField("pod", pod.Name).Warn("Unable to clean pod!")
					}
				}

				logrus.WithField("job", name).Info("Deleting job...")
				_ = s.client.Batch().Jobs(s.namespace).Delete(name, v1.NewDeleteOptions(0))
				if err != nil {
					logrus.WithField("error", err).WithField("job", job).Warn("Unable to clean up job!")
				}

				s.running = s.running - 1
				return nil
			}
		}
	}
}
