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

// Package changes provides functions for parsing and modifying CHANGES files.
package changes

import (
	"fmt"
	"io/ioutil"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/ben-clayton/release-me/semver"
)

// FileNames lists the permitted file names for the CHANGES file.
var FileNames = []string{
	"CHANGES", "CHANGES.md",
}

// Content holds the parsed content of a CHANGES file.
type Content struct {
	versions []version
	lines    []string
}

type version struct {
	semver.Version
	line   int    // Line number this was found on
	prefix string // Prefix before the semver
	style  semver.Style
	sep    string // Separator between version and date
	date   string // Date after the semver
}

var (
	// changesVersionRE is the regular expression used to parse versions from a CHANGES file.
	changesVersionRE = regexp.MustCompile(`^(#* *)((?:\w*-|v)?\d+\.\d+(?:\.\d+)?(?:-\w+)?)( *)(\d\d\d\d-\d\d-\d\d)? *$`)
)

// Load loads the CHANGES file from path. Path may be a full file path, or a
// path to a project root directory containing the CHANGES file.
func Load(path string) (*Content, error) {
	fi, err := os.Stat(path)
	if err != nil {
		return nil, err
	}

	if fi.IsDir() {
		for _, name := range FileNames {
			file, err := ioutil.ReadFile(filepath.Join(path, name))
			if err != nil {
				continue
			}
			return Read(string(file))
		}
		return nil, fmt.Errorf("Failed to find CHANGES file at '%v'", path)
	}

	file, err := ioutil.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("Couldn't open CHANGES file at %v: %w", path, err)
	}
	return Read(string(file))
}

// Read parses the content of the CHANGES file from body, returning a Content.
func Read(body string) (*Content, error) {
	c := Content{lines: strings.Split(body, "\n")}
	if err := c.parse(); err != nil {
		return nil, err
	}
	return &c, nil
}

func (c *Content) parse() error {
	for i, line := range c.lines {
		m := changesVersionRE.FindStringSubmatch(line)
		if len(m) == 0 {
			continue
		}
		var err error
		v := version{line: i + 1}
		v.prefix = m[1]
		v.Version, err = semver.Parse(m[2])
		if err != nil {
			return fmt.Errorf("%v on line %v", err, i)
		}
		if s := semver.ParseStyle(m[2]); s != nil {
			v.style = *s
		}
		v.sep = m[3]
		v.date = m[4]
		c.versions = append(c.versions, v)
	}
	return nil
}

func (c Content) String() string {
	return strings.Join(c.lines, "\n")
}

// ReleaseNotes returns the release notes for the given version
func (c Content) ReleaseNotes(v semver.Version) (string, bool) {
	startLine, endLine := -1, -1
loop:
	for _, ver := range c.versions {
		switch {
		case startLine != -1:
			endLine = ver.line - 1
			break loop
		case ver.Version == v:
			startLine = ver.line
		}
	}
	if startLine == -1 {
		return "", false
	}
	for startLine < len(c.lines) && strings.TrimSpace(c.lines[startLine]) == "" {
		startLine++
	}
	if endLine == -1 {
		endLine = len(c.lines)
	}
	for endLine > startLine && strings.TrimSpace(c.lines[endLine-1]) == "" {
		endLine--
	}
	return strings.Join(c.lines[startLine:endLine], "\n"), true
}

func (c version) String() string {
	b := strings.Builder{}
	b.WriteString(c.prefix)
	b.WriteString(c.style.Format(c.Version))
	b.WriteString(c.sep)
	b.WriteString(c.date)
	return b.String()
}

// Versions returns all the versions found in the changes content in order
// declared.
func (c *Content) Versions() semver.List {
	out := make(semver.List, len(c.versions))
	for i, v := range c.versions {
		out[i] = v.Version
	}
	out.Sort() // If well formed, this would not be needed, but play safe.
	return out
}

// CurrentVersion returns the semantic version for the top most version.
func (c *Content) CurrentVersion() semver.Version {
	if len(c.versions) == 0 {
		return semver.Version{}
	}
	return c.versions[0].Version
}

// CurrentVersionNotes returns the release notes for the top most version.
func (c *Content) CurrentVersionNotes() string {
	if len(c.versions) > 0 {
		from, to := c.versions[0].line+1, len(c.lines)
		if len(c.versions) > 1 {
			to = c.versions[1].line
		}
		if from < to {
			from-- // 0-based index
			to--   // 0-based index
			return strings.Join(c.lines[from:to], "\n")
		}
	}

	return ""
}

// AdjustCurrentVersion changes the semantic version for the top most version.
func (c *Content) AdjustCurrentVersion(v semver.Version, t time.Time) bool {
	if len(c.versions) == 0 {
		return false
	}
	cv := &c.versions[0]
	cv.Version = v
	cv.date = t.Format("2006-01-02")
	if cv.sep == "" {
		cv.sep = "  "
	}
	c.lines[cv.line-1] = cv.String()
	return true
}

// AddNewVersion adds a new top-most version.
func (c *Content) AddNewVersion(v semver.Version, t time.Time, content string) error {
	h := version{
		Version: v,
	}

	if !t.IsZero() {
		h.date = t.Format("2006-01-02")
		h.sep = "  "
	}

	at := len(c.lines)
	if len(c.versions) > 0 {
		at = c.versions[0].line - 1
		// Adopt style of existing heading
		existing := c.versions[0]
		h.prefix = existing.prefix
		h.style = existing.style
		h.sep = existing.sep
	}

	lines := append([]string{}, c.lines[0:at]...)
	if len(lines) == 0 || lines[len(lines)-1] != "" {
		lines = append(lines, "")
	}
	lines = append(lines, h.String(), "")
	if content != "" {
		lines = append(lines, strings.Split(content, "\n")...)
		lines = append(lines, "")
	}
	lines = append(lines, c.lines[at:]...)
	c.lines = lines
	c.versions = nil
	return c.parse()
}

// Validate checks the CHANGES content is well formed, returning any errors
// found.
func (c *Content) Validate(isDevelopmentBranch bool) []error {
	if len(c.versions) == 0 {
		return []error{fmt.Errorf("CHANGES file does not contain any versions")}
	}

	errs := []error{}

	if isDevelopmentBranch {
		if c.versions[0].Flavor == "" {
			errs = append(errs, fmt.Errorf("Top-most version %v on line %v is not suffixed with a flavor (e.g. -dev)",
				c.versions[0].Version, c.versions[0].line))
		}
	}

	for i, curr := range c.versions[1:] {
		next := c.versions[i]
		if curr.Flavor != "" {
			errs = append(errs, fmt.Errorf("Version %v on line %v is flavored. Only the current version can be flavored",
				curr.Version, curr.line))
		}
		if !next.GreaterThan(curr.Version, false) {
			errs = append(errs, fmt.Errorf("Version %v on line %v is not greater than version %v on line %v",
				next.Version, next.line, curr.Version, curr.line))
		}
	}

	return errs
}
