package main

import (
	"os"
	"time"

	"github.com/sirupsen/logrus"
)

func main() {

	opts := gatherOptions()

	opts.validate()

	//TODO: New argument ?
	opts.providers = []string{
		"k8s-1.14",
		"k8s-1.15",
		"k8s-1.16",
		"k8s-1.17",
		"k8s-1.18",
	}

	_ = setupLogger()

	r, err := NewReleaser(*opts)
	mustSucceed(err, "Could not initialize releaser")

	//err = r.releaseProviders()
	//mustSucceed(err, "Could not release providers")

	digestsByProvider, err := r.fetchProvidersDigest()
	mustSucceed(err, "Could not fetch provider's digest")

	buildCliOutput, err := r.buildCli(digestsByProvider)
	mustSucceed(err, buildCliOutput)

	logrus.Info(buildCliOutput)
}

func mustSucceed(err error, message string) {
	if err != nil {
		logrus.WithError(err).Fatal(message)
	}
}

func setupLogger() *logrus.Logger {
	l := logrus.New()
	l.SetFormatter(&logrus.TextFormatter{FullTimestamp: true, TimestampFormat: time.RFC1123Z})
	l.SetLevel(logrus.TraceLevel)
	l.SetOutput(os.Stdout)
	return l
}
