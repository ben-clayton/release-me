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

// release-me is a tool to maintain semantically versioned release branches and
// tags for GitHub repos.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io/ioutil"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/ben-clayton/release-me/changes"
	"github.com/ben-clayton/release-me/git"
	"github.com/ben-clayton/release-me/semver"
	"github.com/ben-clayton/release-me/ui"
	"github.com/google/go-github/v32/github"
	"golang.org/x/oauth2"
)

var (
	errNoChangesFile = fmt.Errorf("No changes file found")
	errGitNotFound   = fmt.Errorf("The git executable was not found on PATH")
	errRepoNotFound  = fmt.Errorf("Repo not found")
	errRestartFlow   = fmt.Errorf("Restart project flow")
)

////////////////////////////////////////////////////////////////////////////////
// main() / run()
////////////////////////////////////////////////////////////////////////////////

func main() {
	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "%s\n", err)
		os.Exit(1)
	}
}

func run() error {
	owner := flag.String("owner", "", "GitHub project organization")
	repo := flag.String("repo", "", "GitHub repository name")
	username := flag.String("user", "", "GitHub username name")
	accesstoken := flag.String("token", "", "GitHub access token")
	flag.Parse()

	ui := ui.New()
	defer ui.Terminate()

	g, err := git.New()
	if err != nil {
		ui.ShowMessage("git not found", errGitNotFound.Error())
		return errGitNotFound
	}

	a := app{
		credPath: "~/.config/release-me/credentials",
		git:      g,
		cmdFlags: cmdFlags{
			repoOwner: *owner,
			repoName:  *repo,
		},
		cred: credentials{
			Username:    *username,
			AccessToken: *accesstoken,
		},
		ui: ui,
	}

	if home, err := os.UserHomeDir(); err == nil {
		a.credPath = strings.ReplaceAll(a.credPath, "~", home)
	}
	a.cred.load(a.credPath)

	return a.flowRoot(context.Background())
}

////////////////////////////////////////////////////////////////////////////////
// app
////////////////////////////////////////////////////////////////////////////////

// app holds the main release-me application configuration and core types.
type app struct {
	cmdFlags cmdFlags
	git      *git.Git
	cred     credentials
	credPath string
	ui       ui.UI
}

type cmdFlags struct {
	repoOwner string
	repoName  string
}

// flowRoot performs the root application logic and UI flow:
// - Ensures that the GitHub credentials are correct.
// - Obtains the list of writable repos available to the user.
// - If there's more than a single repo available, asks the user to select one.
// - Then proceeds to the repo UI flow.
func (a app) flowRoot(ctx context.Context) error {
	// Do we have any existing credentials? If not, ask the user for them.
	askedForCredentials := false
	if a.cred.Username == "" || a.cred.AccessToken == "" {
		if err := a.cred.getFromUser(a.ui, "Specify GitHub credentials "+
			"(generate a access token at https://github.com/settings/tokens):"); err != nil {
			return err
		}
		askedForCredentials = true
	}

	var c *github.Client
	var repos []repo

	// Loop for checking GitHub credentials, and retrieving list of repos.
	for c == nil {
		ts := oauth2.StaticTokenSource(
			&oauth2.Token{AccessToken: a.cred.AccessToken},
		)
		tc := oauth2.NewClient(ctx, ts)
		c = github.NewClient(tc)
		err := a.ui.WithStatus("Fetching repositories...", func(ui.Status) error {
			l, _, err := c.Repositories.List(ctx, "", &github.RepositoryListOptions{})
			if err != nil {
				askedForCredentials = true
				return a.cred.getFromUser(a.ui, "GitHub credentials incorrect")
			}

			repos = make([]repo, len(l))
			for i, r := range l {
				parts := strings.Split(r.GetFullName(), "/")
				repos[i] = repo{
					owner: parts[0],
					name:  parts[1],
					url:   r.GetCloneURL(),
				}
			}
			return nil
		})
		if err != nil {
			return err
		}
	}

	if askedForCredentials {
		a.cred.askToSave(a.ui, a.credPath)
	}

	if len(repos) == 0 {
		a.ui.ShowMessage("No repositories found", "release-me requires a repository to work with.")
		return errRepoNotFound
	}

	// Next filter the repos down to those that the user has write access to,
	// and those that match the optional command line arguments.

	filterRepos := func(pred func(r repo) bool) {
		filtered := make([]repo, 0, len(repos))
		for _, r := range repos {
			if pred(r) {
				filtered = append(filtered, r)
			}
		}
		repos = filtered
	}

	if err := a.ui.WithStatus("Filtering repositories....", func(ui.Status) error {
		if a.cmdFlags.repoOwner != "" {
			filterRepos(func(r repo) bool { return r.owner == a.cmdFlags.repoOwner })
			if len(repos) == 0 {
				a.ui.ShowMessage("No repository found", "No repositories found with the owner '%v'", a.cmdFlags.repoOwner)
				return errRepoNotFound
			}
		}
		if a.cmdFlags.repoName != "" {
			filterRepos(func(r repo) bool { return r.name == a.cmdFlags.repoName })
			if len(repos) == 0 {
				a.ui.ShowMessage("No repository found", "No repositories found with the name '%v'", a.cmdFlags.repoName)
				return errRepoNotFound
			}
		}
		filterRepos(func(r repo) bool {
			p, _, err := c.Repositories.GetPermissionLevel(ctx, r.owner, r.name, a.cred.Username)
			if err != nil {
				return false
			}
			switch p.GetPermission() {
			case "admin", "write":
				return true
			}
			return false
		})
		if len(repos) == 0 {
			a.ui.ShowMessage("No repository found", "No writable repositories found")
			return errRepoNotFound
		}
		return nil
	}); err != nil {
		return err
	}

	// Now filtered, if we have more than one repo, ask the user to select one,
	// otherwise just pick the one we have.
	r := repos[0]
	if len(repos) > 1 {
		options := make([]string, len(repos))
		for i, r := range repos {
			options[i] = fmt.Sprintf("%v/%v", r.owner, r.name)
		}
		i, err := a.ui.ShowMenu("Select project", options)
		if err != nil {
			return nil
		}
		r = repos[i]
	}

	// Proceed to the repo UI flow...
	return a.ui.Enter(fmt.Sprintf("%v/%v", r.owner, r.name), func() error {
		for true {
			err := a.flowRepo(ctx, r, c)
			if err == errRestartFlow {
				continue
			}
			return err
		}
		panic("unreachable")
	})
}

