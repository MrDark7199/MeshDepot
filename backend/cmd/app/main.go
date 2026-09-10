// Command app is the single-binary backend of MeshDepot: HTTP API, embedded SPA,
// workers, scheduler, Tor and browser in one process.
package main

import (
	"context"
	"database/sql"
	"errors"
	"log"
	"meshdepot/internal/dbutil"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	"meshdepot/internal/api"
	"meshdepot/internal/auth"
	"meshdepot/internal/browser"
	"meshdepot/internal/config"
	"meshdepot/internal/crypto"
	"meshdepot/internal/db"
	"meshdepot/internal/health"
	"meshdepot/internal/httpx"
	"meshdepot/internal/maildigest"
	"meshdepot/internal/notify"
	"meshdepot/internal/platforms"
	"meshdepot/internal/platforms/tor"
	"meshdepot/internal/quota"
	"meshdepot/internal/safego"
	"meshdepot/internal/scheduler"
	"meshdepot/internal/storage"
	"meshdepot/internal/translate"
	"meshdepot/internal/worker"

	"meshdepot/internal/logx"
)

// Docker's default grace period before SIGKILL is 10 s, so a longer wait would
// only be waited out by the kernel.
const workerShutdownWait = 8 * time.Second

// Node has to load Playwright first, which takes seconds on a cold container.
// 60 s leaves room for a slow host without making a failure look like a hang.
const resolverStartupWait = 60 * time.Second

