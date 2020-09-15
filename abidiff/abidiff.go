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

// Package abidiff provides functions for interacting with abidiff.
package abidiff

import (
	"context"
	"fmt"
	"os/exec"
	"regexp"
	"strings"
	"time"
)

const (
	abidiffTimeout = time.Minute * 15 // timeout for a abidiff operation
)

// Abidiff provides functions for interacting with abidiff
type Abidiff struct {
	exe string
}

// New looks up the abidiff exectable and returns a new abidiff
func New() (*Abidiff, error) {
	path, err := exec.LookPath("abidiff")
	if err != nil {
		return nil, fmt.Errorf("Couldn't find path to abidiff executable")
	}
	return &Abidiff{path}, nil
}

// Diff describes the ABI differences between two shared objects.
type Diff struct {
	Result DiffType
	Output string
}

// DiffType is an enumerator of general difference types found.
type DiffType int

// The possible enumerator values for DiffType
const (
	// There were no public ABI changes found.
	NoABIChanges = DiffType(iota)
	// There were public ABI changes found, but are backwards compatible.
	CompatibleABIChanges
	// There were non-backwards compatible public ABI changes found.
	IncompatibleABIChanges
)

func (d DiffType) String() string {
	switch d {
	case NoABIChanges:
		return "<no-abi-changes>"
	case CompatibleABIChanges:
		return "<compatible-abi-changes>"
	case IncompatibleABIChanges:
		return "<incompatible-abi-changes>"
	default:
		return fmt.Sprintf("%v", int(d))
	}
}

var (
	reDataMemberChanged = regexp.MustCompile("[0-9]+ data member change[s]?:")
	reBaseClassChange   = regexp.MustCompile("[0-9]+ base class (deletion|insertion)")
)

// Diff shells out to 'abidiff oldSO newSO' and returns the results.
func (a Abidiff) Diff(oldSO, newSO string) (*Diff, error) {
	ctx, cancel := context.WithTimeout(context.Background(), abidiffTimeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, a.exe, "--fail-no-debug-info", oldSO, newSO)
	out, err := cmd.CombinedOutput()

	switch err := err.(type) {
	case nil:
		return &Diff{}, nil
	case *exec.ExitError:
		const ( // https://sourceware.org/libabigail/manual/abidiff.html#return-values
			ErrorBit              = 1
			UsageError            = 2
			ChangeBit             = 4
			IncompatibleChangeBit = 8

			AllBits = ErrorBit | UsageError | ChangeBit | IncompatibleChangeBit
		)

		res := err.ExitCode()
		errs := []string{}
		if res&ErrorBit != 0 {
			errs = append(errs, "ABIDIFF_ERROR")
		}
		if res&UsageError != 0 {
			errs = append(errs, "ABIDIFF_USAGE_ERROR")
		}
		if res&^AllBits != 0 {
			errs = append(errs, fmt.Sprintf("0x%x", res))
		}
		if len(errs) > 0 {
			return nil, fmt.Errorf("abidiff errored with: %v\nOutput: %v", strings.Join(errs, ", "), string(out))
		}

		d := Diff{Result: NoABIChanges, Output: string(out)}
		switch {
		case res&IncompatibleChangeBit != 0:
			d.Result = IncompatibleABIChanges
		case res&ChangeBit != 0:
			d.Result = CompatibleABIChanges

			// Be stricter about changes that abidiff considers compatible, but
			// will likely cause API breakages.
			for _, re := range []*regexp.Regexp{
				reDataMemberChanged,
				reBaseClassChange,
			} {
				if len(re.Find(out)) > 0 {
					d.Result = IncompatibleABIChanges
					break
				}
			}
		}
		return &d, nil
	default:
		return nil, fmt.Errorf("abidiff returned with error %w", err)
	}
}
