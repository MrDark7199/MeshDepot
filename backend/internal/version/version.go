// Package version knows which MeshDepot is running and whether a newer one has
// been released on GitHub.
package version

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"meshdepot/internal/logx"
	"meshdepot/internal/safego"
)

// Current is the running version. The about page and the update check both read
// it, so a release changes this line and nothing else.
const Current = "1.5.2"

const (
	latestReleaseAddress = "https://api.github.com/repos/MrDark7199/MeshDepot/releases/latest"
	releasePagePrefix    = "https://github.com/MrDark7199/MeshDepot/"
	checkInterval        = 6 * time.Hour
	maxAnswerBytes       = 1 << 20
)

// Release is the newest published release: drafts and pre-releases are not
// what /releases/latest answers with.
type Release struct {
	Version string
	URL     string
}

// Checker asks GitHub for the newest release now and then and keeps the answer,
// so a page load reads memory instead of reaching out.
type Checker struct {
	address string
	client  *http.Client

	mutex  sync.RWMutex
	latest Release
}

func NewChecker() *Checker {
	return &Checker{address: latestReleaseAddress, client: &http.Client{Timeout: 15 * time.Second}}
}

// Start checks right away and then every few hours, until stop closes.
func (checker *Checker) Start(stop <-chan struct{}) {
	safego.Go("version-check", func() {
		ticker := time.NewTicker(checkInterval)
		defer ticker.Stop()
		for {
			safego.Run("version-check", checker.Check)
			select {
			case <-stop:
				return
			case <-ticker.C:
			}
		}
	})
}

// Check reads the newest release. A failed check keeps the previous answer: an
// instance without internet access simply never shows a notice.
func (checker *Checker) Check() {
	release, failure := checker.fetch()
	if failure != nil {
		logx.Warnf("[version] the newest release could not be read from GitHub: %v", failure)
		return
	}
	checker.mutex.Lock()
	checker.latest = release
	checker.mutex.Unlock()
}

// Latest is the newest release seen so far; empty before the first good check.
func (checker *Checker) Latest() Release {
	checker.mutex.RLock()
	defer checker.mutex.RUnlock()
	return checker.latest
}

func (checker *Checker) fetch() (Release, error) {
	request, failure := http.NewRequest(http.MethodGet, checker.address, nil)
	if failure != nil {
		return Release{}, failure
	}
	request.Header.Set("Accept", "application/vnd.github+json")
	request.Header.Set("User-Agent", "MeshDepot/"+Current)
	response, failure := checker.client.Do(request)
	if failure != nil {
		return Release{}, failure
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return Release{}, fmt.Errorf("HTTP %d", response.StatusCode)
	}
	var answer struct {
		TagName string `json:"tag_name"`
		HTMLURL string `json:"html_url"`
	}
	if failure := json.NewDecoder(io.LimitReader(response.Body, maxAnswerBytes)).Decode(&answer); failure != nil {
		return Release{}, failure
	}
	if answer.TagName == "" {
		return Release{}, fmt.Errorf("the answer names no release")
	}
	release := Release{Version: strings.TrimPrefix(answer.TagName, "v")}
	// Only a page of this repository becomes a link in the interface.
	if strings.HasPrefix(answer.HTMLURL, releasePagePrefix) {
		release.URL = answer.HTMLURL
	}
	return release, nil
}

// Newer reports whether candidate is a later version than current. Versions are
// dot-separated numbers, a leading "v" is ignored, a missing part counts as 0,
// and a candidate that is not a version at all is never newer.
func Newer(candidate, current string) bool {
	candidateParts, candidateValid := parts(candidate)
	currentParts, currentValid := parts(current)
	if !candidateValid || !currentValid {
		return false
	}
	for index := 0; index < len(candidateParts) || index < len(currentParts); index++ {
		candidatePart, currentPart := partAt(candidateParts, index), partAt(currentParts, index)
		if candidatePart != currentPart {
			return candidatePart > currentPart
		}
	}
	return false
}

func parts(version string) ([]int, bool) {
	version = strings.TrimPrefix(strings.TrimSpace(version), "v")
	if version == "" {
		return nil, false
	}
	var numbers []int
	for _, part := range strings.Split(version, ".") {
		number, failure := strconv.Atoi(part)
		if failure != nil || number < 0 {
			return nil, false
		}
		numbers = append(numbers, number)
	}
	return numbers, true
}

func partAt(numbers []int, index int) int {
	if index < len(numbers) {
		return numbers[index]
	}
	return 0
}
