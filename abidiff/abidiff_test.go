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

package abidiff_test

import (
	"fmt"
	"io/ioutil"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"testing"

	"github.com/ben-clayton/release-me/abidiff"
)

type compiler struct {
	exe string
}

func newCompiler() (*compiler, error) {
	if clang, err := exec.LookPath("clang"); err == nil {
		return &compiler{clang}, nil
	}
	if gcc, err := exec.LookPath("gcc"); err == nil {
		return &compiler{gcc}, nil
	}
	return nil, fmt.Errorf("Could not find a C++ compiler")
}

func (c compiler) compile(cpp, so string) error {
	cmd := exec.Command(c.exe, "-g", "-O0", "-shared", "-o", so, cpp)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("Compilation failed with error %w: %v", err, string(out))
	}
	return nil
}

func build(c *compiler, wd, src, name string) (string, error) {
	header := `
#define EXPORT __attribute__((visibility("default")))
#define NO_EXPORT __attribute__((visibility("hidden")))
`
	dir := filepath.Join(filepath.Join(wd, name))
	if err := os.MkdirAll(dir, 0777); err != nil {
		return "", fmt.Errorf("os.MkdirAll() failed: %w", err)
	}
	cppPath := filepath.Join(dir, "test.cpp")
	if err := ioutil.WriteFile(cppPath, []byte(header+src), 0666); err != nil {
		return "", fmt.Errorf("ioutil.WriteFile() failed: %w", err)
	}
	soPath := filepath.Join(dir, "out.so")
	if err := c.compile(cppPath, soPath); err != nil {
		return "", err
	}
	return soPath, nil
}

type test struct {
	name     string
	oldSrc   string
	newSrc   string
	expected abidiff.DiffType
}

func (t test) run(testIdx int, d *abidiff.Abidiff, c *compiler, wd string) error {
	oldSoPath, err := build(c, wd, t.oldSrc, fmt.Sprintf("test_old_%d", testIdx))
	if err != nil {
		return fmt.Errorf("build() failed with %v", err)
	}
	newSoPath, err := build(c, wd, t.newSrc, fmt.Sprintf("test_new_%d", testIdx))
	if err != nil {
		return fmt.Errorf("build() failed with %v", err)
	}
	diff, err := d.Diff(oldSoPath, newSoPath)
	if err != nil {
		return fmt.Errorf("Diff() returned error: %v", err)
	}
	if t.expected != diff.Result {
		return fmt.Errorf("Diff() gave unexpected result. Expected: %v, Got: %v.\nOutput: %v",
			t.expected, diff.Result, diff.Output)
	}
	return nil
}

