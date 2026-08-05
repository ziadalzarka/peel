package update

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// fetcher counts the times GitHub is asked, so the tests can tell a cached
// answer from a fresh one.
type fetcher struct {
	latest string
	err    error
	calls  int
}

func (f *fetcher) fetch(context.Context) (string, error) {
	f.calls++
	return f.latest, f.err
}

// checker builds a Checker that touches nothing outside the test: its own cache
// file, its own clock, and no environment.
func checker(t *testing.T, current string, f *fetcher) Checker {
	t.Helper()
	return Checker{
		Current:   current,
		CachePath: filepath.Join(t.TempDir(), "update.json"),
		Fetch:     f.fetch,
		Now:       func() time.Time { return time.Date(2026, 8, 5, 9, 0, 0, 0, time.UTC) },
		Env:       func(string) string { return "" },
	}
}

func TestNoticeNamesTheReleaseAndHowToGetIt(t *testing.T) {
	f := &fetcher{latest: "v0.5.0"}
	got := checker(t, "v0.4.0", f).Notice(context.Background())

	for _, want := range []string{"v0.5.0", "v0.4.0", Command} {
		if !strings.Contains(got, want) {
			t.Errorf("the notice is %q, want it to name %q", got, want)
		}
	}
}

// Nothing to say is said with nothing: the reviewer has just quit, and a line
// telling them they are up to date is noise.
func TestNoticeIsSilentWithoutANewerRelease(t *testing.T) {
	for _, tc := range []struct{ name, current, latest string }{
		{"same version", "v0.4.0", "v0.4.0"},
		{"older release", "v0.4.0", "v0.3.9"},
		{"github said nothing", "v0.4.0", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := &fetcher{latest: tc.latest}
			if got := checker(t, tc.current, f).Notice(context.Background()); got != "" {
				t.Errorf("the notice is %q, want silence", got)
			}
		})
	}
}

// A build made from a checkout has no release to be behind, and whoever built
// it is not the person to tell about brew.
func TestABuildFromACheckoutIsNeverChecked(t *testing.T) {
	for _, current := range []string{"dev", "v0.4.0-2-gabcdef", "v0.4.0-dirty", "", "v0.4"} {
		f := &fetcher{latest: "v9.9.9"}
		c := checker(t, current, f)

		if got := c.Notice(context.Background()); got != "" {
			t.Errorf("version %q got the notice %q, want silence", current, got)
		}
		if f.calls != 0 {
			t.Errorf("version %q asked github %d times, want none", current, f.calls)
		}
	}
}

func TestTheEnvironmentCanTurnTheCheckOff(t *testing.T) {
	f := &fetcher{latest: "v9.9.9"}
	c := checker(t, "v0.4.0", f)
	c.Env = func(key string) string {
		if key == DisableEnv {
			return "1"
		}
		return ""
	}

	if got := c.Notice(context.Background()); got != "" {
		t.Errorf("the notice is %q, want silence with %s set", got, DisableEnv)
	}
	if f.calls != 0 {
		t.Errorf("asked github %d times with the check off, want none", f.calls)
	}
}

// One answer stands for a day. A release is not urgent news, and asking on
// every run would spend a request to be told the same thing.
func TestGitHubIsAskedOnceADay(t *testing.T) {
	f := &fetcher{latest: "v0.5.0"}
	c := checker(t, "v0.4.0", f)
	now := time.Date(2026, 8, 5, 9, 0, 0, 0, time.UTC)
	c.Now = func() time.Time { return now }

	for i := 0; i < 3; i++ {
		if got := c.Notice(context.Background()); got == "" {
			t.Fatalf("run %d said nothing, want the notice from the cache", i)
		}
	}
	if f.calls != 1 {
		t.Errorf("asked github %d times within the day, want once", f.calls)
	}

	now = now.Add(Interval + time.Minute)
	f.latest = "v0.6.0"
	if got := c.Notice(context.Background()); !strings.Contains(got, "v0.6.0") {
		t.Errorf("the notice a day later is %q, want the release fetched again", got)
	}
	if f.calls != 2 {
		t.Errorf("asked github %d times in all, want twice", f.calls)
	}
}

// A machine with no network must not retry on every run: the failure is cached
// like any other answer.
func TestAFailedCheckWaitsUntilTomorrow(t *testing.T) {
	f := &fetcher{err: errors.New("no route to host")}
	c := checker(t, "v0.4.0", f)

	if got := c.Notice(context.Background()); got != "" {
		t.Errorf("the notice is %q, want silence when github cannot be reached", got)
	}
	if got := c.Notice(context.Background()); got != "" {
		t.Errorf("the second notice is %q, want silence", got)
	}
	if f.calls != 1 {
		t.Errorf("asked github %d times after a failure, want once", f.calls)
	}
}

// A cache peel cannot read is no worse than no cache at all.
func TestAnUnreadableCacheIsJustAsk(t *testing.T) {
	f := &fetcher{latest: "v0.5.0"}
	c := checker(t, "v0.4.0", f)
	writeCache(c.CachePath, cacheFile{Version: cacheVersion + 1, Checked: c.now(), Latest: "v9.9.9"})

	if got := c.Notice(context.Background()); !strings.Contains(got, "v0.5.0") {
		t.Errorf("the notice is %q, want the release fetched fresh", got)
	}
}

func TestFetchReadsTheReleaseTag(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Write([]byte(`{"tag_name": "v0.5.0", "name": "peel v0.5.0"}`))
	}))
	defer srv.Close()

	defer swapAPI(t, srv.URL)()
	got, err := fetchLatest(context.Background())
	if err != nil {
		t.Fatalf("fetchLatest: %v", err)
	}
	if got != "v0.5.0" {
		t.Errorf("fetchLatest returned %q, want v0.5.0", got)
	}
}

func TestFetchFailsOnARefusal(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "rate limited", http.StatusForbidden)
	}))
	defer srv.Close()

	defer swapAPI(t, srv.URL)()
	if _, err := fetchLatest(context.Background()); err == nil {
		t.Error("fetchLatest returned no error on a refusal")
	}
}

// swapAPI points the checker at a test server and returns the undo.
func swapAPI(t *testing.T, url string) func() {
	t.Helper()
	old := apiURL
	apiURL = url
	return func() { apiURL = old }
}
