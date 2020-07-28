package main

import (
	"flag"
	"fmt"
	"os"

	"github.com/sirupsen/logrus"
)

type options struct {
	configPath    string
	jobConfigPath string
	baseRef       string
	baseSha       string
	kubeconfig    string
	jobsNamespace string
	providers     []string
}

func gatherOptions() *options {
	o := &options{}
	fs := flag.NewFlagSet(os.Args[0], flag.ExitOnError)
	fs.StringVar(&o.kubeconfig,
		"kubeconfig",
		"",
		"Path to kubeconfig. If empty, will try to use K8s defaults.")
	fs.StringVar(&o.jobsNamespace,
		"jobs-namespace",
		"",
		"The namespace in which Prow jobs should be created.")
	fs.StringVar(&o.configPath,
		"config-path",
		"",
		"Path to config.yaml.")
	fs.StringVar(&o.jobConfigPath,
		"job-config-path",
		"",
		"Path to prow job configs.")
	fs.StringVar(&o.baseRef,
		"base-ref",
		"master",
		"Git base ref under test")
	fs.StringVar(&o.baseSha,
		"base-sha",
		"",
		"Git base SHA under test")
	fs.Parse(os.Args[1:])
	return o
}

func (o *options) validate() {
	var errs []error
	if o.configPath == "" {
		errs = append(errs, fmt.Errorf("config-path can't be empty"))
	}
	if o.jobConfigPath == "" {
		errs = append(errs, fmt.Errorf("job-config-path can't be empty"))
	}
	if o.jobsNamespace == "" {
		errs = append(errs, fmt.Errorf("jobs-namespace can't be empty"))
	}
	if o.baseSha == "" {
		errs = append(errs, fmt.Errorf("base-sha can't be empty"))
	}
	if len(errs) > 0 {
		for _, err := range errs {
			logrus.WithError(err).Error("entry validation failure")
		}
		logrus.Fatalf("Arguments validation failed!")
	}
}