func main() {
	configuration := config.Load()
	// Before anything logs: LOG_LEVEL decides what the rest of this run writes.
	// Fatal messages stay outside it - a container that dies has to say why.
	logx.Configure(configuration.LogLevel)

	if failure := validateConfig(configuration); failure != nil {
		log.Fatalf("[fatal] %v", failure)
	}

	// A missing resolver does not announce itself; it just makes MyMiniFactory
	// downloads come back empty one file at a time.
	if failure := platforms.WaitForResolver(configuration.PlaywrightURL, resolverStartupWait); failure != nil {
		log.Fatalf("[fatal] Firefox resolver not reachable at %q: %v. "+
			"It ships inside this image, its address comes from PLAYWRIGHT_URL (set in backend/Dockerfile) and docker-entrypoint.sh starts it - "+
			"check the container logs for '[playwright]' or '[entrypoint]' lines. "+
			"Override PLAYWRIGHT_URL only to move the resolver to another port or to point at one you run yourself; never set it empty.",
			configuration.PlaywrightURL, failure)
	}
	logx.Infof("[init] Firefox resolver ready: %s", configuration.PlaywrightURL)

	database, failure := db.Open(configuration.DBPath)
	if failure != nil {
		log.Fatalf("db open: %v", failure)
	}
	defer database.Close()
	if failure := db.InitSchema(database); failure != nil {
		log.Fatalf("db init: %v", failure)
	}
	logx.Infof("[init] SQLite ready: %s", configuration.DBPath)

	// Only these networks may set X-Real-IP (login rate limiting depends on it).
	if invalid := httpx.SetTrustedProxies(configuration.TrustedProxies); len(invalid) > 0 {
		logx.Warnf("[init] TRUSTED_PROXIES: ignoring unparsable entries %v", invalid)
	}
	if len(configuration.TrustedProxies) > 0 {
		logx.Infof("[init] trusting X-Real-IP from %v", configuration.TrustedProxies)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	stop := make(chan struct{})

	// Non-fatal: without Tor only the tor-dependent downloads stop working.
	torSupervisor := tor.New(configuration.TorBin)
	safego.Go("tor-supervisor", func() {
		if failure := torSupervisor.Start(ctx); failure != nil {
			logx.Warnf("[tor] not started: %v (platform downloads via Tor disabled)", failure)
		} else {
			logx.Infof("[tor] ready")
		}
	})

	browserLauncher := browser.New(configuration.ChromiumBin)
	// Counts every outbound request, including the ones a browser page pulls in on
	// its own. Nothing is blocked; the numbers are what the cooldown was missing.
	platforms.StartRequestAccounting(database, stop)
	browserLauncher.OnRequest = platforms.RecordBrowserRequest
	cryptoHelper := crypto.New(configuration.AppKey)
	platformDeps := platforms.Deps{DB: database, Crypto: cryptoHelper, Tor: torSupervisor, Browser: browserLauncher, Cfg: configuration}
	registry := platforms.New(platformDeps)

	// The same registry goes to the workers, the scheduler and the API, so
	// /admin/health can tell a loop that is idle from one that is gone.
	heartbeats := health.New()

	downloadWorker := worker.NewDownloadWorker(database, downloadProcess(database, configuration, registry), cooldownFor(database))
	downloadWorker.Heartbeat = heartbeats
	downloadWorker.Start(stop)
	syncWorker := worker.NewSyncWorker(database, syncProcess(database, configuration, registry))
	syncWorker.Heartbeat = heartbeats
	syncWorker.Start(stop)

	// Scheduler: forced library sync (flag) + auto-sync enqueue.
	backgroundScheduler := scheduler.New(database, func() { platforms.RunLibrarySync(platformDeps) })
	// Notification e-mails go out in batches: forty queued downloads would otherwise
	// be forty messages. The SMTP password is stored encrypted.
	backgroundScheduler.SendMailDigest = func() { maildigest.Send(database, cryptoHelper) }
	backgroundScheduler.MailDigestInterval = maildigest.Interval
	backgroundScheduler.Heartbeat = heartbeats
	backgroundScheduler.Start(stop)
	logx.Infof("[worker] download/sync workers + scheduler started")

	authService := auth.New(database, cryptoHelper, configuration.AppHTTPS)
	server := api.New(database, authService, cryptoHelper, configuration, registry, platformDeps)
	server.Health = heartbeats
	// Without timeouts a slow client can hold connections open indefinitely, and
	// every DB access funnels through a single connection.
	//
	// ReadHeaderTimeout is what stops Slowloris. ReadTimeout and WriteTimeout stay 0
	// deliberately: they cover the body and the response, and this server accepts
	// multi-hundred-MB uploads and streams long-lived downloads and SSE. Body size
	// is bounded by MaxBytesReader in the upload handlers instead.
	httpServer := &http.Server{
		Addr:              configuration.HTTPAddr,
		Handler:           server.Router(),
		ReadHeaderTimeout: 15 * time.Second,
		IdleTimeout:       120 * time.Second,
	}

	go func() {
		logx.Infof("[http] listening on %s", configuration.HTTPAddr)
		if failure := httpServer.ListenAndServe(); failure != nil && failure != http.ErrServerClosed {
			log.Fatalf("[http] server error: %v", failure)
		}
	}()

	signalChannel := make(chan os.Signal, 1)
	signal.Notify(signalChannel, syscall.SIGINT, syscall.SIGTERM)
	<-signalChannel
	logx.Infof("[app] shutdown…")
	close(stop)
	shutdownContext, shutdownCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer shutdownCancel()
	_ = httpServer.Shutdown(shutdownContext)

	// A job cut between "files written" and "DB rows inserted" leaves a half-imported
	// design that no cleanup finds. Bounded, because a large model may need minutes
	// and the container supervisor will not wait - cancel() then tears the rest down.
	if !downloadWorker.Wait(workerShutdownWait) {
		logx.Warnf("[worker] download job still running, shutting down anyway")
	}
	if !syncWorker.Wait(workerShutdownWait) {
		logx.Warnf("[worker] sync job still running, shutting down anyway")
	}
	cancel()
}

// validateConfig checks the settings the app cannot guess. It returns an error
// rather than exiting, so the rules stay testable.
func validateConfig(configuration config.Config) error {
	// The built-in default APP_KEY is public in the source tree, so every stored
	// credential would be encrypted with a key anyone can read.
	if configuration.AppKey == config.DefaultAppKey {
		return errors.New("APP_KEY is unset or still the built-in default - refusing to start. " +
			"All stored platform credentials would be encrypted with a publicly known key. " +
			"Set a unique, secret APP_KEY (>= 32 random characters) via the environment and restart.")
	}

	// No default listen address on purpose: the deployment decides the port, and the
	// Compose mapping and healthcheck derive from the same value. A silent fallback
	// would look like a broken container rather than a missing setting.
	if configuration.HTTPAddr == "" {
		return errors.New("HTTP_ADDR is unset - refusing to start. " +
			"It is the listen address of the HTTP server and comes from the deployment " +
			"(see the environment block of docker-compose.yml, where it follows APP_PORT). " +
			"Set it to an address of the form \":9000\" and restart.")
	}
	return nil
}

// downloadProcess builds the download worker's Process: resolve the downloader,
// download, save into the library.
func downloadProcess(database *sql.DB, configuration config.Config, registry platforms.Registry) func(worker.Job) (int, error) {
	return func(job worker.Job) (int, error) {
		downloader := registry.Get(job.Platform)
		if downloader == nil {
			return 0, errors.New("error.unsupported_url")
		}
		// Before the download, not after: a job that cannot be kept should not fetch
		// gigabytes first. The message is permanent, so the queue reports it once rather
		// than retrying against a limit that will not move on its own.
		usage := quota.Of(database, job.UserID)
		// Unknown is deliberately not permanent: the figures could not be read,
		// which is a reason to try again later, not to burn the job against a
		// limit nobody has seen.
		if usage.Unknown {
			return 0, errors.New("error.storage_check_failed:Your storage use could not be checked just now. " +
				"The download stays in the queue and is tried again.")
		}
		if usage.Exceeded() {
			return 0, errors.New("error.storage_quota_exceeded:Your storage limit is reached. " +
				"Delete designs or versions you no longer need, or ask an administrator for more space.")
		}
		owner := platforms.Owner{ID: job.UserID, Layout: storage.New(configuration.BasePathData).User(job.UserPublicID)}
		result, failure := downloader.Download(job.SourceURL, owner, func(string, string, int, int) {})
		if failure != nil {
			return 0, failure
		}
		designID, failure := platforms.SaveDownload(database, owner, job.Platform, job.SourceURL, result)
		if failure != nil {
			return 0, failure
		}
		platforms.ResolvePendingCollections(database, job.UserID, job.Platform, result.SourceID, designID)
		// Fail-safe and a no-op when translation_enabled=0: translation must never break
		// the download.
		var description *string
		if result.Description != "" {
			description = &result.Description
		}
		translate.New(database).ApplyToDesign(designID, result.Name, description, true)
		// After the design is stored, so the figure the member is told about is the one
		// they can go and look at.
		notify.StorageNearlyFull(database, job.UserID)
		return designID, nil
	}
}

// syncProcess builds the sync worker's Process: reload the design from its
// source_url and store it as a new version. "Unchanged" counts as done.
func syncProcess(database *sql.DB, configuration config.Config, registry platforms.Registry) func(worker.SyncJob) error {
	return func(job worker.SyncJob) error {
		var sourceURL, name string
		failure := database.QueryRow(
			"SELECT COALESCE(source_url,''), COALESCE(name,'') FROM designs WHERE id=? AND user_id=? LIMIT 1",
			job.DesignID, job.UserID,
		).Scan(&sourceURL, &name)
		if failure != nil || sourceURL == "" {
			return errors.New("error.unsupported_url")
		}
		// Marked right away, or the auto-sync re-pulls this design every 10 min whether
		// or not the attempt succeeds.
		if _, failure := database.Exec("UPDATE designs SET last_synced_at=CURRENT_TIMESTAMP WHERE id=?", job.DesignID); failure != nil {
			logx.Errorf("[sync] design %d: the sync stamp could not be written, so the auto-sync will pull it again: %v",
				job.DesignID, failure)
		}
		platform := platforms.DetectPlatform(sourceURL)
		downloader := registry.Get(platform)
		if downloader == nil {
			return errors.New("error.unsupported_url")
		}
		// Mirrors the current phase into sync_queue, so the frontend shows the running
		// category rather than a static "updating…". Every downloader serves the same
		// progress callback.
		writeStep := func(step string, current, total int) {
			dbutil.ExecLogged(database,
				"UPDATE sync_queue SET current_step=?, step_current=?, step_total=?, progress=? WHERE id=?",
				step, current, total, syncStepPercent(step, current, total), job.ID,
			)
		}
		owner := platforms.Owner{ID: job.UserID, Layout: storage.New(configuration.BasePathData).User(job.UserPublicID)}
		result, failure := downloader.Download(sourceURL, owner, func(step, _ string, current, total int) {
			writeStep(step, current, total)
		})
		if failure != nil {
			return failure
		}

		// Always refreshed, whether or not new files are added.
		translator := translate.New(database)
		var description *string
		if result.Description != "" {
			description = &result.Description
		}
		if translator.Enabled() {
			// The canonical fields hold the EN translation and must not be overwritten with
			// the raw platform text. Compared against the stored originals.
			originalName := translator.OriginalOf(job.DesignID, "name")
			originalDescription := translator.OriginalOf(job.DesignID, "description")
			if originalName != result.Name || originalDescription != result.Description {
				translator.ApplyToDesign(job.DesignID, result.Name, description, true)
			}
			if _, failure := database.Exec("UPDATE designs SET author=?, updated_at=CURRENT_TIMESTAMP WHERE id=?",
				nullIfEmpty(result.Author), job.DesignID); failure != nil {
				logx.Errorf("[sync] design %d: refreshed metadata was not stored: %v", job.DesignID, failure)
			}
		} else {
			if _, failure := database.Exec("UPDATE designs SET description=?, author=?, updated_at=CURRENT_TIMESTAMP WHERE id=?",
				nullIfEmpty(result.Description), nullIfEmpty(result.Author), job.DesignID); failure != nil {
				logx.Errorf("[sync] design %d: refreshed metadata was not stored: %v", job.DesignID, failure)
			}
		}
		platforms.AddTags(database, job.UserID, job.DesignID, result.Tags)
		platforms.AddImages(database, owner, job.DesignID, result.AllImages)

		changed, newVersion, fileCount, failure := platforms.SaveSyncVersion(database, owner, job.DesignID, result, func(_, _ string, current, total int) {
			// SaveSyncVersion reports per file; summarised for the category display.
			writeStep("creating_version", current, total)
		})
		if failure != nil {
			return failure
		}
		// Noted during the library sync, and resolved whether or not the version changed.
		platforms.ResolvePendingCollections(database, job.UserID, platform, result.SourceID, job.DesignID)
		if changed {
			designID := job.DesignID
			notify.User(database, job.UserID, "sync_update", "Update downloaded: "+name,
				"Version "+newVersion+" with "+strconv.Itoa(fileCount)+" file(s) saved.", &designID)
		}
		return nil
	}
}

// syncStepPercent maps the phase to a coarse 0-99 %, refined within the file and
// image phases by the counter. 100 % is set by the worker on completion.
func syncStepPercent(step string, current, total int) int {
	switch step {
	case "authenticating":
		return 5
	case "fetching_metadata":
		return 15
	case "downloading_files":
		if total > 0 {
			return 25 + current*45/total // 25 … 70
		}
		return 25
	case "extracting_files":
		return 72
	case "downloading_images":
		if total > 0 {
			return 75 + current*15/total // 75 … 90
		}
		return 75
	case "creating_version":
		if total > 0 {
			return 90 + current*9/total // 90 … 99
		}
		return 92
	}
	return 0
}

// nullIfEmpty stores an empty field as NULL rather than "".
func nullIfEmpty(value string) any {
	if value == "" {
		return nil
	}
	return value
}

func cooldownFor(database *sql.DB) func(string) int {
	return func(platform string) int {
		for _, key := range []string{"download_cooldown_" + platform, "download_cooldown_default"} {
			var value string
			if database.QueryRow("SELECT value FROM app_settings WHERE key=?", key).Scan(&value) == nil {
				if number, _ := strconv.Atoi(value); number > 0 {
					return number
				}
			}
		}
		return 0
	}
}