// flowRepo performs the logic and UI flow for the repo r:
// - Retrieves the list of all branches and tags for the repo, along with
//   CHANGES file content for each branch and tag.
// - Determines the version style in use (1.2.3, release-1.2.3, v1.2, etc)
// - Checks for issues with the CHANGES content, missing release branches and
//   tags.
// - If any tags or branches are missing, asks the user whether they should be
//   automatically created.
// - Displays the repo menu, asking the user whether they'd like to perform a
//   new release (proceeds to flowReleaseMenu() if selected).
func (a app) flowRepo(ctx context.Context, r repo, c *github.Client) error {
	if err := r.fetchBranches(ctx, a.ui, c); err != nil {
		return fmt.Errorf("Failed to fetch branches: %w", err)
	}
	if err := r.fetchTags(ctx, a.ui, c); err != nil {
		return fmt.Errorf("Failed to fetch tags: %w", err)
	}
	if err := r.fetchReleases(ctx, a.ui, c); err != nil {
		return fmt.Errorf("Failed to fetch releases: %w", err)
	}

	r.determineVersionStyle()

	problems, err := r.validate(ctx, a.ui)
	if err != nil {
		return fmt.Errorf("Failed to validate changes: %w", err)
	}

	if len(problems) > 0 {
		ok, err := a.ui.ShowConfirmation(fmt.Sprintf("%d problems found", len(problems)), strings.Join(problems, "\n"), "Continue anyway")
		if !ok || err != nil {
			return err
		}
	}

	if len(r.missingTags) > 0 || len(r.missingBranches) > 0 || len(r.missingReleases) > 0 {
		types := []string{}
		if len(r.missingBranches) > 0 {
			types = append(types, "branches")
		}
		if len(r.missingTags) > 0 {
			types = append(types, "tags")
		}
		if len(r.missingReleases) > 0 {
			types = append(types, "releases")
		}

		missing := make([]string, 0, len(r.missingTags)+len(r.missingBranches)+len(r.missingReleases))
		for _, v := range r.missingBranches.List() {
			missing = append(missing, fmt.Sprintf("Release branch '%v' for release %v", r.branchNameForVersion(v), v))
		}
		for _, v := range r.missingTags.List() {
			missing = append(missing, fmt.Sprintf("Release tag '%v'", r.tagNameForVersion(v)))
		}
		for _, v := range r.missingReleases.List() {
			missing = append(missing, fmt.Sprintf("Release '%v'", r.releaseNameForVersion(v)))
		}
		ok, err := a.ui.ShowConfirmation("Missing release "+strings.Join(types, " and ")+" found:",
			strings.Join(missing, "\n"), "Would you like to create these now?")
		if err != nil {
			return err
		}
		if ok {
			var numCreatedBranches, numCreatedTags, numCreatedReleases int
			var errs []error
			if len(r.missingBranches) > 0 || len(r.missingTags) > 0 {
				nb, nt, e := createMissingBranchesAndTags(r, a.ui, a.git, a.cred)
				numCreatedBranches, numCreatedTags = nb, nt
				errs = append(errs, e...)

				// Re-scan branches and tags to reflect updates.
				if err := r.fetchBranches(ctx, a.ui, c); err != nil {
					return fmt.Errorf("Failed to fetch branches: %w", err)
				}
				if err := r.fetchTags(ctx, a.ui, c); err != nil {
					return fmt.Errorf("Failed to fetch tags: %w", err)
				}
			}
			if len(r.missingReleases) > 0 && len(errs) == 0 {
				n, e := createMissingReleases(ctx, r, a.ui, c)
				numCreatedReleases = n
				errs = append(errs, e...)
			}

			title := fmt.Sprintf("Created %v branches, %v tags and %v releases with %v errors",
				numCreatedBranches, numCreatedTags, numCreatedReleases, len(errs))
			body := []string{}
			for _, err := range errs {
				body = append(body, err.Error())
			}
			if c := len(r.missingBranches); c > 0 {
				body = append(body, fmt.Sprintf("There are still %d release branches missing", c))
			}
			if c := len(r.missingTags); c > 0 {
				body = append(body, fmt.Sprintf("There are still %d release tags missing", c))
			}
			if c := len(r.missingReleases); c > 0 {
				body = append(body, fmt.Sprintf("There are still %d releases missing", c))
			}
			a.ui.ShowMessage(title, strings.Join(body, "\n"))
			return errRestartFlow
		}
	}

	const (
		optCreateRelease = "New release"
		optQuit          = "Quit"
	)

	options := []string{optCreateRelease, optQuit}
	selection, err := a.ui.ShowMenu("Select action", options)
	if err != nil {
		return err
	}

	switch options[selection] {
	case optCreateRelease:
		return a.flowReleaseMenu(ctx, r, c)
	case optQuit:
		return nil
	}
	return nil
}

