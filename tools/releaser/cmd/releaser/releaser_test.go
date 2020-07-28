package main

import (
	"fmt"
	"io"
	"io/ioutil"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"time"

	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"

	"github.com/phayes/freeport"

	"github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing/object"

	"github.com/google/go-containerregistry/pkg/authn"
	"github.com/google/go-containerregistry/pkg/name"
	"github.com/google/go-containerregistry/pkg/v1/remote"

	"github.com/sosedoff/gitkit"

	"github.com/opencontainers/go-digest"
	"github.com/udhos/equalfile"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	prowjobsapiv1 "k8s.io/test-infra/prow/apis/prowjobs/v1"
	"k8s.io/test-infra/prow/client/clientset/versioned/fake"
	prowjobsclientsetv1 "k8s.io/test-infra/prow/client/clientset/versioned/typed/prowjobs/v1"
)

var _ = Describe("Releaser", func() {
	Context("when initialized with 10 seconds job timeout, project infra path and fake prow client", func() {
		var (
			r     releaser
			opts  *options
			prowc prowjobsclientsetv1.ProwV1Interface
		)
		BeforeEach(func() {
			opts = gatherOptions([]string{})
			opts.configPath = filepath.Join(projectInfraPath, "github/ci/prow/files/config.yaml")
			opts.jobConfigPath = filepath.Join(projectInfraPath, "github/ci/prow/files/jobs/kubevirt/kubevirtci/")
			opts.kubevirtciPath = filepath.Join("..", "..", "..", "..")
			opts.jobTimeout = 10 * time.Second
			prowc = fake.NewSimpleClientset().ProwV1()
			r = releaser{
				opts: opts,
			}
			Expect(r.initialize(prowc)).To(Succeed(), "should succeed initializing releaser wit fake prow client")
		})
		Context("and has no providers specified", func() {
			BeforeEach(func() {
				r.opts.providers = []string{}
			})
			It("should fail calling releaseProviders", func() {
				Expect(r.releaseProviders()).ToNot(Succeed(), "should not succeed if there is no providers")
			})
		})
		Context("and none of the providers has a release job", func() {
			BeforeEach(func() {
				r.opts.providers = []string{"fake-1.17", "fake-1.18"}
			})
			It("should fail calling releaseProviders", func() {
				Expect(r.releaseProviders()).ToNot(Succeed(), "should not succeed if there is no release job for the providers")
			})
		})
		Context("and all the providers has a release jobs", func() {
			launchReleaseProvidersAndExpectSucceed := func(shouldSucceed bool, done Done) {
				By("Launch releaseProviders in the background")
				go func() {
					defer GinkgoRecover()
					err := r.releaseProviders()
					if shouldSucceed {
						ExpectWithOffset(1, err).To(Succeed(), "should succeed running releaseProviders")
					} else {
						ExpectWithOffset(1, err).ToNot(Succeed(), "should not succeed running releaseProviders")
					}
					close(done)
				}()
			}

			waitForJobsCreation := func() {
				By("Wait for jobs to be created")
				EventuallyWithOffset(1, func() ([]prowjobsapiv1.ProwJob, error) {
					prowJobList, err := r.prowJobs.List(metav1.ListOptions{})
					if err != nil {
						return nil, err
					}
					return prowJobList.Items, nil
				}, 5*time.Second, 1*time.Second).Should(HaveLen(len(r.opts.providers)), "should contain the new created jobs for the providers")
				//TODO Check job label to check that they are the expected ones
			}

			markJobsToCompleteWithState := func(state prowjobsapiv1.ProwJobState) {
				By("Sleep 5 seconds to emulate prow running")
				time.Sleep(5 * time.Second)

				By(fmt.Sprintf("Set prow jobs status as completed and with %s state", state))
				prowJobList, err := r.prowJobs.List(metav1.ListOptions{})
				ExpectWithOffset(1, err).ToNot(HaveOccurred(), "should succeed listing jobs to update status")
				for _, prowJob := range prowJobList.Items {
					prowJob.SetComplete()
					prowJob.Status.State = state
					_, err := r.prowJobs.Update(&prowJob)
					ExpectWithOffset(1, err).ToNot(HaveOccurred(), "should succeed updating prow job")
				}
			}
			It("should create prow jobs and wait for prow jobs to successfully finish calling releaseProviders", func(done Done) {
				launchReleaseProvidersAndExpectSucceed(true, done)
				waitForJobsCreation()
				markJobsToCompleteWithState(prowjobsapiv1.SuccessState)
			}, 20)
			It("should create prow jobs and fail if some of the jobs finish with failure calling releaseProviders", func(done Done) {
				launchReleaseProvidersAndExpectSucceed(false, done)
				waitForJobsCreation()
				markJobsToCompleteWithState(prowjobsapiv1.FailureState)
			}, 20)
			It("should create prow jobs and fail if some of the job does not complete calling releaseProviders", func(done Done) {
				launchReleaseProvidersAndExpectSucceed(false, done)
				waitForJobsCreation()
			}, 20)

		})
		Context("and fetchProvidersDigest is run", func() {
			It("should return a map with valid providers digests", func() {
				digestByProvider, err := r.fetchProvidersDigest()
				Expect(err).ToNot(HaveOccurred(), "should succeed fetching digests")
				Expect(digestByProvider).To(HaveLen(len(r.opts.providers)))

				for _, provider := range r.opts.providers {
					Expect(digestByProvider).To(HaveKey(provider))
					containerName := filepath.Join(r.opts.containerRegistry, r.opts.containerOrg, provider+"@"+digestByProvider[provider])

					digest, err := name.NewDigest(containerName)
					Expect(err).ToNot(HaveOccurred(), "should succeed parsing the digest")

					_, err = remote.Image(digest, remote.WithAuthFromKeychain(authn.DefaultKeychain))
					Expect(err).ToNot(HaveOccurred(), "should succeed inspecting the image")
				}
			})
			Context("with providers not present at the registry", func() {
				BeforeEach(func() {
					r.opts.providers = []string{"fake-1.18", "fake-1.17"}
				})
				It("should fail", func() {
					_, err := r.fetchProvidersDigest()
					Expect(err).To(HaveOccurred(), "should fail fetching invalid providers digests")
				})
			})
		})
		Context("when buildCli is run with fake digests", func() {
			var (
				fakeDigests  = map[string]string{}
				buildCliPath string
			)
			BeforeEach(func() {
				for _, provider := range r.opts.providers {
					fakeDigests[provider] = string(digest.FromString(provider))
				}

				By("Building `cli` binary")
				output, err := r.buildCli(fakeDigests)
				Expect(err).ToNot(HaveOccurred(), fmt.Sprintf("should succeed building cli binary, output:\n%s", output))

				By("Retrieve digest by provider from binary running 'go run' and parsing output")
				buildCliPath = filepath.Join(r.opts.kubevirtciPath, "cluster-provision", "gocli", "build", "cli")

			})
			It("should build cli with specified provider digests", func() {
				cmd := exec.Command(buildCliPath, "run")
				cliRunOutput, _ := cmd.CombinedOutput()
				obtainedDigests := map[string]string{}
				for _, provider := range r.opts.providers {
					regex := *regexp.MustCompile(provider + `(.*)`)
					res := regex.FindStringSubmatch(string(cliRunOutput))
					obtainedDigests[provider] = res[1]
				}
				Expect(obtainedDigests).To(Equal(fakeDigests))
			})
			Context("and buildReleaseTarball is run after it", func() {
				var (
					workingDir  string
					tarballPath string
				)
				BeforeEach(func() {
					var err error
					workingDir, err = ioutil.TempDir("/tmp", "kubevirtci-release")
					Expect(err).ToNot(HaveOccurred(), "should succeed creating temp dir to store release tarball")

					tarballPath, err = r.buildReleaseTarball(workingDir)
					Expect(err).ToNot(HaveOccurred(), "should succeed creating release tarball")
				})
				AfterEach(func() {
					By(fmt.Sprintf("Removing working dir %s", workingDir))
					os.RemoveAll(workingDir)
				})
				It("should create a kubevirtci.tar.gz tarball with proper content", func() {
					Expect(tarballPath).To(BeARegularFile(), "should create the release tarball and be a regular file")
					extractPath := filepath.Join(workingDir, "extracted")
					Expect(extractTarball(tarballPath, extractPath)).To(Succeed(), "should succeed extracting release tarball")

					By("Comparing compiled and extracted cli")
					cmp := equalfile.New(nil, equalfile.Options{})
					cliIsEqual, err := cmp.CompareFile(buildCliPath, filepath.Join(extractPath, "cli"))
					Expect(err).ToNot(HaveOccurred(), "should succeed comparing 'cli' compiled (%s) and compressed (%s)")
					Expect(cliIsEqual).To(BeTrue(), "should contain the compiled `cli` binary")

					By("Comparing kubevirtci/cluster-up contents with extracted release tarball")
					clusterUpPath := filepath.Join(r.opts.kubevirtciPath, "cluster-up")
					err = filepath.Walk(clusterUpPath,
						func(filePath string, info os.FileInfo, err error) error {
							Expect(err).ToNot(HaveOccurred(), "should succeed walking cluster-up")

							if info.IsDir() {
								return nil
							}

							relFilePath, err := filepath.Rel(clusterUpPath, filePath)
							Expect(err).ToNot(HaveOccurred(), "should succeed composing cluster-up relative path to file")

							extractedFilePath := filepath.Join(extractPath, relFilePath)
							extractedFileIsEqual, err := cmp.CompareFile(filePath, extractedFilePath)
							Expect(err).ToNot(HaveOccurred(), "should succeed comparing extracted files")
							Expect(extractedFileIsEqual).To(BeTrue(), "should contain same file from cluster-up at the release tarball")
							return nil
						})
					Expect(err).ToNot(HaveOccurred())
				})

			})
		})
		//TODO: Test with http server
		Context("when tagRepository is run", func() {
			var (
				expectedTagName, workingDir, remoteRepoDir, cloneDir string
				gitServer                                            *gitkit.SSH
				clonedRepo, remoteRepo                               *git.Repository
				gitCfg                                               gitConfig
			)

			addCommit := func() {
				fileName := strconv.FormatInt(time.Now().UnixNano(), 10)
				w, err := remoteRepo.Worktree()
				Expect(err).ToNot(HaveOccurred())

				filename := filepath.Join(remoteRepoDir, fileName)
				err = ioutil.WriteFile(filename, []byte("hello world!"), 0644)
				Expect(err).ToNot(HaveOccurred())

				_, err = w.Add(fileName)
				Expect(err).ToNot(HaveOccurred())

				_, err = w.Commit("example go-git commit", &git.CommitOptions{
					Author: &object.Signature{
						Name:  "John Doe",
						Email: "john@doe.org",
						When:  time.Now(),
					},
				})
				Expect(err).ToNot(HaveOccurred())
			}

			findExpectedTag := func() bool {
				tags, err := remoteRepo.TagObjects()
				Expect(err).ToNot(HaveOccurred())

				found := false
				for {
					tag, err := tags.Next()
					if err == io.EOF {
						break
					}
					Expect(err).ToNot(HaveOccurred())
					if tag.Name == expectedTagName {
						found = true
						break
					}
				}
				return found
			}

			BeforeEach(func() {

				var err error
				workingDir, err = ioutil.TempDir("/tmp", "gitserver")
				Expect(err).ToNot(HaveOccurred(), "should succeed creating temp dir to store git repos")

				port, err := freeport.GetFreePort()
				Expect(err).ToNot(HaveOccurred(), "should succeed getting a free port")

				serverKeyDir := filepath.Join(workingDir, "keys")
				r.opts.githubServer = fmt.Sprintf("localhost:%d", port)
				r.opts.githubSSHKey = filepath.Join(serverKeyDir, "gitkit.rsa")

				serverRepoDir := filepath.Join(workingDir, "repo")

				By("Create a repository with a commit")
				remoteRepoDir = filepath.Join(serverRepoDir, r.opts.githubOrg, r.opts.githubRepo+".git")
				remoteRepo, err = git.PlainInit(remoteRepoDir, false)
				Expect(err).ToNot(HaveOccurred(), "should succeed initializing bare repository")
				addCommit()

				By("Start a gitkit ssh server using the created repo")
				gitServer = gitkit.NewSSH(gitkit.Config{
					Dir:    serverRepoDir,
					KeyDir: serverKeyDir,
				})
				Expect(gitServer.Listen(fmt.Sprintf(":%d", port))).To(Succeed(), "should succeed binding gitkit git ssh server")
				go func() {
					gitServer.Serve()
				}()

				// Wait a little for ssh server to start
				time.Sleep(1 * time.Second)

				gitCfg, err = r.composeGitConfig()
				Expect(err).ToNot(HaveOccurred(), "should succeed composing git auth method and repo URL")

				By("Clone the repo from the gitkit ssh server")
				// Releaser expect to have the repository already cloned

				cloneDir = filepath.Join(workingDir, "cloned")
				clonedRepo, err = git.PlainClone(cloneDir, false, &git.CloneOptions{
					URL:  gitCfg.url,
					Auth: gitCfg.auth,
				})
				Expect(err).ToNot(HaveOccurred(), "should succeed cloning the repo from ssh git sever")

				By("Call tagRepository over the cloned repo")
				expectedTagName, err = r.tagRepository(cloneDir)
				Expect(err).ToNot(HaveOccurred(), "should succeed tagging the repo")
			})
			AfterEach(func() {
				Expect(gitServer.Stop()).To(Succeed(), "should succeed stopping the git ssh server")
				os.RemoveAll(workingDir)
			})
			It("should tag remote repository at master HEAD with epoch", func() {
				Expect(expectedTagName).ToNot(BeEmpty(), "should return a non empty tag")
				Expect(findExpectedTag()).To(BeTrue(), "should tag the remote repository")
			})
			Context("and new commits are created and tagRepository is called again", func() {
				var (
					previousExpectedTagName string
				)
				BeforeEach(func() {
					previousExpectedTagName = expectedTagName
					By("Adding three new commits")
					addCommit()
					addCommit()
					addCommit()

					By("Pull new commits into the cloned repo")
					w, err := clonedRepo.Worktree()
					Expect(err).ToNot(HaveOccurred())
					Expect(w.Pull(&git.PullOptions{Auth: gitCfg.auth})).To(Succeed())

					By("Call tagRepository over the cloned repo")
					time.Sleep(2 * time.Second)
					expectedTagName, err = r.tagRepository(cloneDir)
					Expect(err).ToNot(HaveOccurred(), "should succeed tagging the repo")
				})
				It("should tag remote repository at master latest HEAD with epoch", func() {
					Expect(expectedTagName).ToNot(BeEmpty(), "should return a non empty tag")
					Expect(expectedTagName).ToNot(Equal(previousExpectedTagName), "should create different tags")
					Expect(findExpectedTag()).To(BeTrue(), "should tag the remote repository")
				})
			})
		})

		Context("when createGithubRelease is run", func() {
			It("should create a github release at specififed tag", func() {

			})
			Context("and assests are downloaded", func() {
				It("should contain the release tarball as one of the assets", func() {
					Fail("not implemented")
				})
			})
		})
	})
})
