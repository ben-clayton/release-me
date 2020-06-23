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

package changes_test

import (
	"fmt"
	"reflect"
	"testing"
	"time"

	"github.com/ben-clayton/release-me/changes"
	"github.com/ben-clayton/release-me/semver"
)

const (
	devNotes = `
### 2.2.1-dev
xxx
Notes about the 2.2.1 patch release
yyy
### 2.2.0    2020-02-10

Notes about the 2.2.0 minor release

### 2.1.0

Notes about the 2.1.0 minor release

### 2.0.0    2020-01-01

Notes about the 2.0.0 major release

### 1.0.0

Notes about the 1.0.0 major release
`
	relNotes = `
### 2.2.1

Notes about the 2.2.1 patch release

### 2.2.0    2020-01-04

Notes about the 2.2.0 minor release

### 2.1.0

Notes about the 2.1.0 minor release

### 2.0.0    2020-01-01

Notes about the 2.0.0 major release

### 1.0.0

Notes about the 1.0.0 major release
`
	vYearMinorStyle = `

v2019.2-dev 2019-01-07

	notes-d

v2019.1 2019-01-07

	notes-c

v2018.6 2018-11-07

	notes-b

v2018.5 2018-09-07

	notes-a
`
)

func check(t *testing.T, name string, got, expect interface{}) {
	if !reflect.DeepEqual(got, expect) {
		t.Errorf("%v was not as expected.\nGot:\n`%v`\nExpect:\n`%v`", name, got, expect)
	}
}

func TestRead(t *testing.T) {
	c, err := changes.Read(devNotes)
	if err != nil {
		t.Errorf("changes.Read() returned error: %v", err)
		return
	}
	got := c.Versions()
	expect := semver.List{
		{Major: 2, Minor: 2, Patch: 1, Flavor: "dev"},
		{Major: 2, Minor: 2, Patch: 0},
		{Major: 2, Minor: 1, Patch: 0},
		{Major: 2, Minor: 0, Patch: 0},
		{Major: 1, Minor: 0, Patch: 0},
	}
	check(t, "Versions()", got, expect)
}

func TestReadvYearMinorStyle(t *testing.T) {
	c, err := changes.Read(vYearMinorStyle)
	if err != nil {
		t.Errorf("changes.Read() returned error: %v", err)
		return
	}
	got := c.Versions()
	expect := semver.List{
		{Major: 2019, Minor: 2, Flavor: "dev"},
		{Major: 2019, Minor: 1},
		{Major: 2018, Minor: 6},
		{Major: 2018, Minor: 5},
	}
	check(t, "Versions()", got, expect)
}

func TestString(t *testing.T) {
	c, err := changes.Read(devNotes)
	if err != nil {
		t.Errorf("changes.Read() returned error: %v", err)
		return
	}
	check(t, "String()", c.String(), devNotes)
}

func TestCurrentVersion(t *testing.T) {
	c, err := changes.Read(devNotes)
	if err != nil {
		t.Errorf("changes.Read() returned error: %v", err)
		return
	}
	check(t, "CurrentVersion()", c.CurrentVersion(), semver.Version{
		Major: 2, Minor: 2, Patch: 1, Flavor: "dev",
	})
}

func TestCurrentVersionNotes(t *testing.T) {
	c, err := changes.Read(devNotes)
	if err != nil {
		t.Errorf("changes.Read() returned error: %v", err)
		return
	}
	check(t, "CurrentVersionNotes()", c.CurrentVersionNotes(), `xxx
Notes about the 2.2.1 patch release
yyy`)
}