// flowReleaseMenu performs the logic and UI to create a new release for the
// repo r:
// - Asks the user for the main branch to release from, along with the release
//   version.
// - Calls doRelease() to perform the actual release.
func (a app) flowReleaseMenu(ctx context.Context, r repo, c *github.Client) error {
	return a.ui.Enter("Create release", func() error {
		mainBranchName := ""
		releaseVer := semver.Version{}
		if main := r.mainBranch; main != nil {
			mainBranchName = r.mainBranch.name
			releaseVer = r.mainBranch.changes.CurrentVersion()
			releaseVer.Flavor = ""
		}
		versionStr := releaseVer.String()
		if err := a.ui.ShowForm("Create new release", []ui.TextField{
			{
				Name:  "Main branch",
				Value: &mainBranchName,
				Validate: func(s string) error {
					if _, ok := r.branches[s]; !ok {
						return fmt.Errorf("Unknown branch '%v'", s)
					}
					return nil
				},
			}, {
				Name:  "Version",
				Value: &versionStr,
				Validate: func(s string) error {
					v, err := semver.Parse(s)
					if err != nil {
						return err
					}
					if !v.GreaterEqualTo(releaseVer, false) {
						return fmt.Errorf("Version must be greater or equal to %v", releaseVer)
					}
					return nil
				},
			},
		}); err != nil {
			return err
		}
		b, ok := r.branches[mainBranchName]
		if !ok {
			return fmt.Errorf("Branch '%v' not found", mainBranchName)
		}
		v, err := semver.Parse(versionStr)
		if err != nil {
			return err
		}
		if err := doRelease(ctx, r, a.ui, a.git, c, b, v, a.cred); err != nil {
			return err
		}
		return nil
	})
}

// saveAndCommit saves the file content to path, performs a `git add`,
// followed by `git commit` using the given commit message, returning the new
// change's git hash.
func saveAndCommit(g *git.Git, path string, content string, msg string) (git.Hash, error) {
	wd := filepath.Dir(path)

	// Save new CHANGES file
	if err := ioutil.WriteFile(path, []byte(content), 0666); err != nil {
		return git.Hash{}, fmt.Errorf("Failed to save file '%v': %v", path, err)
	}

	// git add
	if err := g.Add(wd, path); err != nil {
		return git.Hash{}, fmt.Errorf("Failed to stage '%v': %v", path, err)
	}

	// git commit
	if err := g.Commit(wd, msg, git.CommitFlags{}); err != nil {
		return git.Hash{}, fmt.Errorf("Failed to commit changes to '%v': %v", path, err)
	}

	head, err := g.HeadCL(wd)
	if err != nil {
		return git.Hash{}, fmt.Errorf("Failed to get HEAD: %v", err)
	}

	return head.Hash, nil
}

