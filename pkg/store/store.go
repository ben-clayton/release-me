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

// Package store provides an abstract interface for package storage backends.
package store

import (
	"fmt"
	"net/url"

	"github.com/ben-clayton/release-me/pkg"
)

// Factory constructs a Store for the given url.
type Factory func(url url.URL) (Store, error)

// Factories keys by URL scheme
var factories = map[string]Factory{}

// Store is the interface for a package store.
type Store interface {
	// Packages returns all the packages in the store. These are ordered
	// starting with the highest version then most recent.
	Packages() (pkg.InfoList, error)

	// Fetch loads the package with the given info.
	Fetch(pkg.Info) (*pkg.Package, error)
}

// New builds and returns a store for the given URL.
func New(url url.URL) (Store, error) {
	factory, ok := factories[url.Scheme]
	if !ok {
		return nil, fmt.Errorf("No store factory registered for scheme %v", url.Scheme)
	}
	return factory(url)
}

// Register registeres the Factory f for given URL scheme.
func Register(scheme string, f Factory) {
	if _, found := factories[scheme]; found {
		panic(fmt.Errorf("store.Register() called multiple times for scheme '%v'", scheme))
	}
	factories[scheme] = f
}

// ErrPackageNotFound is the error type returned by Store.Fetch when the
// requested package is not found in the store.
type ErrPackageNotFound struct {
	Package pkg.Info
}

func (e ErrPackageNotFound) Error() string {
	return fmt.Sprintf("Package %v not found", e.Package)
}
