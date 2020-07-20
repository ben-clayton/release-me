# `release-me`
Tools for managing [semantic versioned](https://semver.org/) releases of GitHub projects

---

`release-me` is a tool that automates the process of maintaining release branches, tags and
[GitHub releases](https://docs.github.com/en/enterprise/2.13/user/articles/creating-releases).

The tool will detect missing branches release branches, tags and 
[GitHub releases](https://docs.github.com/en/enterprise/2.13/user/articles/creating-releases),
offering to create them, and also provides a one-click process for creating a new release.

## `CHANGES`

The tool expect the project to contain a file at the root of your project with the name `CHANGES`
(or `CHANGES.md`) that contains release notes for your project's semantically versioned releases.

The `CHANGES` file must contain release versions in sorted order, starting with the highest and
most recent version. The `CHANGES` file in the default branch should begin with a pre-release
suffix (e.g. `-dev`).

An example `CHANGES` file [can be seen here](https://github.com/KhronosGroup/SPIRV-Tools/blob/master/CHANGES).

## Usage

`release-me` is written [in Go](https://golang.org/). With go installed, run:

```
go get -u github.com/ben-clayton/release-me
go run github.com/ben-clayton/release-me
```

When you first run `release-me`, you'll be asked to enter your GitHub username and access
token. [Create a token](https://github.com/settings/tokens) with the following permissions:
 - `read:packages, repo`
 - `write:discussion, write:packages`
 
 You'll be offered to save these credentials to your home directory.
 
 The tool will then guide you though the process of creating missing branches or creating a new release.
