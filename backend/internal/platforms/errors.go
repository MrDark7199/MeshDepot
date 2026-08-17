package platforms

// Typed download error with phase marking. The worker (RunOnce) uses the phase
// to write a precise log line when a job fails: "no metadata" (model info not
// found) vs. "files failed" (model found, but no download succeeded).

import "fmt"

// FailStage marks in which phase of Download() the error occurred.
type FailStage int

const (
	// StageOther: everything else (URL parse, missing credentials/token …).
	StageOther FailStage = iota
	// StageMetadata: model info (name, description, file list) could not be
	// loaded at all - nothing was found.
	StageMetadata
	// StageFiles: metadata OK, but not a single file could be loaded.
	StageFiles
)

// DownloadError additionally carries the failure phase. Error() returns the
// inner string unchanged so that i18n keys (e.g. error.printables_no_files) and
// the permanence/frontend classification keep working as before.
type DownloadError struct {
	Stage FailStage
	Err   error
}

func (downloadError *DownloadError) Error() string { return downloadError.Err.Error() }
func (downloadError *DownloadError) Unwrap() error { return downloadError.Err }

// MetadataError marks an error of the metadata phase (situation 2).
func MetadataError(format string, args ...any) error {
	return &DownloadError{Stage: StageMetadata, Err: fmt.Errorf(format, args...)}
}

// FilesError marks an error of the file phase (situation 1).
func FilesError(format string, args ...any) error {
	return &DownloadError{Stage: StageFiles, Err: fmt.Errorf(format, args...)}
}
