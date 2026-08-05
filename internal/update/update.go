// Package update tells a reviewer when a newer peel has been released.
//
// The check is network work in a tool that is otherwise entirely local, so it
// is kept off the review itself: it runs while the session is open and is only
// spoken about once peel has quit, at most once a day, and never for a build
// that did not come from a release.
package update

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// Repo is the GitHub repository releases are published to.
const Repo = "ziadalzarka/peel"

// Command upgrades peel where brew put it, which is how peel is installed.
const Command = "brew upgrade ziadalzarka/tap/peel"

// DisableEnv turns the check off when it is set to anything at all.
const DisableEnv = "PEEL_NO_UPDATE_CHECK"

// Interval is how long one answer from GitHub stands for. A release is not
// urgent news, and asking on every run would spend a request on nothing.
const Interval = 24 * time.Hour

// Timeout bounds the request, so a network that never answers costs a session
// nothing.
const Timeout = 3 * time.Second

// apiURL is the GitHub endpoint naming the most recent release. It is a
// variable so tests can answer it themselves rather than reach the network.
var apiURL = "https://api.github.com/repos/" + Repo + "/releases/latest"

// Checker asks whether a newer peel exists, remembering the answer between
// runs. The zero value works apart from Current, which the caller sets to the
// running build's version.
type Checker struct {
	// Current is the version of the running build.
	Current string
	// CachePath is where the last answer is kept. Empty means the user's cache
	// directory.
	CachePath string
	// Fetch returns the latest released version. Defaults to the GitHub API.
	Fetch func(ctx context.Context) (string, error)
	// Now reads the clock, for the interval. Defaults to time.Now.
	Now func() time.Time
	// Env reads the environment. Defaults to os.Getenv.
	Env func(string) string
}

// Notice is what to tell the reviewer once they have quit, or "" when there is
// nothing to say: no release newer than this build, a build not made from a
// release, the check turned off, or GitHub unreachable. Being unable to answer
// is never worth a word — peel works the same either way.
func (c Checker) Notice(ctx context.Context) string {
	latest, ok := c.Check(ctx)
	if !ok {
		return ""
	}
	return fmt.Sprintf("peel %s is out — you have %s\n  %s", latest, c.Current, Command)
}

// Check reports the latest release when it is newer than the running build.
func (c Checker) Check(ctx context.Context) (string, bool) {
	if c.env(DisableEnv) != "" {
		return "", false
	}
	current, ok := parse(c.Current)
	if !ok {
		// A build from a checkout rather than a release: nothing to compare
		// against, and whoever built it does not need telling.
		return "", false
	}

	latest := c.latest(ctx)
	got, ok := parse(latest)
	if !ok || !got.after(current) {
		return "", false
	}
	return latest, true
}

// latest returns the newest released version, from the cache while it is still
// good and from GitHub otherwise. Every answer is cached, the empty one
// included, so a machine with no network asks once a day rather than every run.
func (c Checker) latest(ctx context.Context) string {
	path := c.cachePath()
	cached, err := readCache(path)
	if err == nil && c.now().Sub(cached.Checked) < Interval {
		return cached.Latest
	}

	ctx, cancel := context.WithTimeout(ctx, Timeout)
	defer cancel()

	latest, err := c.fetch(ctx)
	if err != nil {
		latest = ""
	}
	writeCache(path, cacheFile{Version: cacheVersion, Checked: c.now(), Latest: latest})
	return latest
}

func (c Checker) fetch(ctx context.Context) (string, error) {
	if c.Fetch != nil {
		return c.Fetch(ctx)
	}
	return fetchLatest(ctx)
}

func (c Checker) now() time.Time {
	if c.Now != nil {
		return c.Now()
	}
	return time.Now()
}

func (c Checker) env(key string) string {
	if c.Env != nil {
		return c.Env(key)
	}
	return os.Getenv(key)
}

// cachePath is where the last answer lives: the user's cache directory, not the
// repository, since the release is the same whichever repository is being read.
func (c Checker) cachePath() string {
	if c.CachePath != "" {
		return c.CachePath
	}
	dir, err := os.UserCacheDir()
	if err != nil {
		return ""
	}
	return filepath.Join(dir, "peel", "update.json")
}

// fetchLatest asks GitHub for the tag of the most recent release.
func fetchLatest(ctx context.Context) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, apiURL, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", "peel")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("github: %s", resp.Status)
	}

	var body struct {
		TagName string `json:"tag_name"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&body); err != nil {
		return "", err
	}
	return body.TagName, nil
}

// cacheVersion is the on-disk format of the cache file.
const cacheVersion = 1

type cacheFile struct {
	Version int       `json:"version"`
	Checked time.Time `json:"checked"`
	Latest  string    `json:"latest"`
}

// readCache loads the last answer. A missing, unreadable or unrecognised file
// is simply no answer, which sends the caller to GitHub.
func readCache(path string) (cacheFile, error) {
	if path == "" {
		return cacheFile{}, fs.ErrNotExist
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return cacheFile{}, err
	}
	var f cacheFile
	if err := json.Unmarshal(b, &f); err != nil {
		return cacheFile{}, err
	}
	if f.Version != cacheVersion {
		return cacheFile{}, errors.New("unrecognised cache")
	}
	return f, nil
}

// writeCache records the answer, and fails silently: an update notice is not
// worth an error in front of someone who has just finished a review.
func writeCache(path string, f cacheFile) {
	if path == "" {
		return
	}
	b, err := json.Marshal(f)
	if err != nil {
		return
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return
	}
	os.WriteFile(path, append(b, '\n'), 0o644)
}

// version is a release, as the three numbers a tag carries.
type version [3]int

// parse reads vX.Y.Z. Anything else — "dev", or the describe output a build
// from a checkout carries — is not a release and does not compare.
func parse(s string) (version, bool) {
	parts := strings.Split(strings.TrimPrefix(strings.TrimSpace(s), "v"), ".")
	if len(parts) != 3 {
		return version{}, false
	}
	var v version
	for i, p := range parts {
		n, err := strconv.Atoi(p)
		if err != nil || n < 0 {
			return version{}, false
		}
		v[i] = n
	}
	return v, true
}

func (v version) after(other version) bool {
	for i := range v {
		if v[i] != other[i] {
			return v[i] > other[i]
		}
	}
	return false
}
