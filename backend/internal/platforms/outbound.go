package platforms

// Outbound request accounting.
//
// Every request that leaves the process for a platform is counted here. Nothing
// is blocked yet: the point of this phase is to learn what one download really
// costs. The download cooldown paces whole jobs, while a single job turns out to
// issue a metadata call, a full browser page load with all its subresources, a
// presigned URL lookup per instance and the file transfer - so "300 seconds
// between jobs" says nothing about how many requests leave the process.
//
// The recorder is package-level state on purpose. fetch(), streamDownload() and
// the browser callback are package-level too, and threading a dependency through
// every one of the thirty-odd call sites for a counter would be a poor trade.

import (
	"database/sql"
	"log"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"meshdepot/internal/safego"
)

// requestKind classifies an outbound request in the statistics.
type requestKind string

const (
	requestKindAPI      requestKind = "api"      // metadata and JSON calls
	requestKindPage     requestKind = "page"     // browser page loads and their subresources
	requestKindDownload requestKind = "download" // file transfers
)

const (
	// requestFlushInterval is how often buffered records reach the database.
	// Writes are batched because a single page load produces a burst of events
	// and the database runs on one connection.
	requestFlushInterval = 5 * time.Second
	// requestFlushThreshold forces a flush before the interval when a burst
	// fills the buffer.
	requestFlushThreshold = 200
	// requestRetention is how long records are kept. Long enough to look at a
	// full day of syncing, short enough to stay small.
	requestRetention = 48 * time.Hour
	// requestSummaryInterval is how often the counts are logged, so the numbers
	// show up without querying the database.
	requestSummaryInterval = 5 * time.Minute
)

// platformHostSuffixes maps a host suffix to the platform it belongs to. Suffix
// matching keeps a platform's CDN and login hosts with the platform itself -
// MakerWorld files come from bblmw.com and its login from bambulab.com, and both
// count against MakerWorld.
var platformHostSuffixes = map[string]string{
	"makerworld.com":    "makerworld",
	"bblmw.com":         "makerworld",
	"bambulab.com":      "makerworld",
	"printables.com":    "printables",
	"prusa3d.com":       "printables",
	"thingiverse.com":   "thingiverse",
	"cults3d.com":       "cults3d",
	"myminifactory.com": "myminifactory",
	"thangs.com":        "thangs",
}

// platformForURL resolves which platform a URL belongs to, or "" when the host
// is none of ours - unrelated CDNs and avatar hosts are not counted.
func platformForURL(rawURL string) string {
	parsed, failure := url.Parse(rawURL)
	if failure != nil {
		return ""
	}
	host := strings.ToLower(parsed.Hostname())
	if host == "" {
		return ""
	}
	for suffix, platform := range platformHostSuffixes {
		if host == suffix || strings.HasSuffix(host, "."+suffix) {
			return platform
		}
	}
	return ""
}

// requestRecord is one counted outbound request.
type requestRecord struct {
	platform string
	kind     requestKind
	status   int
	at       time.Time
}

// requestRecorder buffers records and writes them in batches.
type requestRecorder struct {
	db     *sql.DB
	mutex  sync.Mutex
	buffer []requestRecord
	// summary counts records per platform since the last summary log.
	summary     map[string]int
	lastSummary time.Time
	lastPrune   time.Time
}

// activeRecorder is nil until StartRequestAccounting wires it up; every record
// call is a no-op until then, which is what tests and one-off tools want.
var activeRecorder atomic.Pointer[requestRecorder]

// StartRequestAccounting connects the counter to the database and starts the
// flush loop. It returns immediately.
func StartRequestAccounting(database *sql.DB, stop <-chan struct{}) {
	if database == nil {
		return
	}
	recorder := &requestRecorder{
		db:          database,
		summary:     map[string]int{},
		lastSummary: time.Now(),
		lastPrune:   time.Now(),
	}
	activeRecorder.Store(recorder)

	safego.Go("request-accounting", func() {
		ticker := time.NewTicker(requestFlushInterval)
		defer ticker.Stop()
		for {
			select {
			case <-stop:
				recorder.flush()
				return
			case <-ticker.C:
				recorder.flush()
			}
		}
	})
}

// recordRequest counts one outbound request. Requests to hosts that belong to no
// platform are ignored.
func recordRequest(rawURL string, kind requestKind, status int) {
	recorder := activeRecorder.Load()
	if recorder == nil {
		return
	}
	platform := platformForURL(rawURL)
	if platform == "" {
		return
	}
	recorder.add(requestRecord{platform: platform, kind: kind, status: status, at: time.Now()})
}

// RecordBrowserRequest counts one request a browser page issued. Exported for
// the browser runner, which reports every request its pages make.
func RecordBrowserRequest(rawURL string) {
	recordRequest(rawURL, requestKindPage, 0)
}

// add buffers a record and flushes early when a burst fills the buffer.
func (recorder *requestRecorder) add(record requestRecord) {
	recorder.mutex.Lock()
	recorder.buffer = append(recorder.buffer, record)
	full := len(recorder.buffer) >= requestFlushThreshold
	recorder.mutex.Unlock()
	if full {
		safego.Go("request-accounting-flush", recorder.flush)
	}
}

// flush writes the buffered records, logs the periodic summary and prunes old
// rows. A failing write drops the batch: these are statistics, not data worth
// stalling a download for.
func (recorder *requestRecorder) flush() {
	recorder.mutex.Lock()
	batch := recorder.buffer
	recorder.buffer = nil
	recorder.mutex.Unlock()

	if len(batch) > 0 {
		transaction, failure := recorder.db.Begin()
		if failure != nil {
			return
		}
		statement, failure := transaction.Prepare("INSERT INTO platform_requests (platform, kind, status, at) VALUES (?,?,?,?)")
		if failure != nil {
			_ = transaction.Rollback()
			return
		}
		for _, record := range batch {
			_, _ = statement.Exec(record.platform, string(record.kind), record.status, record.at.UTC().Format(time.DateTime))
		}
		_ = statement.Close()
		if transaction.Commit() != nil {
			return
		}
	}

	recorder.mutex.Lock()
	for _, record := range batch {
		recorder.summary[record.platform]++
	}
	dueSummary := time.Since(recorder.lastSummary) >= requestSummaryInterval
	var summary map[string]int
	if dueSummary {
		summary = recorder.summary
		recorder.summary = map[string]int{}
		recorder.lastSummary = time.Now()
	}
	duePrune := time.Since(recorder.lastPrune) >= requestRetention/48 // hourly
	if duePrune {
		recorder.lastPrune = time.Now()
	}
	recorder.mutex.Unlock()

	for platform, count := range summary {
		if count > 0 {
			log.Printf("[requests] %s: %d request(s) in the last %s", platform, count, requestSummaryInterval)
		}
	}
	if duePrune {
		cutoff := time.Now().UTC().Add(-requestRetention).Format(time.DateTime)
		_, _ = recorder.db.Exec("DELETE FROM platform_requests WHERE at < ?", cutoff)
	}
}
