// Copyright 2020 Google LLC
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     https://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

// Package git provides functions for interacting with Git.
package git

import (
	"context"
	"encoding/hex"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"strings"
	"time"
)

const (
	gitTimeout = time.Minute * 15 // timeout for a git operation
)

// Git provides functions for interacting with git
type Git struct {
	exe string
}

// New looks up the git exectable and returns a new Git
func New() (*Git, error) {
	path, err := exec.LookPath("git")
	if err != nil {
		return nil, fmt.Errorf("Couldn't find path to git executable")
	}
	return &Git{path}, nil
}

// Hash is a 20 byte, git object hash.
type Hash [20]byte

func (h Hash) String() string { return hex.EncodeToString(h[:]) }

// ParseHash returns a Hash from a hexadecimal string.
func ParseHash(s string) Hash {
	b, _ := hex.DecodeString(s)
	h := Hash{}
	copy(h[:], b)
	return h
}

// Add calls 'git add <file>'.
func (g Git) Add(wd, file string) error {
	if _, err := shell(gitTimeout, g.exe, wd, "add", file); err != nil {
		return fmt.Errorf("`git add %v` in working directory %v failed: %w", file, wd, err)
	}
	return nil
}

// CommitFlags advanced flags for Commit
type CommitFlags struct {
	Name  string // Used for author and committer
	Email string // Used for author and committer
}

// Commit calls 'git commit -m <msg> --author <author>'.
func (g Git) Commit(wd, msg string, flags CommitFlags) error {
	args := []string{}
	if flags.Name != "" {
		args = append(args, "-c", "user.name="+flags.Name)
	}
	if flags.Email != "" {
		args = append(args, "-c", "user.email="+flags.Email)
	}
	args = append(args, "commit", "-m", msg)
	_, err := shell(gitTimeout, g.exe, wd, args...)
	return err
}

// PushFlags advanced flags for pushing changes, tags.
type PushFlags struct {
	Username string // Used for authentication when uploading
	Password string // Used for authentication when uploading
}

func (f PushFlags) addCredentials(remote string) (string, error) {
	if f.Username != "" {
		u, err := url.Parse(remote)
		if err != nil {
			return "", fmt.Errorf("Couldn't parse remote URL: %w", err)
		}
		u.User = url.UserPassword(f.Username, f.Password)
		remote = u.String()
	}
	return remote, nil
}

// Push pushes the local branch to remote.
func (g Git) Push(wd, remote, localBranch, remoteBranch string, flags PushFlags) error {
	remote, err := flags.addCredentials(remote)
	if err != nil {
		return err
	}
	_, err = shell(gitTimeout, g.exe, wd, "push", remote, localBranch+":refs/heads/"+remoteBranch)
	return err
}

// PushTags pushes all local tags to remote.
func (g Git) PushTags(wd, remote string, flags PushFlags) error {
	remote, err := flags.addCredentials(remote)
	if err != nil {
		return err
	}
	_, err = shell(gitTimeout, g.exe, wd, "push", remote, "--tags")
	return err
}

// CheckoutRemoteBranch performs a git fetch and checkout of the given branch into path.
func (g Git) CheckoutRemoteBranch(path, url string, branch string) error {
	if err := os.MkdirAll(path, 0777); err != nil {
		return fmt.Errorf("mkdir '%v' failed: %w", path, err)
	}

	for _, cmds := range [][]string{
		{"init"},
		{"fetch", url, branch},
		{"checkout", "FETCH_HEAD"},
	} {
		if _, err := shell(gitTimeout, g.exe, path, cmds...); err != nil {
			os.RemoveAll(path)
			return err
		}
	}

	return nil
}

// CheckoutRemoteCommit performs a git fetch and checkout of the given commit into path.
func (g Git) CheckoutRemoteCommit(path, url string, commit Hash) error {
	if err := os.MkdirAll(path, 0777); err != nil {
		return fmt.Errorf("mkdir '%v' failed: %w", path, err)
	}

	for _, cmds := range [][]string{
		{"init"},
		{"fetch", url, commit.String()},
		{"checkout", "FETCH_HEAD"},
	} {
		if _, err := shell(gitTimeout, g.exe, path, cmds...); err != nil {
			os.RemoveAll(path)
			return err
		}
	}

	return nil
}

// Tag creates a git tag for the given hash.
func (g Git) Tag(path, name string, at Hash) error {
	if _, err := shell(gitTimeout, g.exe, path, "tag", name, at.String()); err != nil {
		return err
	}
	return nil
}