// createMissingBranchesAndTags checks out the repo r to a temporary directory,
// scans the CHANGES file for all missing release branches and tags, building
// each and pushing them to the repo r.
func createMissingBranchesAndTags(r repo, u ui.UI, g *git.Git, cred credentials) (numCreatedBranches int, numCreatedTags int, errs []error) {
	err := u.Enter("Create missing", func() error {
		if r.mainBranch == nil {
			return fmt.Errorf("Couldn't identifiy main branch")
		}

		wd := filepath.Join(os.TempDir(), "release-me", r.owner, r.name)
		if err := os.MkdirAll(wd, 0777); err != nil {
			return fmt.Errorf("Failed to create temporary checkout directory at '%v'", wd)
		}
		defer os.RemoveAll(wd)

		if err := u.WithStatus("Checking out repository...", func(ui.Status) error {
			if err := g.CheckoutRemoteBranch(wd, r.url, r.mainBranch.name); err != nil {
				return fmt.Errorf("Failed to checkout branch '%v': %w", r.mainBranch.name, err)
			}
			return nil
		}); err != nil {
			return err
		}

		type versionAndHash struct {
			v semver.Version
			h git.Hash
		}
		branchesToCreate := []versionAndHash{}
		tagsToCreate := []versionAndHash{}

		if err := u.WithStatus(fmt.Sprintf("Scanning history for '%v'...", r.mainBranch.changesPath), func(ui.Status) error {
			missingBranches := r.missingBranches.Clone()
			missingTags := r.missingTags.Clone()

			log, err := g.Log(wd, r.mainBranch.changesPath, -1)
			if err != nil {
				return fmt.Errorf("Failed to retrieve git log for '%v': %w", r.mainBranch.changesPath, err)
			}
			for i := len(log) - 1; i >= 0; i-- {
				cl := log[i]
				content, err := g.Show(wd, r.mainBranch.changesPath, cl.Hash.String())
				if err != nil {
					errs = append(errs, fmt.Errorf("Failed to read '%v' at %v: %w", r.mainBranch.changesPath, cl.Hash, err))
					continue
				}
				c, err := changes.Read(string(content))
				if err != nil {
					errs = append(errs, fmt.Errorf("Failed to parse '%v' at %v: %w", r.mainBranch.changesPath, cl.Hash, err))
					continue
				}
				versions := c.Versions().Set()
				for _, v := range versions.Union(missingBranches).List() {
					missingBranches.Remove(v)
					branchesToCreate = append(branchesToCreate, versionAndHash{v, cl.Hash})
				}
				for _, v := range versions.Union(missingTags).List() {
					missingTags.Remove(v)
					tagsToCreate = append(tagsToCreate, versionAndHash{v, cl.Hash})
				}
			}
			return nil
		}); err != nil {
			return err
		}

		u.WithStatus(fmt.Sprintf("Creating %d missing release branches...", len(branchesToCreate)), func(ui.Status) error {
			for _, vh := range branchesToCreate {
				if err := createReleaseBranch(r, u, g, wd, vh.h, vh.v, cred); err == nil {
					r.missingBranches.Remove(vh.v)
					numCreatedBranches++
				} else {
					errs = append(errs, err)
				}
			}
			return nil
		})

		u.WithStatus(fmt.Sprintf("Creating %d missing release tags...", len(branchesToCreate)), func(ui.Status) error {
			for _, vh := range tagsToCreate {
				if err := createReleaseTag(r, u, g, wd, vh.h, vh.v, cred); err == nil {
					r.missingTags.Remove(vh.v)
					numCreatedTags++
				} else {
					errs = append(errs, err)
				}
			}
			return nil
		})
		return nil
	})
	if err != nil {
		errs = append(errs, err)
	}
	return numCreatedBranches, numCreatedTags, errs
}

// createMissingReleases creates all the missing GitHub releases for the repo r.
func createMissingReleases(ctx context.Context, r repo, u ui.UI, c *github.Client) (numCreatedReleases int, errs []error) {
	u.Enter("Create missing releases", func() error {
		for version := range r.missingReleases {
			if err := createRelease(ctx, r, u, c, version); err != nil {
				errs = append(errs, err)
			} else {
				delete(r.missingReleases, version)
				numCreatedReleases++
			}
		}
		return nil
	})
	return numCreatedReleases, errs
}

// createRelease creates a GitHub release for the given version for the repo r.
func createRelease(ctx context.Context, r repo, u ui.UI, c *github.Client, version semver.Version) error {
	tagName := r.tagNameForVersion(version)
	releaseName := r.releaseNameForVersion(version)
	tag, ok := r.tags[tagName]
	if !ok {
		return fmt.Errorf("Failed to find release tag '%v'", tagName)
	}
	releaseNotes, ok := tag.changes.ReleaseNotes(version)
	if !ok {
		return fmt.Errorf("Failed to find release notes for version %v", version)
	}
	draft, prerelease := false, false
	_, _, err := c.Repositories.CreateRelease(ctx, r.owner, r.name, &github.RepositoryRelease{
		TagName:         &tagName,
		TargetCommitish: &tag.sha,
		Name:            &releaseName,
		Body:            &releaseNotes,
		Draft:           &draft,
		Prerelease:      &prerelease})
	if err != nil {
		return fmt.Errorf("Failed to create release: %w", err)
	}
	return nil
}

