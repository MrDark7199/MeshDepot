package platforms

// Outbound request accounting. Nothing is blocked: the point is to learn what one
// download really costs. The cooldown paces whole jobs, while a single job issues
// a metadata call, a full browser page load with its subresources, a presigned
// URL lookup per instance and the transfer.
//
// The recorder is package-level state on purpose: fetch(), streamDownload() and
// the browser callback are too, and threading a counter through thirty call sites
// would be a poor trade.

import (
	"database/sql"
	"meshdepot/internal/dbutil"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"meshdepot/internal/safego"

	"meshdepot/internal/logx"
)

type requestKind string

const (
	requestKindAPI      requestKind = "api"      // metadata and JSON calls
	requestKindPage     requestKind = "page"     // browser page loads and their subresources
	requestKindDownload requestKind = "download" // file transfers
)

const (
	// Writes are batched: a single page load produces a burst of events and the
	// database runs on one connection.
	requestFlushInterval = 5 * time.Second
	// requestFlushThreshold forces a flush before the interval on a burst.
	requestFlushThreshold = 200
	// requestRetention is long enough for a full day of syncing, short enough to
	// stay small.
	requestRetention = 48 * time.Hour
	// requestSummaryInterval logs the counts, so they show up without a query.
	requestSummaryInterval = 5 * time.Minute
)

// platformHostSuffixes keeps a platform's CDN and login hosts with the platform:
// MakerWorld files come from bblmw.com and its login from bambulab.com.
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

// platformForURL returns "" for hosts that are none of ours - unrelated CDNs and
// avatar hosts are not counted.
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

type requestRecord struct {
	platform string
	kind     requestKind
	status   int
	at       time.Time
}

type requestRecorder struct {
	db     *sql.DB
	mutex  sync.Mutex
	buffer []requestRecord
	// summary counts records per platform since the last summary log.
	summary     map[string]int
	lastSummary time.Time
	lastPrune   time.Time
}

// activeRecorder is nil until StartRequestAccounting wires it up, so every record
// call is a no-op until then.
var activeRecorder atomic.Pointer[requestRecorder]

// StartRequestAccounting connects the counter and starts the flush loop.
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

// recordRequest ignores hosts that belong to no platform.
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

// RecordBrowserRequest is exported for the browser runner, which reports every
// request its pages make.
func RecordBrowserRequest(rawURL string) {
	recordRequest(rawURL, requestKindPage, 0)
}

func (recorder *requestRecorder) add(record requestRecord) {
	recorder.mutex.Lock()
	recorder.buffer = append(recorder.buffer, record)
	full := len(recorder.buffer) >= requestFlushThreshold
	recorder.mutex.Unlock()
	if full {
		safego.Go("request-accounting-flush", recorder.flush)
	}
}

// flush writes the buffered records, logs the summary and prunes old rows. A
// failing write drops the batch: these are statistics, not data worth stalling a
// download for.
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
		// Counted rather than logged per row: a database that refuses one insert
		// refuses the next two hundred as well, and the point of this is one line,
		// not a flood of them.
		refused := 0
		for _, record := range batch {
			if _, failure := statement.Exec(record.platform, string(record.kind), record.status,
				record.at.UTC().Format(time.DateTime)); failure != nil {
				refused++
			}
		}
		if refused > 0 {
			logx.Warnf("[requests] %d of %d records could not be stored", refused, len(batch))
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
			logx.Debugf("[requests] %s: %d request(s) in the last %s", platform, count, requestSummaryInterval)
		}
	}
	if duePrune {
		cutoff := time.Now().UTC().Add(-requestRetention).Format(time.DateTime)
		dbutil.ExecLogged(recorder.db, "DELETE FROM platform_requests WHERE at < ?", cutoff)
	}
}
