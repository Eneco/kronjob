# kronjob

Kronjob takes a job template and a cron schedule. It creates, manages, watches and afterwards cleans
jobs in kubernetes according to the schedule. It also aggregates the logs of the pods running the job
into the single container where kronjob runs.

 - [Introduction](#introduction)
 - [Installation](#installation)
 - [Example Usage](#usage)
 - [Contributing](#contributing)

## Introduction

### Why

 - Kubernetes ScheduledJob/CronJob is still in alpha/beta and currently seem to cause instability in kubernetes clusters
 - ScheduledJob/CronJob does not remove finished pods/jobs for you, requiring manual scripts to do this for you
 - Logs for a CronJob/ScheduledJob are scattered accross different pods

### How

 - Have a container running Kronjob in your cluster for each job you want to run on a schedule
 - Create a job on the assigned schedule by talking directly to the Kubernetes API using in-cluster authentication
 - Keep track of created pods performing the job and aggregate their logs into the Kronjob container
 - Remove any completed and failed pods, as well as the jobs after they have finished

### What

 - A kubernetes deployment configures your kronjob with a schedule and job template
 - Kronjob, a Go binary, runs in a pod and talks to the Kubernetes API

## Installation

### Using the Docker image

The most common way to use Kronjob is by using the publicly available docker image `eneco/kronjob:latest`.
Note that Kronjob is meant to be run from within a Kubernetes cluster, usually by creating a deployment
with one replica to keep a pod running the kronjob.

### From source
You can alo build Kronjob from source. Again note that Kronjob is meant to be run from within a Kubernetes cluster
so the usefulness of building it from source is limited.

You must have a working Go environment with [glide](https://github.com/Masterminds/glide) and `Make` installed.

From a terminal:
```shell
  cd $GOPATH && mkdir -p src/github.com/eneco/ && cd !$
  git clone https://github.com/Eneco/kronjob.git
  cd kronjob
  make bootstrap build
```

Outputs the landscaper binary in `./build/kronjob`.

## Example Usage

This is an example of a kubernetes resource running a Kronjob. Note that the template
itself is yaml in an environment variable, thus requiring the `|-` syntax.

Another thing of note is the `@every 30s` as a schedule value. The Go cron library
we are using adds support for these convenient configuration. To see what is possible,
please refer to the library's [documentation](https://godoc.org/gopkg.in/robfig/cron.v2).

```yaml
apiVersion: extensions/v1beta1
kind: Deployment
metadata:
  name: kronjob-randomfail
  labels:
    app: kronjob-randomfail
spec:
  replicas: 1
  template:
    metadata:
      labels:
        app: kronjob-randomfail
    spec:
      containers:
      - name: kronjob-randomfail
        image: eneco/kronjob:latest
        env:
        - name: SCHEDULE
          value: "@every 30s"
        - name: TEMPLATE
          value: |-
            containers:
            - name: myjob
              image: alpine
              imagePullPolicy: Always
              command:
                - /bin/sh
                - -c
              args:
                - "echo hello world; sleep 5; EXITCODE=`shuf -i 0-2 -n 1` && exit `expr $EXITCODE - 1`"
```

Here is an example of what the logs of a randomly failing kronjob look like:

![Image of kronjob output](https://github.com/Eneco/kronjob/blob/master/example-output.png)

## Contributing

We'd love to accept your contributions! Please use GitHub pull requests: fork the repo, develop and test your code, submit a pull request. Thanks!
