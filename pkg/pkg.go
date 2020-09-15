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

// Package pkg contains types and methods for building release package files.
package pkg

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"io/ioutil"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/ben-clayton/release-me/changes"
	"github.com/ben-clayton/release-me/git"
	"github.com/ben-clayton/release-me/match"
	"github.com/ben-clayton/release-me/semver"
)

// Config is used to control what build artifacts are included into a package.
type Config struct {
	// Name of the project
	Name string
	// Files is a list of file paths relative to the build directory to include
	// in the package. May include globs.
	Files []string
	// Package type (zip, tar, etc)
	Type Type
}

func (c *Config) load(path string) error {
	// Read the config
	file, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("Failed to open config file: %w", err)
	}
	defer file.Close()
	d := json.NewDecoder(file)
	if err := d.Decode(c); err != nil {
		return fmt.Errorf("Failed to decode the config file: %w", err)
	}
	return nil
}

// filter returns a predicate that returns true if the file at path should
// be included in a package.
func (c *Config) filter() (func(string) bool, error) {
	tests := make([]match.Test, len(c.Files))
	for i, f := range c.Files {
		test, err := match.New(f)
		if err != nil {
			return nil, fmt.Errorf("Config contains an invalid file path / glob '%v': %w", f, err)
		}
		tests[i] = test
	}
	return func(path string) bool {
		for _, test := range tests {
			if test(path) {
				return true
			}
		}
		return false
	}, nil
}

// Info holds information about a build package.
type Info struct {
	Name    string         // Build package name (usually project name)
	Version semver.Version // Version of the build
	SHA     string         // Source code Git SHA (optional)
	Type    Type           // Package type (zip, tar, etc)
}

// Validate verifies that all of the fields of i are legal.
func (i Info) Validate() error {
	if strings.Contains(i.Name, "--") {
		return fmt.Errorf("Name must not contain '--'")
	}
	_, err := hex.DecodeString(i.SHA)
	if err != nil {
		return fmt.Errorf("SHA is not a hexadecimal string")
	}
	switch i.Type {
	case Zip, Tar:
	default:
		return fmt.Errorf("Unknown package type %v", i.Type)
	}
	return nil
}

// Canonical returns the canonicalized package file name from the info i, taking
// the form:
// <name>--<major>.<minor>.<patch>[-flavour][--<short-sha>].ext
func (i Info) Canonical() (string, error) {
	if err := i.Validate(); err != nil {
		return "", err
	}
	return i.String(), nil
}

func (i Info) String() string {
	parts := []string{i.Name, i.Version.String()}
	if i.SHA != "" {
		parts = append(parts, ShortSHA(i.SHA))
	}
	ext := ""
	switch i.Type {
	case Zip:
		ext = ".zip"
	case Tar:
		ext = ".tar"
	}
	return strings.Join(parts, "--") + ext
}

// InfoList is a slice of Infos
type InfoList []Info

// Filter returns a new InfoList with only the Infos that when passed the
// predicate.
func (l InfoList) Filter(pred func(Info) bool) InfoList {
	out := make(InfoList, 0, len(l))
	for _, i := range l {
		if pred(i) {
			out = append(out, i)
		}
	}
	return out
}

// Type is an enumerator of package storage types
type Type int

// Possible package types
const (
	Tar Type = iota
	Zip
)

var extToType = map[string]Type{
	".zip": Zip,
	".tar": Tar,
}

// UnmarshalJSON unmarshals the Type from a JSON field.
func (t *Type) UnmarshalJSON(json []byte) error {
	str := strings.Trim(strings.ToLower(string(json)), `"`)
	switch str {
	case "tar":
		*t = Tar
	case "zip":
		*t = Zip
	default:
		return fmt.Errorf("Unsupported package type '%v'", str)
	}
	return nil
}

// File represents a single file in a package
type File struct {
	Path string      // File path within package
	Data []byte      // File data (if not symlink)
	Link string      // Symlink target path (if not regular file)
	Mode os.FileMode // File mode
}

// Package is a collection of files
type Package struct {
	Info  Info
	Files []File
}

// Save stores all the files in the package to the given directory.
func (p Package) Save(dir string) error {
	for _, file := range p.Files {
		path := filepath.Join(dir, file.Path)
		if err := os.MkdirAll(filepath.Dir(path), 0777); err != nil {
			return fmt.Errorf("Failed to create directory '%v'", err)
		}
		if file.Link != "" {
			if err := os.Symlink(file.Link, path); err != nil {
				return fmt.Errorf("Failed to create symlink from '%v' to '%v': %w", path, file.Link, err)
			}
		} else {
			if err := ioutil.WriteFile(path, file.Data, file.Mode); err != nil {
				return fmt.Errorf("Failed to create symlink from '%v' to '%v': %w", path, file.Link, err)
			}
		}
	}
	return nil
}

