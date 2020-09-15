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

package pkg_test

import (
	"fmt"
	"io/ioutil"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/ben-clayton/release-me/pkg"
	"github.com/ben-clayton/release-me/semver"
)

func TestInfoString(t *testing.T) {
	for _, test := range []struct {
		expect string
		info   pkg.Info
	}{
		{
			"my-awesome-package--1.2.3.zip",
			pkg.Info{
				Name:    "my-awesome-package",
				Version: semver.MustParse("1.2.3"),
				Type:    pkg.Zip,
			},
		}, {
			"my-awesome-package--1.2.3--987654.zip",
			pkg.Info{
				Name:    "my-awesome-package",
				Version: semver.MustParse("1.2.3"),
				SHA:     "9876543210abcdef",
				Type:    pkg.Zip,
			},
		}, {
			"my-awesome-package--1.2.3-blah--987654.zip",
			pkg.Info{
				Name:    "my-awesome-package",
				Version: semver.MustParse("1.2.3-blah"),
				SHA:     "9876543210abcdef",
				Type:    pkg.Zip,
			},
		},
	} {
		name, err := test.info.Canonical()
		if err != nil {
			t.Fatalf("pkg.Info.Canonical() returned %v for %+v", err, test.info)
		}
		check(t, name, test.expect, "pkg.Info.String()")

		parsed, err := pkg.Parse(filepath.Join("some", "dir", "prefix", name))
		if err != nil {
			t.Fatalf("pkg.Parse('%v') returned %v", name, err)
		}

		check(t, parsed.Name, test.info.Name, "pkg.Parse(%v).Name", name)
		check(t, parsed.Version, test.info.Version, "pkg.Parse(%v).Version", name)
		check(t, parsed.SHA, pkg.ShortSHA(test.info.SHA), "pkg.Parse(%v).SHA", name)
	}
}

func TestCreateAndLoad(t *testing.T) {
	// Utilities
	mkdir := func(path ...string) string {
		dir := filepath.Join(path...)
		if err := os.MkdirAll(dir, 0777); err != nil {
			t.Fatalf("Failed to create directory '%s': %v", dir, err)
		}
		return dir
	}
	writeFile := func(path, data string) {
		mkdir(filepath.Dir(path))
		if err := ioutil.WriteFile(path, []byte(data), 0666); err != nil {
			t.Fatalf("Failed to write file '%s': %v", path, err)
		}
	}
	symlink := func(to, from string) {
		mkdir(filepath.Dir(from))
		err := os.Symlink(to, from)
		if err != nil {
			t.Fatalf("Failed to create symlink '%s' to '%s'", from, to)
		}
	}

	tmp, err := ioutil.TempDir("", "release-me-pkg-test")
	if err != nil {
		t.Fatalf("ioutil.TempDir() returned %v", err)
	}
	defer os.RemoveAll(tmp)

	outDir := mkdir(tmp, "out")
	srcDir := mkdir(tmp, "src")
	buildDir := mkdir(tmp, "build")
	pkgCfg := filepath.Join(srcDir, "package.cfg")

	// Write fake build artifact files and package config file
	writeFile(filepath.Join(buildDir, "a", "cat"), "meow")
	writeFile(filepath.Join(buildDir, "a", "lion"), "roar")
	writeFile(filepath.Join(buildDir, "b", "dog"), "woof")
	writeFile(filepath.Join(buildDir, "b", "duck"), "quack")
	symlink(filepath.Join(buildDir, "a", "cat"), filepath.Join(buildDir, "c", "link"))
	writeFile(pkgCfg, `{
	"type": "tar",
	"name": "pkgtest",
	"files": [
		"a/cat",
		"a/lion",
		"?/d*",
		"c/link"
	]
}`)
	writeFile(filepath.Join(srcDir, "CHANGES"), `Revision history for pkgtest.

## 0.1.2-dev 2020-06-16

The dog says woof.

## 0.1.1 2020-06-10

The cat says purr.
`)

	path, err := pkg.Create(pkgCfg, srcDir, buildDir, outDir)
	if err != nil {
		t.Fatalf("pkg.Create() returned %v", err)
	}

	got, err := pkg.Load(path)
	if err != nil {
		t.Fatalf("pkg.Load() returned %v", err)
	}

	expected := pkg.Package{
		Info: pkg.Info{
			Name:    "pkgtest",
			Version: semver.MustParse("0.1.2-dev"),
		},
		Files: []pkg.File{
			{Path: "a/cat", Data: []byte("meow"), Mode: 0664},
			{Path: "a/lion", Data: []byte("roar"), Mode: 0664},
			{Path: "b/dog", Data: []byte("woof"), Mode: 0664},
			{Path: "b/duck", Data: []byte("quack"), Mode: 0664},
			{Path: "c/link", Link: "../a/cat", Mode: 0777},
		},
	}
	check(t, *got, expected, "pkg.Files")
}

func check(t *testing.T, got, expect interface{}, name string, args ...interface{}) {
	if !reflect.DeepEqual(got, expect) {
		t.Errorf("%v was not as expected!\ngot      %+v\nexpected %+v", fmt.Sprintf(name, args...), got, expect)
	}
}
