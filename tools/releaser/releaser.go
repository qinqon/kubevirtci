package main

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/config"
	"github.com/go-git/go-git/v5/plumbing/transport/ssh"
	"github.com/google/go-github/v32/github"
	"github.com/otiai10/copy"
	"github.com/pkg/errors"
	"github.com/sirupsen/logrus"
	"golang.org/x/oauth2"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/wait"
	prowjobsapiv1 "k8s.io/test-infra/prow/apis/prowjobs/v1"
	prowjobsclientsetv1 "k8s.io/test-infra/prow/client/clientset/versioned/typed/prowjobs/v1"
	prowconfig "k8s.io/test-infra/prow/config"
	"k8s.io/test-infra/prow/pjutil"

	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"

	"github.com/google/go-containerregistry/pkg/authn"
	"github.com/google/go-containerregistry/pkg/name"
	"github.com/google/go-containerregistry/pkg/v1/remote"
)

type releaser struct {
	opts       options
	restConfig *rest.Config
	prowConfig *prowconfig.Config
	prowJobs   prowjobsclientsetv1.ProwJobInterface
}

func NewReleaser(opts options) (*releaser, error) {

	r := &releaser{
		opts: opts,
	}

	var err error
	if opts.kubeconfig != "" {
		r.restConfig, err = clientcmd.BuildConfigFromFlags("", opts.kubeconfig)
		if err != nil {
			return nil, errors.Wrapf(err, "failed instantiating K8s config from the given kubeconfig.")
		}
	} else {
		r.restConfig, err = rest.InClusterConfig()
		if err != nil {
			return nil, errors.Wrapf(err, "failed instantiating K8s config from the in cluster config.")
		}
	}
	prowClient, err := prowjobsclientsetv1.NewForConfig(r.restConfig)
	if err != nil {
		return nil, errors.Wrap(err, "failed instantiating a Prow client from the given kubeconfig.")
	}

	r.prowJobs = prowClient.ProwJobs(opts.jobsNamespace)

	r.prowConfig, err = prowconfig.Load(opts.configPath, opts.jobConfigPath)
	if err != nil {
		return nil, errors.Wrap(err, "failed loading prow configuration")
	}
	return r, nil
}

func (r *releaser) findPostsubmitConfig(name string) (prowconfig.Postsubmit, error) {
	for _, postsubmit := range r.prowConfig.JobConfig.PostsubmitsStatic["kubevirt/kubevirtci"] {
		if postsubmit.Name == name {
			return postsubmit, nil
		}
	}
	return prowconfig.Postsubmit{}, errors.Errorf("Could not find %s at postsubmit jobs configuration", name)
}

func (r *releaser) waitForProwJobCondition(name string, condition func(*prowjobsapiv1.ProwJob) (bool, error)) error {
	return wait.PollImmediate(5*time.Second, 2*time.Minute, func() (bool, error) {
		prowJob, err := r.prowJobs.Get(name, metav1.GetOptions{})
		if err != nil {
			return false, errors.Wrapf(err, "Failed getting prowjob to for a condition")
		}
		return condition(prowJob)
	})
}

func (r *releaser) createReleaseProviderJob(provider string) (*prowjobsapiv1.ProwJob, error) {
	providerReleaseJob := "release-" + provider
	selectedJobConfig, err := r.findPostsubmitConfig(providerReleaseJob)
	if err != nil {
		return nil, errors.Wrapf(err, "failed finding provider release job for %s", provider)
	}

	extraLabels := map[string]string{}
	extraAnnotations := map[string]string{}
	refs := prowjobsapiv1.Refs{
		Org:     "kubevirt",
		Repo:    "kubevirtci",
		BaseRef: r.opts.baseRef,
		BaseSHA: r.opts.baseSha,
	}
	postSubmitJob := pjutil.NewProwJob(pjutil.PostsubmitSpec(selectedJobConfig, refs), extraLabels, extraAnnotations)

	prowJob, err := r.prowJobs.Create(&postSubmitJob)
	if err != nil {
		return nil, errors.Wrap(err, "failed creating post submit job")
	}

	return prowJob, nil
}

func (r *releaser) releaseProviders() error {
	releaseProviderJobs := []*prowjobsapiv1.ProwJob{}
	for _, provider := range r.opts.providers {
		releaseProviderJob, err := r.createReleaseProviderJob(provider)
		if err != nil {
			return errors.Wrapf(err, "failed running release provider %s job", provider)
		}
		releaseProviderJobs = append(releaseProviderJobs, releaseProviderJob)
	}

	logrus.Info("Waitting for all the release jobs to finish")
	for _, releaseProviderJob := range releaseProviderJobs {
		err := r.waitForProwJobCondition(releaseProviderJob.Name, func(prowJobToCheck *prowjobsapiv1.ProwJob) (bool, error) {
			return prowJobToCheck.Complete(), nil
		})
		if err != nil {
			return errors.Wrap(err, "Job did not finish before timeout timeout")
		}
	}
	return nil
}