func TestAdjustCurrentVersion(t *testing.T) {
	c, err := changes.Read(devNotes)
	if err != nil {
		t.Errorf("changes.Read() returned error: %v", err)
		return
	}
	ver := semver.Version{Major: 10, Minor: 20, Patch: 30, Flavor: "woof"}
	date, _ := time.Parse("2006-01-02", "2019-07-10")
	c.AdjustCurrentVersion(ver, date)
	expect := `
### 10.20.30-woof  2019-07-10
xxx
Notes about the 2.2.1 patch release
yyy
### 2.2.0    2020-02-10

Notes about the 2.2.0 minor release

### 2.1.0

Notes about the 2.1.0 minor release

### 2.0.0    2020-01-01

Notes about the 2.0.0 major release

### 1.0.0

Notes about the 1.0.0 major release
`
	check(t, "String()", c.String(), expect)
}

func TestAddNewVersionFromEmpty(t *testing.T) {
	c, err := changes.Read(`# Release notes for fooglezap

This file contains all the release notes about fooglezap`)
	if err != nil {
		t.Errorf("changes.Read() returned error: %v", err)
		return
	}
	ver := semver.Version{Major: 10, Minor: 20, Patch: 30, Flavor: "woof"}
	date, _ := time.Parse("2006-01-02", "2019-07-10")
	if err := c.AddNewVersion(ver, date, "bark bark bark"); err != nil {
		t.Errorf("AddNewVersion() returned error: %v", err)
	}
	check(t, "String()", c.String(), `# Release notes for fooglezap

This file contains all the release notes about fooglezap

10.20.30-woof  2019-07-10

bark bark bark
`)
}

func TestAddNewVersionFromExisting(t *testing.T) {
	c, err := changes.Read(`# Release notes for fooglezap

This file contains all the release notes about fooglezap

## 1.2.3     2015-11-30

purr purr purr
`)
	if err != nil {
		t.Errorf("changes.Read() returned error: %v", err)
		return
	}
	ver := semver.Version{Major: 10, Minor: 20, Patch: 30, Flavor: "woof"}
	date, _ := time.Parse("2006-01-02", "2019-07-10")
	if err := c.AddNewVersion(ver, date, "bark bark bark"); err != nil {
		t.Errorf("AddNewVersion() returned error: %v", err)
	}
	check(t, "String()", c.String(), `# Release notes for fooglezap

This file contains all the release notes about fooglezap

## 10.20.30-woof     2019-07-10

bark bark bark

## 1.2.3     2015-11-30

purr purr purr
`)
}
func TestValidateCleanForDev(t *testing.T) {
	c, err := changes.Read(devNotes)
	if err != nil {
		t.Errorf("changes.Read() returned error: %v", err)
		return
	}
	check(t, "Validate()", c.Validate(true), []error{})
}

func TestValidateNoVersion(t *testing.T) {
	c, err := changes.Read(`
I'm text with no version info.

This looks a bit like a version 1.2.3, but doesn't match the pattern!
	`)
	if err != nil {
		t.Errorf("changes.Read() returned error: %v", err)
		return
	}
	check(t, "Validate()", c.Validate(true), []error{
		fmt.Errorf("CHANGES file does not contain any versions"),
	})
}

func TestValidateBadFlavorForRel(t *testing.T) {
	c, err := changes.Read(relNotes)
	if err != nil {
		t.Errorf("changes.Read() returned error: %v", err)
		return
	}
	check(t, "Validate()", c.Validate(true), []error{
		fmt.Errorf("Top-most version 2.2.1 on line 2 is not suffixed with a flavor (e.g. -dev)"),
	})
}

func TestValidateVersionOrder(t *testing.T) {
	c, err := changes.Read(`
### 2.2.1

### 2.1.0

### 2.1.0

### 1.0.0

### 2.4.0
`)
	if err != nil {
		t.Errorf("changes.Read() returned error: %v", err)
		return
	}
	check(t, "Validate()", c.Validate(false), []error{
		fmt.Errorf("Version 2.1.0 on line 4 is not greater than version 2.1.0 on line 6"),
		fmt.Errorf("Version 1.0.0 on line 8 is not greater than version 2.4.0 on line 10"),
	})
}