// doRelease checks out the repo to a temporary directory, and creates or
// updates the release branch and git tag for the release at from / v, and
// updating the CHANGES file. The release branch, tag and updated CHANGES file
// is pushed to the repo r.
func doRelease(ctx context.Context, r repo, u ui.UI, g *git.Git, c *github.Client, from *branch, v semver.Version, cred credentials) error {
	changes := *from.changes

	// Sanity checks (should be caught by validation)
	flavor := changes.CurrentVersion().Flavor
	if flavor == "" {
		return fmt.Errorf("Nothing in %v to release (top most version is not flavored)", from.changesPath)
	}

	if err := u.WithStatus("Checking out repository...", func(s ui.Status) error {
		wd := filepath.Join(os.TempDir(), "release-me", r.owner, r.name)
		if err := os.MkdirAll(wd, 0777); err != nil {
			return fmt.Errorf("Failed to create temporary checkout directory at '%v'", wd)
		}
		defer os.RemoveAll(wd)

		if err := g.CheckoutRemoteBranch(wd, r.url, from.name); err != nil {
			return fmt.Errorf("Failed to checkout branch '%v': %w", from.name, err)
		}

		head, err := g.HeadCL(wd)
		if err != nil {
			return fmt.Errorf("Failed to obtain branch HEAD: %w", err)
		}

		if head.Hash.String() != from.sha {
			return fmt.Errorf("New changes have landed in branch '%v'. Cannot continue", from.name)
		}

		s.Update("Updating %v", from.changesPath)

		// Rename flavored version to release version
		v.Flavor = ""
		changes.AdjustCurrentVersion(v, time.Now())

		// Save new CHANGES file
		changesPath := filepath.Join(wd, from.changesPath)
		commitMsg := fmt.Sprintf("Finalize release notes for %v\n\n", v)
		if notes := changes.CurrentVersionNotes(); notes != "" {
			commitMsg += "Release Notes:\n\n"
			commitMsg += changes.CurrentVersionNotes()
		}
		releaseHash, err := saveAndCommit(g, changesPath, changes.String(), commitMsg)
		if err != nil {
			return err
		}

		// Create release branch, tag and GitHub release.
		if err := createReleaseBranch(r, u, g, wd, releaseHash, v, cred); err != nil {
			return err
		}
		if err := createReleaseTag(r, u, g, wd, releaseHash, v, cred); err != nil {
			return err
		}
		if err := r.fetchTags(ctx, u, c); err != nil { // Re-scan tags to reflect updates. Needed by createRelease()
			return fmt.Errorf("Failed to fetch tags: %w", err)
		}
		if err := createRelease(ctx, r, u, c, v); err != nil {
			return err
		}

		// Stub main's CHANGES with a new flavored version
		nextVer := v
		nextVer.Flavor = flavor
		nextVer.Patch++
		changes.AddNewVersion(nextVer, time.Time{}, "\n[Add release notes here]\n")

		commitMsg = fmt.Sprintf("Stub release notes for %v\n\n", v)
		mainHash, err := saveAndCommit(g, changesPath, changes.String(), commitMsg)
		if err != nil {
			return err
		}

		// Push new CHANGES
		pushFlags := git.PushFlags{Username: cred.Username, Password: cred.AccessToken}
		if err := g.Push(wd, r.url, mainHash.String(), from.name, pushFlags); err != nil {
			return fmt.Errorf("Failed to push changes to main branch '%v': %w", from.name, err)
		}

		u.ShowMessage("Released", "Release %v successfully made", v)

		return nil
	}); err != nil {
		return err
	}
	return nil
}

