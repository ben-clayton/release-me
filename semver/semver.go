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

package semver

import (
	"fmt"
	"regexp"
	"sort"
	"strconv"
)

// Style represents the style used to format the semantic version
type Style struct {
	Prefix    string
	OmitPatch bool
}

var (
	versionRE = regexp.MustCompile(`^(?:\w*-|v)?(\d+)\.(\d+)(?:\.(\d+))?(-\w+)?$`)
	styleRE   = regexp.MustCompile(`^(\w*-|v)?(\d+)\.(\d+)(?:\.(\d+))?(-\w+)?$`)
)

// ParseStyle attempts to parse the semantic version style from s.
func ParseStyle(s string) *Style {
	m := styleRE.FindStringSubmatch(s)
	if len(m) == 0 {
		return nil
	}
	return &Style{
		Prefix:    m[1],
		OmitPatch: m[4] == "",
	}
}

// Format returns the version v formatted using the style.
func (s Style) Format(v Version) string {
	out := fmt.Sprintf("%s%d.%d", s.Prefix, v.Major, v.Minor)
	if v.Patch != 0 || !s.OmitPatch {
		out += fmt.Sprintf(".%d", v.Patch)
	}
	if v.Flavor != "" {
		out += "-" + v.Flavor
	}
	return out
}

// Merge attempts to merge the styles a and b. Returns nil if the styles are
// incompatible.
func Merge(a, b Style) *Style {
	if a.Prefix != b.Prefix {
		return nil
	}
	out := Style{}
	out.Prefix = a.Prefix
	out.OmitPatch = a.OmitPatch || b.OmitPatch
	return &out
}

// Version describes a semantic version.
type Version struct {
	Major  int
	Minor  int
	Patch  int
	Flavor string
}

func (v Version) String() string {
	s := fmt.Sprintf("%d.%d.%d", v.Major, v.Minor, v.Patch)
	if v.Flavor != "" {
		s += "-" + v.Flavor
	}
	return s
}

// Parse parses the Version from the string s.
func Parse(s string) (Version, error) {
	m := versionRE.FindStringSubmatch(s)
	if len(m) == 0 {
		return Version{}, fmt.Errorf("Cannot parse '%v' as a semantic version", s)
	}
	v := Version{}
	var err error
	v.Major, err = strconv.Atoi(m[1])
	if err != nil {
		return Version{}, fmt.Errorf("Failed to parse version major '%v'", m[1])
	}
	v.Minor, err = strconv.Atoi(m[2])
	if err != nil {
		return Version{}, fmt.Errorf("Failed to parse version minor '%v'", m[2])
	}
	if m[3] != "" {
		v.Patch, err = strconv.Atoi(m[3])
		if err != nil {
			return Version{}, fmt.Errorf("Failed to parse version patch '%v'", m[3])
		}
	}
	if len(m[4]) > 0 {
		v.Flavor = m[4][1:]
	}
	return v, nil
}

// MustParse parses the Version from the string s, panicing if there's a parse
// error.
func MustParse(s string) Version {
	v, err := Parse(s)
	if err != nil {
		panic(err)
	}
	return v
}

// Set is a set of unique versions
type Set map[Version]struct{}

// List is a list of versions
type List []Version

// Compare compares two versions, returning:
// -1 if a < b
//  1 if a > b
//  0 if a == b
func Compare(a, b Version, compareFlavor bool) int {
	switch {
	case a.Major < b.Major:
		return -1
	case a.Major > b.Major:
		return 1
	case a.Minor < b.Minor:
		return -1
	case a.Minor > b.Minor:
		return 1
	case a.Patch < b.Patch:
		return -1
	case a.Patch > b.Patch:
		return 1
	default:
		if compareFlavor {
			switch {
			case a.Flavor == "" && b.Flavor != "":
				return -1
			case a.Flavor != "" && b.Flavor == "":
				return 1
			case a.Flavor < b.Flavor:
				return -1
			case a.Flavor > b.Flavor:
				return 1
			}
		}
		return 0
	}

}

// GreaterThan returns true if version o is greater than version v.
func (v Version) GreaterThan(o Version, compareFlavor bool) bool {
	return Compare(v, o, compareFlavor) > 0
}

// GreaterEqualTo returns true if version o is greater than or equal to version
// v.
func (v Version) GreaterEqualTo(o Version, compareFlavor bool) bool {
	return Compare(v, o, compareFlavor) >= 0
}

// Sort sorts the versions starting with the most recent to the oldest.
func (l List) Sort() {
	sort.Slice(l, func(i, j int) bool { return Compare(l[i], l[j], true) > 0 })
}

// Set returns the unique versions in the list.
func (l List) Set() Set {
	set := Set{}
	for _, v := range l {
		set[v] = struct{}{}
	}
	return set
}

// Add adds v to the set s.
func (s Set) Add(v Version) { s[v] = struct{}{} }

// Remove removes v from the set s.
func (s Set) Remove(v Version) { delete(s, v) }

// Contains returns true if the set s contains v.
func (s Set) Contains(v Version) bool { _, found := s[v]; return found }

// Union returns the versions found in s that are also found in o.
func (s Set) Union(o Set) Set {
	out := make(Set, len(s))
	for v := range o {
		if s.Contains(v) {
			out.Add(v)
		}
	}
	return out
}

// Clone returns a shallow copy of this Set.
func (s Set) Clone() Set {
	out := make(Set, len(s))
	for v := range s {
		out.Add(v)
	}
	return out
}

// List returns the sorted versions in this set as a list
func (s Set) List() List {
	list := make(List, 0, len(s))
	for v := range s {
		list = append(list, v)
	}
	list.Sort()
	return list
}
