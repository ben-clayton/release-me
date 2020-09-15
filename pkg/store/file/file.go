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

// Package file provides a local file based implementation of the package store
// interface.
package file

import (
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"sort"

	"github.com/ben-clayton/release-me/pkg"
	"github.com/ben-clayton/release-me/pkg/store"
)

func init() {
	store.Register("file", func(url url.URL) (store.Store, error) {
		return New(url.Path)
	})
}

// New builds and returns a file store for the given URL.
func New(root string) (store.Store, error) {
	infos := pkg.InfoList{}
	paths := map[pkg.Info]string{}
	err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if info, err := pkg.Parse(path); err == nil {
			infos = append(infos, info)
			paths[info] = path
		}
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("Failed to walk files at '%v': %w", root, err)
	}

	sort.Slice(infos, func(i, j int) bool {
		return infos[i].Version.GreaterThan(infos[j].Version, true)
	})

	return filestore{infos, paths}, nil
}

type filestore struct {
	infos pkg.InfoList
	paths map[pkg.Info]string
}

func (s filestore) Packages() (pkg.InfoList, error) {
	return s.infos, nil
}

func (s filestore) Fetch(info pkg.Info) (*pkg.Package, error) {
	path, ok := s.paths[info]
	if !ok {
		return nil, store.ErrPackageNotFound{Package: info}
	}
	return pkg.Load(path)
}