// createReleaseBranch creates or updates an existing release branch with the
// changes at from / v, pushing the changes to the repo r.
// wd is the path to the local git checkout of the repo.
func createReleaseBranch(r repo, u ui.UI, g *git.Git, wd string, from git.Hash, v semver.Version, cred credentials) error {
	releaseBranchName := r.branchNameForVersion(v)
	pushFlags := git.PushFlags{Username: cred.Username, Password: cred.AccessToken}

	var err error
	if _, ok := r.branches[releaseBranchName]; ok {
		err = u.WithStatus(fmt.Sprintf("Updating existing release branch '%v'...", releaseBranchName), func(s ui.Status) error {
			// Checkout the target branch
			if err := g.CheckoutRemoteBranch(wd, r.url, releaseBranchName); err != nil {
				return fmt.Errorf("Failed to checkout branch '%v': %w", releaseBranchName, err)
			}
			// Rebase new changes
			if err := g.Rebase(wd, from); err != nil {
				return fmt.Errorf("Failed to rebase branch '%v': %w", releaseBranchName, err)
			}
			head, err := g.HeadCL(wd)
			if err != nil {
				return fmt.Errorf("Failed to get HEAD: %v", err)
			}
			if err := g.Push(wd, r.url, head.Hash.String(), releaseBranchName, pushFlags); err != nil {
				return fmt.Errorf("Failed to push changes to release branch '%v': %w", releaseBranchName, err)
			}
			return nil
		})
	} else {
		err = u.WithStatus(fmt.Sprintf("Creating new release branch '%v'...", releaseBranchName), func(s ui.Status) error {
			// Create a new branch
			if err := g.Push(wd, r.url, from.String(), releaseBranchName, pushFlags); err != nil {
				return fmt.Errorf("Failed to push changes to release branch '%v': %w", releaseBranchName, err)
			}
			return nil
		})
	}

	if err != nil {
		return fmt.Errorf("Failed to create release branch '%v': %w", releaseBranchName, err)
	}
	return nil
}

// createReleaseTag creates a new git tag for the release at from / v, pushing
// the changes to the repo r.
// wd is the path to the local git checkout of the repo.
func createReleaseTag(r repo, u ui.UI, g *git.Git, wd string, from git.Hash, v semver.Version, cred credentials) error {
	releaseTagName := r.tagNameForVersion(v)
	err := u.WithStatus(fmt.Sprintf("Creating release tag '%v'...", releaseTagName), func(s ui.Status) error {
		if err := g.Tag(wd, r.tagNameForVersion(v), from); err != nil {
			return fmt.Errorf("Failed to create branch tag '%v': %w", v.String(), err)
		}
		pushFlags := git.PushFlags{Username: cred.Username, Password: cred.AccessToken}
		if err := g.PushTags(wd, r.url, pushFlags); err != nil {
			return fmt.Errorf("Failed to push tags: %w", err)
		}
		return nil
	})
	if err != nil {
		return fmt.Errorf("Failed to create release tag '%v': %w", releaseTagName, err)
	}
	return nil
}

////////////////////////////////////////////////////////////////////////////////
// credentials
////////////////////////////////////////////////////////////////////////////////

// credentials holds a username and access token used for performing
// authenticated GitHub operations.
type credentials struct {
	Username    string `json:"user"`
	AccessToken string `json:"token"`
}

// load loads the credentials in JSON format from the given file path.
func (c *credentials) load(path string) error {
	f, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("Couldn't open credentials file at '%v': %w", path, err)
	}
	defer f.Close()
	if err := json.NewDecoder(f).Decode(c); err != nil {
		return fmt.Errorf("Couldn't parse credentials '%v': %w", path, err)
	}
	return nil
}

// save saves the credentials in JSON format to the given file path.
func (c credentials) save(path string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0777); err != nil {
		return fmt.Errorf("Couldn't create directories for credentials file at '%v': %w", path, err)
	}
	f, err := os.Create(path)
	if err != nil {
		return fmt.Errorf("Couldn't create credentials file: %w", err)
	}
	defer f.Close()
	if err := json.NewEncoder(f).Encode(c); err != nil {
		return fmt.Errorf("Couldn't write credentials file '%v': %w", path, err)
	}
	return nil
}

// getFromUser uses the UI u to ask the user for their credentials, storing the
// results into c.
func (c *credentials) getFromUser(u ui.UI, title string) error {
	return u.ShowForm(title, []ui.TextField{
		{Name: "user", Value: &c.Username},
		{Name: "access token", Value: &c.AccessToken},
	})
}

// askToSave uses the UI u to ask the user whether they'd like to save their
// credentials to the given file path. If the user accepts, askToSave() saves
// the credentials in JSON format to the given file path.
func (c credentials) askToSave(ui ui.UI, path string) {
	i, err := ui.ShowMenu("Save credentials to '"+path+"' ?", []string{"yes", "no"})
	if err == nil && i == 0 {
		if err := c.save(path); err != nil {
			ui.ShowMessage("Error", "Could not save credentials: %v", err)
		}
	}
	return
}

////////////////////////////////////////////////////////////////////////////////
// repo
////////////////////////////////////////////////////////////////////////////////

type repo struct {
	owner           string              // www.github.com/<owner>/<name>
	name            string              // www.github.com/<owner>/<name>
	url             string              // Git remote URL
	mainBranch      *branch             // Pointer to the default git branch
	versionStyle    semver.Style        // Style determined from existing branch / tags names
	branches        map[string]*branch  // Existing branches by name
	tags            map[string]*tag     // Existing tags by name
	releases        map[string]*release // Existing releases by name
	missingBranches semver.Set          // Release branches mentioned in CHANGES, but missing
	missingTags     semver.Set          // Release tags mentioned in CHANGES, but missing
	missingReleases semver.Set          // Releases mentioned in CHANGES, but missing
}

