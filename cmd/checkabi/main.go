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

package main

import (
	"flag"
	"fmt"
	"io/ioutil"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	"github.com/ben-clayton/release-me/abidiff"
	"github.com/ben-clayton/release-me/pkg"
	"github.com/ben-clayton/release-me/pkg/store"
	_ "github.com/ben-clayton/release-me/pkg/store/file"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "%s\n", err)
		os.Exit(1)
	}
}

func run() error {
	flag.Usage = func() {
		fmt.Fprintf(flag.CommandLine.Output(), `Usage of %[1]s:
    %[1]s --store=<path-to-store> --pkg=<path-to-new-package> [--verbose]
`, os.Args[0])
		flag.PrintDefaults()
	}

	storeURLRaw := flag.String("store", "", "URL to package storage")
	newPkgPath := flag.String("pkg", "", "Path of the new package to check")
	verbose := flag.Bool("verbose", false, "Print verbose messages")
	flag.Parse()

	for _, arg := range []string{"store", "pkg"} {
		if flag.Lookup(arg).Value.String() == "" {
			return fmt.Errorf("Argument --%v must be set", arg)
		}
	}

	ad, err := abidiff.New()
	if err != nil {
		return err
	}

	newPkg, err := pkg.Load(*newPkgPath)
	if err != nil {
		return err
	}

	storeURL, err := url.Parse(*storeURLRaw)
	if err != nil {
		return fmt.Errorf("Failed to parse store url '%v': %w", *storeURLRaw, err)
	}

	if storeURL.Scheme == "" { // No scheme? Assume local file path.
		storeURL.Scheme = "file"
	}

	store, err := store.New(*storeURL)
	if err != nil {
		return fmt.Errorf("Failed to create store: %w", err)
	}

	pkgs, err := store.Packages()
	if err != nil {
		return fmt.Errorf("Failed to fetch packages from store: %w", err)
	}

	// Filter the packages to non-flavored versions that are less than newPkg.
	pkgs = pkgs.Filter(func(i pkg.Info) bool {
		return i.Version.Flavor == "" && newPkg.Info.Version.GreaterThan(i.Version, false)
	})

	if len(pkgs) == 0 {
		fmt.Printf("Store has no released packages that match the major version %v\n", newPkg.Info.Version.Major)
		return nil
	}

	// Packages are guaranteed to be ordered starting with the highest version
	// then most recent.
	oldPkgInfo := pkgs[0]
	if *verbose {
		fmt.Printf("\n*** Comparing '%v' to '%v' ***\n", newPkg.Info, oldPkgInfo)
	}

	oldPkg, err := store.Fetch(oldPkgInfo)
	if err != nil {
		return fmt.Errorf("Failed to fetch last release: %w", err)
	}

	// Create a temporary directory for saving the shared objects to compare
	// against.
	tmp, err := ioutil.TempDir("", "release-me-pkg-test")
	if err != nil {
		return fmt.Errorf("ioutil.TempDir() returned %v", err)
	}
	defer os.RemoveAll(tmp)

	// Store the old and new packages to the temporary directories so we have
	// file paths to pass to abidiff.
	oldRoot, newRoot := filepath.Join(tmp, "old"), filepath.Join(tmp, "new")
	if err := oldPkg.Save(oldRoot); err != nil {
		return fmt.Errorf("Failed to save old release to '%v': %w", tmp, err)
	}
	if err := newPkg.Save(newRoot); err != nil {
		return fmt.Errorf("Failed to save new release to '%v': %w", tmp, err)
	}

	// Gather all the shared objects from the old and new packages.
	oldSOs, newSOs := sharedObjects(oldPkg), sharedObjects(newPkg)
	newSONames := map[string]string{}
	for _, path := range newSOs {
		newSONames[trimSOVersion(path)] = path
	}

	sameMajorVersion := oldPkgInfo.Version.Major == newPkg.Info.Version.Major
	sameMajorAndMinorVersion := oldPkgInfo.Version.Major == newPkg.Info.Version.Major &&
		oldPkgInfo.Version.Minor == newPkg.Info.Version.Minor
	errs := []error{}

	recommendBumpMajor, recommendBumpMinor := false, false

	// For each shared object...
	for _, oldPath := range oldSOs {
		trimmed := trimSOVersion(oldPath)
		newPath, found := newSONames[trimmed]
		if !found {
			if sameMajorVersion {
				errs = append(errs, fmt.Errorf("'%v' is missing in new package", oldPath))
			}
			continue
		}

		if *verbose {
			fmt.Printf("> Checking ABI differences for '%v'\n", newPath)
		}

		oldPath, newPath := filepath.Join(oldRoot, oldPath), filepath.Join(newRoot, newPath)
		diff, err := ad.Diff(oldPath, newPath)
		if err != nil {
			return err
		}

		switch {
		case sameMajorVersion && diff.Result == abidiff.IncompatibleABIChanges:
			errs = append(errs, fmt.Errorf("Incompatible ABI changes made for '%v' with same major version:\n%v",
				newPath, indent(diff.Output, 2)))
			recommendBumpMajor = true
		case sameMajorAndMinorVersion && diff.Result == abidiff.CompatibleABIChanges:
			errs = append(errs, fmt.Errorf("ABI changes made for '%v' with same major and minor version:\n%v",
				newPath, indent(diff.Output, 2)))
			recommendBumpMinor = true
		}

		if *verbose {
			switch diff.Result {
			case abidiff.CompatibleABIChanges:
				fmt.Println("Backwards-compatible ABI changes found")
			case abidiff.IncompatibleABIChanges:
				fmt.Println("Incompatible ABI changes found")
			case abidiff.NoABIChanges:
				fmt.Println("No ABI changes found")
			}
			fmt.Println(indent(diff.Output, 2))
		}
	}

	if len(errs) > 0 {
		msgs := []string{fmt.Sprintf("Invalid ABI changes found between versions %v and %v", oldPkg.Info.Version, newPkg.Info.Version)}
		switch {
		case recommendBumpMajor:
			msgs = append(msgs, "Package major version needs to be increased", "")
		case recommendBumpMinor:
			msgs = append(msgs, "Package minor version needs to be increased", "")
		}
		for _, err := range errs {
			msgs = append(msgs, err.Error())
		}
		return fmt.Errorf(strings.Join(msgs, "\n"))
	}

	return nil
}

// sharedObjects returns the path to all the shared objects found in pkg.
func sharedObjects(pkg *pkg.Package) []string {
	out := []string{}
	for _, file := range pkg.Files {
		if file.Link != "" {
			continue // Don't examine symlinks
		}
		_, name := filepath.Split(file.Path)
		if strings.Contains(name, ".so") {
			out = append(out, file.Path)
		}
	}
	return out
}

// trimSOVersion returns path with any trailing shared object versioning
// removed.
func trimSOVersion(path string) string {
	i := strings.LastIndex(path, ".so")
	if i < 0 {
		return path
	}
	return path[:i+len(".so")]
}

// indent returns s with each new line indented with n whitespace charaters.
func indent(s string, n int) string {
	pad := strings.Repeat(" ", n)
	lines := strings.Split(s, "\n")
	for i, l := range lines {
		if len(l) > 0 {
			lines[i] = pad + l
		}
	}
	return strings.Join(lines, "\n")
}