func TestAbidiff(t *testing.T) {
	d, err := abidiff.New()
	if err != nil {
		t.Skip(err)
	}

	c, err := newCompiler()
	if err != nil {
		t.Skip(err)
	}

	// Create a temporary directory for source files and built shared objects
	tmp, err := ioutil.TempDir("", "abidiff-tests")
	if err != nil {
		t.Fatalf("ioutil.TempDir() returned %v", err)
	}
	defer os.RemoveAll(tmp)

	tests := []test{
		{
			name:     "Function no change",
			oldSrc:   `void f(int i, float f, bool b) {}`,
			newSrc:   `void f(int i, float f, bool b) {}`,
			expected: abidiff.NoABIChanges,
		}, {
			name:     "Struct no change",
			oldSrc:   `struct S { int i; float f; bool b; };`,
			newSrc:   `struct S { int i; float f; bool b; };`,
			expected: abidiff.NoABIChanges,
		}, {
			name:     "Struct method no change",
			oldSrc:   `struct S { void M(); }; void S::M() {}`,
			newSrc:   `struct S { void M(); }; void S::M() {}`,
			expected: abidiff.NoABIChanges,
		}, {
			name:     "Public function removed",
			oldSrc:   `void f(int i, float f, bool b) {}`,
			newSrc:   ``,
			expected: abidiff.IncompatibleABIChanges,
		}, {
			name:     "Private function removed",
			oldSrc:   `NO_EXPORT void f(int i, float f, bool b) {}`,
			newSrc:   ``,
			expected: abidiff.NoABIChanges,
		}, {
			name:     "Public struct removed",
			oldSrc:   `struct S { int i; float f; }; void f(S*) {}`,
			newSrc:   ``,
			expected: abidiff.IncompatibleABIChanges,
		}, {
			name:     "Private struct removed",
			oldSrc:   `NO_EXPORT struct S { int i; float f; }; NO_EXPORT void f(S*) {}`,
			newSrc:   ``,
			expected: abidiff.NoABIChanges,
		}, {
			name:     "Public struct field added (at end)",
			oldSrc:   `struct S { int i; float f; }; void f(S*) {}`,
			newSrc:   `struct S { int i; float f; bool b; }; void f(S*) {}`,
			expected: abidiff.CompatibleABIChanges,
		}, {
			name:     "Private struct field added (at end)",
			oldSrc:   `struct S { int i; float f; }; NO_EXPORT void f(S*) {}`,
			newSrc:   `struct S { int i; float f; bool b; }; NO_EXPORT void f(S*) {}`,
			expected: abidiff.NoABIChanges,
		}, {
			name:     "Public struct field added (at start)",
			oldSrc:   `struct S { int i; float f; }; void f(S*) {}`,
			newSrc:   `struct S { bool b; int i; float f; }; void f(S*) {}`,
			expected: abidiff.IncompatibleABIChanges,
		}, {
			name:     "Private struct field added (at start)",
			oldSrc:   `struct S { int i; float f; }; NO_EXPORT void f(S*) {}`,
			newSrc:   `struct S { bool b; int i; float f; }; NO_EXPORT void f(S*) {}`,
			expected: abidiff.NoABIChanges,
		}, {
			name:     "Public struct field added (at mid)",
			oldSrc:   `struct S { int i; float f; }; void f(S*) {}`,
			newSrc:   `struct S { int i; bool b; float f; }; void f(S*) {}`,
			expected: abidiff.IncompatibleABIChanges,
		}, {
			name:     "Private struct field added (at mid)",
			oldSrc:   `struct S { int i; float f; }; NO_EXPORT void f(S*) {}`,
			newSrc:   `struct S { int i; bool b; float f; }; NO_EXPORT void f(S*) {}`,
			expected: abidiff.NoABIChanges,
		}, {
			name:     "Public struct base renamed (same fields)",
			oldSrc:   `struct B{ int x; }; struct S : B { int i; }; void f(S*) {}`,
			newSrc:   `struct C{ int x; }; struct S : C { int i; }; void f(S*) {}`,
			expected: abidiff.IncompatibleABIChanges,
		}, {
			name:     "Private struct base renamed (same fields)",
			oldSrc:   `struct B{ int x; }; struct S : B { int i; }; NO_EXPORT void f(S*) {}`,
			newSrc:   `struct C{ int x; }; struct S : C { int i; }; NO_EXPORT void f(S*) {}`,
			expected: abidiff.NoABIChanges,
		}, {
			name:     "Public struct base renamed (different fields)",
			oldSrc:   `struct B{ float x; }; struct S : B { int i; }; void f(S*) {}`,
			newSrc:   `struct C{ int x; }; struct S : C { int i; }; void f(S*) {}`,
			expected: abidiff.IncompatibleABIChanges,
		}, {
			name:     "Private struct base renamed (different fields)",
			oldSrc:   `struct B{ float x; }; struct S : B { int i; }; NO_EXPORT void f(S*) {}`,
			newSrc:   `struct C{ int x; }; struct S : C { int i; }; NO_EXPORT void f(S*) {}`,
			expected: abidiff.NoABIChanges,
		}, {
			name:     "Public struct base change",
			oldSrc:   `struct B{ int   x; }; struct S : B { int i; }; void f(S*) {}`,
			newSrc:   `struct B{ float y; }; struct S : B { int i; }; void f(S*) {}`,
			expected: abidiff.IncompatibleABIChanges,
		}, {
			name:     "Private struct base change",
			oldSrc:   `struct B{ int   x; }; struct S : B { int i; }; NO_EXPORT void f(S*) {}`,
			newSrc:   `struct B{ float y; }; struct S : B { int i; }; NO_EXPORT void f(S*) {}`,
			expected: abidiff.NoABIChanges,
		}, {
			name:     "Public struct method added",
			oldSrc:   `struct S{};`,
			newSrc:   `struct S{ void M(); }; void S::M() {}`,
			expected: abidiff.CompatibleABIChanges,
		}, {
			name:     "Private struct method added",
			oldSrc:   `struct S{};`,
			newSrc:   `struct S{ NO_EXPORT void M(); }; void S::M() {}`,
			expected: abidiff.NoABIChanges,
		}, {
			name:     "Public struct method removed",
			oldSrc:   `struct S{ void M(); }; void S::M() {}`,
			newSrc:   `struct S{};`,
			expected: abidiff.IncompatibleABIChanges,
		}, {
			name:     "Private struct method removed",
			oldSrc:   `struct S{ NO_EXPORT void M(); }; void S::M() {}`,
			newSrc:   `struct S{};`,
			expected: abidiff.NoABIChanges,
		},
	}

	errs := make([]error, len(tests))
	wg := sync.WaitGroup{}
	wg.Add(len(tests))
	for i, test := range tests {
		i, test := i, test
		go func() {
			defer wg.Done()
			if err := test.run(i, d, c, tmp); err != nil {
				errs[i] = err
			}
		}()
	}
	wg.Wait()

	for i, err := range errs {
		if err != nil {
			t.Errorf("Test '%v' failed: %v", tests[i].name, err)
		}
	}
}