type branch struct {
	name           string           // Branch name
	sha            string           // Branch git hash
	releaseVersion *int             // Parsed major version (nil if not a release branch)
	changes        *changes.Content // Content of CHANGES file at sha
	changesPath    string           // Repo-relative path to CHANGES file
	problems       []error          // Problems found
}

type tag struct {
	name    string           // Tag name
	sha     string           // Tag git hash
	changes *changes.Content // Content of CHANGES file at sha
}

type release struct {
	name string
	tag  string
}

// fetchBranches retrieves all the branches of the repo r, populating the
// r.branches, r.mainBranch fields.
func (r *repo) fetchBranches(ctx context.Context, u ui.UI, c *github.Client) error {
	return u.WithStatus("Fetching branches", func(ui.Status) error {
		repo, _, err := c.Repositories.Get(ctx, r.owner, r.name)
		if err != nil {
			return fmt.Errorf("Failed to fetch info for repository: %w", err)
		}

		branches, _, err := c.Repositories.ListBranches(ctx, r.owner, r.name, &github.BranchListOptions{})
		if err != nil {
			return fmt.Errorf("Failed to list branches for repository: %w", err)
		}

		r.branches = map[string]*branch{}

		for _, b := range branches {
			b := &branch{
				name: b.GetName(),
				sha:  b.GetCommit().GetSHA(),
			}

			if b.name == repo.GetDefaultBranch() {
				r.mainBranch = b
			}
			b.releaseVersion = parseReleaseBranch(b.name)
			b.changes, b.changesPath, err = r.fetchChanges(ctx, c, u, b.name, b.sha)
			switch err {
			case nil:
				r.branches[b.name] = b
			case errNoChangesFile:
				continue
			default:
				return err
			}
		}

		return nil
	})
}

// fetchTags retrieves all the branches of the repo r, populating the r.tags
// field.
func (r *repo) fetchTags(ctx context.Context, u ui.UI, c *github.Client) error {
	return u.WithStatus("Fetching tags", func(ui.Status) error {
		tags, _, err := c.Repositories.ListTags(ctx, r.owner, r.name, nil)
		if err != nil {
			return fmt.Errorf("Failed to list tags for repository: %w", err)
		}

		r.tags = map[string]*tag{}
		for _, t := range tags {
			t := &tag{
				name: t.GetName(),
				sha:  t.GetCommit().GetSHA(),
			}

			t.changes, _, err = r.fetchChanges(ctx, c, u, t.name, t.sha)

			switch err {
			case nil:
				r.tags[t.name] = t
			case errNoChangesFile:
				continue
			default:
				return err
			}
		}

		return nil
	})
}

// fetchTags retrieves all GitHub releases of the repo r, populating the
// r.releases field.
func (r *repo) fetchReleases(ctx context.Context, u ui.UI, c *github.Client) error {
	return u.WithStatus("Fetching releases", func(ui.Status) error {
		releases, _, err := c.Repositories.ListReleases(ctx, r.owner, r.name, nil)
		if err != nil {
			return fmt.Errorf("Failed to list tags for repository: %w", err)
		}

		r.releases = map[string]*release{}
		for _, rel := range releases {
			rel := &release{
				name: rel.GetName(),
				tag:  rel.GetTagName(),
			}
			r.releases[rel.name] = rel
		}

		return nil
	})
}

// determineVersionStyle attempts to determine the style used to label release
// branches, tags and releases. If no style can be determined, these defaults
// are used:
//   branch: "release-<major>.x.x"
//   tag:    "release-<major>.<minor>.<patch>"
func (r *repo) determineVersionStyle() {
	prefixUses := map[string]int{}
	usesPatch := true
	for _, b := range r.branches {
		if s := semver.ParseStyle(b.name); s != nil {
			prefixUses[s.Prefix] = prefixUses[s.Prefix] + 1
			usesPatch = !s.OmitPatch && usesPatch
		}
	}
	for _, t := range r.tags {
		if s := semver.ParseStyle(t.name); s != nil {
			prefixUses[s.Prefix] = prefixUses[s.Prefix] + 1
			usesPatch = !s.OmitPatch && usesPatch
		}
	}
	for _, r := range r.releases {
		if s := semver.ParseStyle(r.name); s != nil {
			prefixUses[s.Prefix] = prefixUses[s.Prefix] + 1
			usesPatch = !s.OmitPatch && usesPatch
		}
	}
	mostCommonPrefix := "release-"
	mostCommonPrefixUses := 0
	for p, c := range prefixUses {
		if c >= mostCommonPrefixUses {
			mostCommonPrefix = p
			mostCommonPrefixUses = c
		}
	}
	r.versionStyle.Prefix = mostCommonPrefix
	r.versionStyle.OmitPatch = !usesPatch
}