func (r *releaser) fetchProviderDigest(provider string) (string, error) {
	ref, err := name.ParseReference("docker.io/kubevirtci/" + provider)
	if err != nil {
		return "", errors.Wrapf(err, "failed parsing %s provider container URL", provider)
	}

	img, err := remote.Image(ref, remote.WithAuthFromKeychain(authn.DefaultKeychain))
	if err != nil {
		return "", errors.Wrapf(err, "failed retrieving %s provider container, provider")
	}

	digest, err := img.Digest()
	if err != nil {
		return "", errors.Wrapf(err, "failed parsing %s provider digest")

	}
	return fmt.Sprintf("%s:%s", digest.Algorithm, digest.Hex), nil
}

func (r *releaser) fetchProvidersDigest() (map[string]string, error) {
	digestByProvider := map[string]string{}
	for _, provider := range r.opts.providers {
		digest, err := r.fetchProviderDigest(provider)
		if err != nil {
			return nil, errors.Wrap(err, "failed fetching providers")
		}
		digestByProvider[provider] = digest
	}
	return digestByProvider, nil
}

func (r *releaser) buildCli(digestsByProvider map[string]string) (string, error) {
	makeArgs := []string{"-C", "../../cluster-provision/gocli/", "cli"}
	for provider, digest := range digestsByProvider {
		// Transform k8s-1.18 into something like K8S118SUFFIX
		suffixVarName := strings.ToUpper(strings.ReplaceAll(strings.ReplaceAll(provider, "-", ""), ".", "")) + "SUFFIX"
		makeArgs = append(makeArgs, fmt.Sprintf("%s=\"%s\"", suffixVarName, digest))
	}
	logrus.Infof("Running 'make %s'", strings.Join(makeArgs, " "))
	cmd := exec.Command("make", makeArgs...)
	stdoutStderr, err := cmd.CombinedOutput()
	if err != nil {
		return string(stdoutStderr), errors.Wrap(err, "failed calling make to build cli")
	}
	return string(stdoutStderr), nil
}

func (r *releaser) buildReleaseTarball(workingDir string) (string, error) {
	tarballWorkingDir := filepath.Join(workingDir, "kubevirtci")
	tarballPath := filepath.Join(tarballWorkingDir, "kubevirtci.tar.gz")

	copy.Copy(filepath.Join("..", "..", "cluster-up", "*"), tarballWorkingDir)
	copy.Copy(filepath.Join("..", "..", "cluster-provision", "gocli", "build", "cli"), tarballWorkingDir)
	createTarball(tarballPath, []string{tarballWorkingDir})
	return tarballPath, nil
}

func (r *releaser) tagRepository(repositoryPath string) (string, error) {
	// We instantiate a new repository targeting the given path (the .git folder)
	repo, err := git.PlainOpen(repositoryPath)
	if err != nil {
		return "", errors.Wrap(err, "failed opening kubevirtci repository")
	}

	repoHead, err := repo.Head()
	if err != nil {
		return "", errors.Wrap(err, "failed getting HEAD from kubevirtci repo")
	}

	// Use epoch as tag
	tagName := strconv.FormatInt(time.Now().Unix(), 10)

	_, err = repo.CreateTag(tagName, repoHead.Hash(), &git.CreateTagOptions{
		Message: tagName,
	})
	if err != nil {
		return "", errors.Wrap(err, "failed tagging HEAD at kubevirtci repo")
	}

	auth, err := ssh.NewPublicKeysFromFile("git", os.Getenv("GITHUB_SSH_KEY"), "")
	if err != nil {
		return "", errors.Wrap(err, "failed creating public key auth method")
	}

	po := &git.PushOptions{
		RemoteName: "qinqon",
		RefSpecs:   []config.RefSpec{config.RefSpec("refs/tags/*:refs/tags/*")},
		Auth:       auth,
	}
	err = repo.Push(po)
	if err != nil {
		return "", errors.Wrap(err, "failed pushing kubevirtci tag")
	}

	return tagName, nil
}

func (r *releaser) createGithubRelease(tag, releaseTraballPath string) error {
	body := "Follow the instruction at the tarball README to use kubevirtci"
	ts := oauth2.StaticTokenSource(
		&oauth2.Token{AccessToken: os.Getenv("GITHUB_TOKEN")},
	)
	tc := oauth2.NewClient(context.Background(), ts)
	client := github.NewClient(tc)
	_, _, err := client.Repositories.CreateRelease(context.Background(), "qinqon", "kubevirtci", &github.RepositoryRelease{
		TagName: &tag,
		Name:    &tag,
		Body:    &body,
	})
	if err != nil {
		return errors.Wrapf(err, "failed releasing kubevirtci tarball")
	}
	// list public repositories for org "github"
	return nil
}