// Rebase performs a git rebase of the current branch onto to.
func (g Git) Rebase(path string, to Hash) error {
	if _, err := shell(gitTimeout, g.exe, path, "rebase", to.String()); err != nil {
		return err
	}
	return nil
}

// CheckoutCommit performs a git checkout of the given commit.
func (g Git) CheckoutCommit(path string, commit Hash) error {
	_, err := shell(gitTimeout, g.exe, path, "checkout", commit.String())
	return err
}

// Apply applys the patch file to the git repo at dir.
func (g Git) Apply(dir, patch string) error {
	_, err := shell(gitTimeout, g.exe, dir, "apply", patch)
	return err
}

// FetchRefHash returns the git hash of the given ref.
func (g Git) FetchRefHash(ref, url string) (Hash, error) {
	out, err := shell(gitTimeout, g.exe, "", "ls-remote", url, ref)
	if err != nil {
		return Hash{}, err
	}
	return ParseHash(string(out)), nil
}

type ChangeList struct {
	Hash        Hash
	Date        time.Time
	Author      string
	Subject     string
	Description string
}

// Log returns the top count ChangeLists at HEAD, starting with the most recent.
func (g Git) Log(wd, path string, count int) ([]ChangeList, error) {
	return g.LogFrom(wd, path, "HEAD", count)
}

// LogFrom returns the top count ChangeList starting from at, starting with the
// most recent.
func (g Git) LogFrom(wd, path, at string, count int) ([]ChangeList, error) {
	if at == "" {
		at = "HEAD"
	}
	args := []string{"log", at, "--pretty=format:" + prettyFormat}
	if count > 0 {
		args = append(args, fmt.Sprintf("-%d", count))
	}
	args = append(args, path)
	out, err := shell(gitTimeout, g.exe, wd, args...)
	if err != nil {
		return nil, err
	}
	return parseLog(string(out)), nil
}

// Parent returns the parent ChangeList for cl.
func (g Git) Parent(cl ChangeList) (ChangeList, error) {
	out, err := shell(gitTimeout, g.exe, "", "log", "--pretty=format:"+prettyFormat, fmt.Sprintf("%v^", cl.Hash))
	if err != nil {
		return ChangeList{}, err
	}
	cls := parseLog(string(out))
	if len(cls) == 0 {
		return ChangeList{}, fmt.Errorf("Unexpected output")
	}
	return cls[0], nil
}

// HeadCL returns the HEAD ChangeList.
func (g Git) HeadCL(wd string) (ChangeList, error) {
	cls, err := g.LogFrom(wd, wd, "HEAD", 1)
	if err != nil {
		return ChangeList{}, err
	}
	if len(cls) == 0 {
		return ChangeList{}, fmt.Errorf("No commits found")
	}
	return cls[0], nil
}

// Show content of the file at path for the given commit/tag/branch.
func (g Git) Show(wd, path, at string) ([]byte, error) {
	return shell(gitTimeout, g.exe, wd, "show", at+":"+path)
}

const prettyFormat = "ǁ%Hǀ%cIǀ%an <%ae>ǀ%sǀ%b"

func parseLog(str string) []ChangeList {
	msgs := strings.Split(str, "ǁ")
	cls := make([]ChangeList, 0, len(msgs))
	for _, s := range msgs {
		if parts := strings.Split(s, "ǀ"); len(parts) == 5 {
			cl := ChangeList{
				Hash:        ParseHash(parts[0]),
				Author:      strings.TrimSpace(parts[2]),
				Subject:     strings.TrimSpace(parts[3]),
				Description: strings.TrimSpace(parts[4]),
			}
			date, err := time.Parse(time.RFC3339, parts[1])
			if err != nil {
				panic(err)
			}
			cl.Date = date

			cls = append(cls, cl)
		}
	}
	return cls
}

// shell runs the executable exe with the given arguments, in the working
// directory wd, with the given timeout.
func shell(timeout time.Duration, exe, wd string, args ...string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, exe, args...)
	cmd.Dir = wd

	out, err := cmd.Output()
	switch err := err.(type) {
	case nil:
		return out, nil
	case *exec.ExitError:
		return nil, fmt.Errorf("%v returned with %w\nstderr: %v\nstdout: %v", exe, err, string(err.Stderr), string(out))
	default:
		return nil, fmt.Errorf("%v returned with %w\nstdout: %v", exe, err, string(out))
	}
}