// ShortSHA returns sha truncated to at most 6 bytes.
func ShortSHA(sha string) string {
	if len(sha) > 6 {
		return sha[:6]
	}
	return sha
}

var parseRE = regexp.MustCompile("")

// Parse parses the canonicalized package file name, returning an Info.
func Parse(path string) (Info, error) {
	_, name := filepath.Split(path)
	ext := filepath.Ext(name)
	noext := name[:len(name)-len(ext)]
	parts := strings.Split(noext, "--")
	out := Info{}

	ty, ok := extToType[ext]
	if !ok {
		return Info{}, fmt.Errorf("Unsupported package extension'%s'", ext)
	}
	out.Type = ty

	// [--<short-sha>] is optional.
	if len(parts) < 2 || len(parts) > 3 {
		return Info{}, fmt.Errorf("'%v' is not a package name", name)
	}

	out.Name = parts[0]

	version, err := semver.Parse(parts[1])
	if err != nil {
		return Info{}, fmt.Errorf("Failed to parse package version from '%s': %w", name, err)
	}
	out.Version = version

	if len(parts) > 2 {
		out.SHA = parts[len(parts)-1]
	}

	return out, nil
}

// Create builds a package in outDir using the config at cfgPath, source
// checkout at srcDir and build artifacts at buildDir.
func Create(cfgPath, srcDir, buildDir, outDir string) (string, error) {
	// Load the config
	cfg := Config{}
	if err := cfg.load(cfgPath); err != nil {
		return "", err
	}

	// Build the file filter prediacate
	filter, err := cfg.filter()
	if err != nil {
		return "", err
	}

	// Gather all the artifacts to include in the package
	files, err := gatherFiles(buildDir, filter)
	if err != nil {
		return "", err
	}

	if len(files) == 0 {
		return "", fmt.Errorf("No files found for package")
	}

	// Determine the current git SHA
	sha, err := gitHash(srcDir)
	if err != nil {
		sha = "" // SHA is optional
	}

	// Load the changes so we can examine the version
	chg, err := changes.Load(srcDir)
	if err != nil {
		return "", err
	}

	name, err := Info{
		Name:    cfg.Name,
		Version: chg.CurrentVersion(),
		SHA:     sha,
		Type:    cfg.Type,
	}.Canonical()
	if err != nil {
		return "", err
	}

	// Create the output package file
	outPath := filepath.Join(outDir, name)
	out, err := os.Create(outPath)
	if err != nil {
		return "", fmt.Errorf("Failed to create output package file")
	}
	defer out.Close()

	var writer func(out io.Writer, root string, files []string) error
	switch cfg.Type {
	case Zip:
		writer = zipFiles
	case Tar:
		writer = tarFiles
	default:
		return "", fmt.Errorf("Unknown package type %v", cfg.Type)
	}

	if err := writer(out, buildDir, files); err != nil {
		return "", err
	}

	return outPath, nil
}

// Load loads the package at path, returning the package info and file content.
func Load(path string) (*Package, error) {
	info, err := Parse(path)
	if err != nil {
		return nil, err
	}

	var loader func(path string) ([]File, error)
	switch info.Type {
	case Zip:
		loader = unzipFiles
	case Tar:
		loader = untarFiles
	default:
		return nil, fmt.Errorf("Unknown package type %v", info.Type)
	}

	files, err := loader(path)
	if err != nil {
		return nil, err
	}

	return &Package{info, files}, nil
}

func unzipFiles(path string) ([]File, error) {
	files := []File{}
	zf, err := zip.OpenReader(path)
	if err != nil {
		return nil, fmt.Errorf("Failed to open package file '%v': %w", path, err)
	}
	defer zf.Close()

	read := func(f *zip.File) error {
		r, err := f.Open()
		if err != nil {
			return fmt.Errorf("Failed to open package '%v' embedded file '%v': %w", path, f.Name, err)
		}
		defer r.Close()

		data, err := ioutil.ReadAll(r)
		if err != nil {
			return fmt.Errorf("Failed to read package '%v' embedded file '%v': %w", path, f.Name, err)
		}

		files = append(files, File{Path: f.Name, Data: data})
		return nil
	}

	for _, f := range zf.File {
		if err := read(f); err != nil {
			return nil, err
		}
	}
	return files, nil
}

