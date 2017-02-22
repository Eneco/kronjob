package kronjob

import (
	"time"

	"k8s.io/client-go/pkg/api/v1"
	batchv1 "k8s.io/client-go/pkg/apis/batch/v1"
)

// Execution represents a single execution of a job
type Execution struct {
	Job           *batchv1.Job
	Pods          map[string]*v1.Pod
	ProcessedPods map[string]bool

	Failures  int32
	StartTime time.Time
	Cleaned   bool

	StopJobWatchCh chan error
	StopPodWatchCh chan error
}

// NewExecution returns a prepared Execution for a specified job
func NewExecution(job *batchv1.Job) *Execution {
	return &Execution{
		StartTime:      time.Now(),
		Failures:       0,
		Job:            job,
		Pods:           make(map[string]*v1.Pod),
		ProcessedPods:  make(map[string]bool),
		StopJobWatchCh: make(chan error, 1),
		StopPodWatchCh: make(chan error, 1),
	}
}

// Stop sends a signal to all stop channels
func (e *Execution) Stop(stopErr error) {
	e.StopPodWatchCh <- stopErr
	e.StopJobWatchCh <- stopErr
}