// fetchChanges uses the GitHub git API to obtain the CHANGES file content for
// the given sha.
func (r *repo) fetchChanges(ctx context.Context, c *github.Client, u ui.UI, name, sha string) (*changes.Content, string, error) {
	var out *changes.Content
	var changesPath string
	err := u.WithStatus(fmt.Sprintf("Fetching changes for '%v'", name), func(ui.Status) error {
		commit, _, err := c.Git.GetCommit(ctx, r.owner, r.name, sha)
		if err != nil {
			return fmt.Errorf("Failed to fetch commit %v: %w", name, err)
		}
		tree, _, err := c.Git.GetTree(ctx, r.owner, r.name, commit.Tree.GetSHA(), false)
		if err != nil {
			return fmt.Errorf("Failed to fetch commit %v tree: %w", name, err)
		}
		changesSHA := ""
		for _, entry := range tree.Entries {
			if entry.GetType() == "blob" && isChangesFile(entry.GetPath()) {
				changesSHA = entry.GetSHA()
				changesPath = entry.GetPath()
				break
			}
		}
		if changesSHA == "" {
			return errNoChangesFile
		}
		blob, _, err := c.Git.GetBlobRaw(ctx, r.owner, r.name, changesSHA)
		if err != nil {
			return fmt.Errorf("Failed to fetch CHANGES content for %v: %w", name, err)
		}
		out, err = changes.Read(string(blob))
		if err != nil {
			return fmt.Errorf("Failed to parse CHANGES content for %v: %w", name, err)
		}
		return nil
	})
	if err != nil {
		return nil, "", err
	}
	return out, changesPath, nil
}

// isChangesFile returns true if the file at p could be a CHANGES file.
func isChangesFile(p string) bool {
	dir, name := path.Split(p)
	if dir == "" {
		for _, n := range changes.FileNames {
			if name == n {
				return true
			}
		}
	}
	return false
}

// validate looks for and returns a list of problems found with the current
// release branches, tags and CHANGES of the repo r.
func (r *repo) validate(ctx context.Context, u ui.UI) ([]string, error) {
	problems := []string{}

	r.missingBranches = semver.Set{}
	r.missingTags = semver.Set{}
	r.missingReleases = semver.Set{}

	for _, b := range r.branches {
		isDevelopementBranch := r.mainBranch == b
		b.problems = append(b.problems, b.changes.Validate(isDevelopementBranch)...)

		for _, v := range b.changes.Versions() {
			if v.Flavor != "" {
				continue
			}
			if r.mainBranch == b {
				vBranchName := r.branchNameForVersion(v)
				if _, found := r.branches[vBranchName]; !found {
					r.missingBranches.Add(v)
				}
				vTagName := r.tagNameForVersion(v)
				if _, found := r.tags[vTagName]; !found {
					r.missingTags.Add(v)
				}
				vReleaseName := r.releaseNameForVersion(v)
				if _, found := r.releases[vReleaseName]; !found {
					r.missingReleases.Add(v)
				}
			}
		}

		if b.releaseVersion != nil { // Is a release branch
			moaned := map[int]bool{}
			for _, v := range b.changes.Versions() {
				if v.Major > *b.releaseVersion && !moaned[v.Major] {
					moaned[v.Major] = true
					b.problems = append(b.problems,
						fmt.Errorf("CHANGES in release branch %v.x.x has notes for future version %v", *b.releaseVersion, v))
					break
				}
			}
		}

		for _, p := range b.problems {
			problems = append(problems, fmt.Sprintf("Branch '%v': %v", b.name, p))
		}
	}

	return problems, nil
}

var branchVersionRE = regexp.MustCompile(`^(?:\w*-|v)?(\d+)\.x+(?:\.x+)?$`)

// parseReleaseBranch parses the major release version from the branch name s.
func parseReleaseBranch(s string) *int {
	m := branchVersionRE.FindStringSubmatch(s)
	if len(m) == 0 {
		return nil
	}
	major, err := strconv.Atoi(m[1])
	if err != nil {
		return nil
	}
	return &major
}

// branchNameForVersion returns the style-formatted release branch name for
// the version v.
func (r repo) branchNameForVersion(v semver.Version) string {
	if r.versionStyle.OmitPatch {
		return fmt.Sprintf("%s%v.x", r.versionStyle.Prefix, v.Major)
	}
	return fmt.Sprintf("%s%v.x.x", r.versionStyle.Prefix, v.Major)
}

// tagNameForVersion returns the style-formatted release tag name for the
// version v.
func (r repo) tagNameForVersion(v semver.Version) string {
	return r.versionStyle.Format(v)
}

// releaseNameForVersion returns the style-formatted release name for the
// version v.
func (r repo) releaseNameForVersion(v semver.Version) string {
	return r.versionStyle.Format(v)
}