func untarFiles(path string) ([]File, error) {
	files := []File{}
	fr, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("Failed to open package file '%v': %w", path, err)
	}

	gzr, err := gzip.NewReader(fr)
	if err != nil {
		return nil, fmt.Errorf("Failed to open package file '%v': %w", path, err)
	}
	defer gzr.Close()

	tr := tar.NewReader(gzr)

	for {
		header, err := tr.Next()
		switch err {
		case nil:
			break
		case io.EOF:
			return files, nil
		default:
			return nil, fmt.Errorf("Failed to open package file '%v': %w", path, err)
		}

		switch header.Typeflag {
		case tar.TypeReg:
			buf := bytes.NewBuffer(make([]byte, 0, header.Size))
			if _, err := io.Copy(buf, tr); err != nil {
				return nil, fmt.Errorf("Failed to open package '%v' embedded file '%v': %w", path, header.Name, err)
			}
			files = append(files, File{Path: header.Name, Data: buf.Bytes(), Mode: os.FileMode(header.Mode)})
		case tar.TypeSymlink:
			files = append(files, File{Path: header.Name, Link: header.Linkname, Mode: os.FileMode(header.Mode)})
		case tar.TypeDir: // Ignored
		default:
			return nil, fmt.Errorf("Unhandled tar file type %v", header.Typeflag)
		}
	}
}

func gitHash(srcDir string) (string, error) {
	g, err := git.New()
	if err != nil {
		return "", err
	}
	cl, err := g.HeadCL(srcDir)
	if err != nil {
		return "", fmt.Errorf("Couldn't determine git hash for path '%s': %w", srcDir, err)
	}
	return cl.Hash.String(), nil
}

// gatherFiles walks all files and subdirectories from root, returning those
// that pred returns true for.
func gatherFiles(root string, pred func(string) bool) ([]string, error) {
	files := []string{}
	err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		rel, err := filepath.Rel(root, path)
		if err != nil {
			rel = path
		}
		if !info.IsDir() && pred(rel) {
			files = append(files, rel)
		}
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("Failed to walk files at '%v': %w", root, err)
	}
	return files, nil
}

func copyFile(dst io.Writer, file string) error {
	src, err := os.Open(file)
	if err != nil {
		return fmt.Errorf("Failed to open file '%s': %w", file, err)
	}
	defer src.Close()
	if _, err := io.Copy(dst, src); err != nil {
		return fmt.Errorf("Failed to copy file '%s': %w", file, err)
	}
	return nil
}

func zipFiles(out io.Writer, root string, files []string) error {
	zw := zip.NewWriter(out)
	defer zw.Close()

	for _, file := range files {
		path := filepath.Join(root, file)
		dst, err := zw.Create(file)
		if err != nil {
			return fmt.Errorf("Failed to create file '%s' in package zip: %w", file, err)
		}
		if err := copyFile(dst, path); err != nil {
			return err
		}
	}
	return nil
}

func tarFiles(out io.Writer, root string, files []string) error {
	gzw := gzip.NewWriter(out)
	defer gzw.Close()

	tw := tar.NewWriter(gzw)
	defer tw.Close()

	for _, file := range files {
		path := filepath.Join(root, file)

		info, err := os.Lstat(path)
		if err != nil {
			return fmt.Errorf("Failed to call Stat() on artifact file '%s'", path)
		}

		header, err := tar.FileInfoHeader(info, file)
		if err != nil {
			return fmt.Errorf("FileInfoHeader() for artifact file '%v' returned %w", file, err)
		}
		header.Name = file // abs -> rel

		if info.Mode()&os.ModeSymlink != 0 {
			// Symlink
			absTarget, err := filepath.EvalSymlinks(path)
			if err != nil {
				return fmt.Errorf("Failed to read artifact symlink '%s' target", file)
			}
			if !strings.HasPrefix(absTarget, root) {
				return fmt.Errorf("Artifact file '%s' is a symlink to a file outside of the build root (%v)", file, absTarget)
			}
			relTarget, err := filepath.Rel(filepath.Dir(path), absTarget)
			if err != nil {
				return fmt.Errorf("Failed to get symlink target relative path for '%s'", file)
			}
			header.Linkname = relTarget
			if err := tw.WriteHeader(header); err != nil {
				return fmt.Errorf("Failed to write tar header for artifact file '%v': %w", file, err)
			}
		} else {
			// Regular file
			if err := tw.WriteHeader(header); err != nil {
				return fmt.Errorf("Failed to write tar header for artifact file '%v': %w", file, err)
			}
			if err := copyFile(tw, path); err != nil {
				return err
			}
		}
	}
	return nil
}
