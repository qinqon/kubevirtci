package main

import (
	"fmt"
	"io/ioutil"
	"os"

	"github.com/go-git/go-git/v5/plumbing/transport"
	gogithttp "github.com/go-git/go-git/v5/plumbing/transport/http"
	gogitssh "github.com/go-git/go-git/v5/plumbing/transport/ssh"
	"github.com/pkg/errors"
	"golang.org/x/crypto/ssh"
)

type gitConfig struct {
	auth transport.AuthMethod
	url  string
}

func (r *releaser) composeGitSSHAuth() (transport.AuthMethod, error) {
	auth, err := gogitssh.NewPublicKeysFromFile("git", r.opts.githubSSHKey, "")
	if err != nil {
		return nil, errors.Wrap(err, "failed composing new ssh key for git client")
	}
	auth.HostKeyCallback = ssh.InsecureIgnoreHostKey()
	return auth, nil
}

func (r *releaser) composeGitSSHRepoURL() string {
	return fmt.Sprintf("ssh://git@%s/%s/%s.git", r.opts.githubServer, r.opts.githubOrg, r.opts.githubRepo)
}

func (r *releaser) composeGitHTTPTokenAuth() (transport.AuthMethod, error) {
	f, err := os.Open(r.opts.githubToken)
	if err != nil {
		return nil, errors.Wrap(err, "failed opening github token file")
	}
	token, err := ioutil.ReadAll(f)
	if err != nil {
		return nil, errors.Wrap(err, "failed reading github token file")
	}
	auth := gogithttp.TokenAuth{Token: string(token)}
	return &auth, nil
}

func (r *releaser) composeGitHTTPRepoURL() string {
	return fmt.Sprintf("https://%s/%s/%s.git", r.opts.githubServer, r.opts.githubOrg, r.opts.githubRepo)
}

func (r *releaser) composeGitConfig() (gitConfig, error) {
	var composeGitAuth func(*releaser) (transport.AuthMethod, error)
	var composeGitRepoURL func(*releaser) string
	if r.opts.githubSSHKey != "" {
		composeGitAuth = (*releaser).composeGitSSHAuth
		composeGitRepoURL = (*releaser).composeGitSSHRepoURL
	} else {
		composeGitAuth = (*releaser).composeGitHTTPTokenAuth
		composeGitRepoURL = (*releaser).composeGitHTTPRepoURL
	}
	auth, err := composeGitAuth(r)
	if err != nil {
		return gitConfig{}, errors.Wrap(err, "failed composing git auth")
	}
	return gitConfig{
		auth: auth,
		url:  composeGitRepoURL(r),
	}, nil
}
