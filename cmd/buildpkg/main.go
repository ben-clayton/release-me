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

// buildpkg zips together build artifacts into a zip file with a semantically
// versioned name.
package main

import (
	"flag"
	"fmt"
	"os"

	"github.com/ben-clayton/release-me/pkg"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "%s\n", err)
		os.Exit(1)
	}
}

func run() error {
	srcDir := flag.String("src", "", "Project source root directory")
	buildDir := flag.String("build", "", "Project build directory")
	cfgPath := flag.String("cfg", "", "Package config directory")
	outDir := flag.String("out", cwd(), "Package output directory")
	flag.Parse()

	for _, arg := range []string{"src", "build", "cfg", "out"} {
		if flag.Lookup(arg).Value.String() == "" {
			return fmt.Errorf("Argument --%v must be set", arg)
		}
	}

	pkgPath, err := pkg.Create(*cfgPath, *srcDir, *buildDir, *outDir)
	if err != nil {
		return err
	}

	fmt.Printf("Package created at: %v\n", pkgPath)

	return nil
}

func cwd() string {
	dir, err := os.Getwd()
	if err != nil {
		return ""
	}
	return dir
}
