// Package storage owns the on-disk layout of everything the app persists.
//
// Everything a user owns lives under one directory, so deleting an account is a
// single RemoveAll and its disk usage a single du:
//
//	{root}/user/{publicID}/design/{designID}/version/{version}/{path}  model files
//	{root}/user/{publicID}/design/{designID}/cover.{ext}               design cover
//	{root}/user/{publicID}/design/{designID}/pictures/{file}           gallery images
//	{root}/user/{publicID}/blobs/{hash[0:2]}/{hash[2:4]}/{hash}        content store
//	{root}/user/{publicID}/account/avatar/{file}                       profile image
//	{root}/user/{publicID}/tmp/{…}                                     in-flight downloads
//
// Named after the public id rather than users.id: a cover is served over an
// unauthenticated URL containing this path, and a sequential number there would
// let anyone count the accounts and address them one by one.
//
// Version directories hold real files, each a hard link to the blob of the same
// content, so a file that survives ten syncs occupies the disk once. Backups have
// to be told about the links (tar --hard-links, rsync -H) or they expand every
// version into a full copy. Paths are stored relative to the root, so moving the
// data directory does not invalidate every row.
package storage

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"meshdepot/internal/logx"
)

// What the container's own user needs; the entrypoint chowns the volume to it
// before dropping privileges.
const (
	dirPermissions  os.FileMode = 0o775
	filePermissions os.FileMode = 0o664
)

// defaultCoverExtension has to be a shape the public image endpoint accepts, or
// the file is stored but can never be served.
const defaultCoverExtension = "jpg"

type Layout struct {
	root string
}

func New(root string) Layout {
	return Layout{root: strings.TrimRight(filepath.Clean(root), "/")}
}

func (layout Layout) Root() string {
	return layout.root
}

// User narrows the layout to one account, so code holding nothing but a numeric
// id cannot build a path at all.
func (layout Layout) User(publicID string) UserLayout {
	return UserLayout{root: layout.root, publicID: publicID}
}

// Abs turns a stored path into an absolute one. Absolute input is returned
// unchanged, so rows written before a path became relative still resolve.
func (layout Layout) Abs(stored string) string {
	if stored == "" {
		return ""
	}
	if filepath.IsAbs(stored) {
		return stored
	}
	return filepath.Join(layout.root, filepath.FromSlash(stored))
}

// Rel turns an absolute path into the stored form: relative to the root and
// always with forward slashes.
func (layout Layout) Rel(absolute string) string {
	relative, failure := filepath.Rel(layout.root, absolute)
	if failure != nil || strings.HasPrefix(relative, "..") {
		// Returning the absolute path keeps the caller working, but it is also the one
		// way an absolute value ends up in a path column again.
		logx.Warnf("[storage] path outside the data root stays absolute: %s", absolute)
		return absolute
	}
	return filepath.ToSlash(relative)
}

// Contains reports whether path stays inside the data root once symlinks and
// ".." are resolved. Every handler building a path from request data asks first.
func (layout Layout) Contains(path string) bool {
	resolvedRoot, failure := filepath.Abs(layout.root)
	if failure != nil {
		return false
	}
	resolved, failure := filepath.Abs(path)
	if failure != nil {
		return false
	}
	return resolved == resolvedRoot || strings.HasPrefix(resolved, resolvedRoot+string(os.PathSeparator))
}

type UserLayout struct {
	root     string
	publicID string
}

func (user UserLayout) Root() string {
	return filepath.Join(user.root, "user", user.publicID)
}

func (user UserLayout) PublicID() string {
	return user.publicID
}

func (user UserLayout) Design(designID int) string {
	return filepath.Join(user.Root(), "design", strconv.Itoa(designID))
}

func (user UserLayout) Version(designID int, version string) string {
	return filepath.Join(user.Design(designID), "version", version)
}

func (user UserLayout) Pictures(designID int) string {
	return filepath.Join(user.Design(designID), "pictures")
}

// Cover normalises the extension here rather than at the call sites: the public
// image endpoint only serves "cover.{1-5 alphanumerics}".
func (user UserLayout) Cover(designID int, extension string) string {
	return filepath.Join(user.Design(designID), "cover."+normalizeExtension(extension))
}

func (user UserLayout) Avatar() string {
	return filepath.Join(user.Root(), "account", "avatar")
}

// Temp sits inside the account directory, so a half-finished download is removed
// with the account and the move into the blob store is a rename, not a copy.
func (user UserLayout) Temp() string {
	return filepath.Join(user.Root(), "tmp")
}

// Blob is content-addressed; the two prefix levels keep the per-directory entry
// count reasonable on large libraries.
func (user UserLayout) Blob(hash string) string {
	if len(hash) < 4 {
		return ""
	}
	return filepath.Join(user.Root(), "blobs", hash[0:2], hash[2:4], hash)
}

// Delegated so a UserLayout is self-contained.
func (user UserLayout) Abs(stored string) string   { return user.layout().Abs(stored) }
func (user UserLayout) Rel(absolute string) string { return user.layout().Rel(absolute) }
func (user UserLayout) Contains(path string) bool  { return user.layout().Contains(path) }

func (user UserLayout) layout() Layout {
	return Layout{root: user.root}
}

// normalizeExtension reduces an extension to what the public image endpoint
// accepts, falling back to jpg.
func normalizeExtension(extension string) string {
	extension = strings.ToLower(strings.TrimPrefix(strings.TrimSpace(extension), "."))
	if extension == "" || len(extension) > 5 {
		return defaultCoverExtension
	}
	for _, character := range extension {
		isDigit := character >= '0' && character <= '9'
		isLetter := character >= 'a' && character <= 'z'
		if !isDigit && !isLetter {
			return defaultCoverExtension
		}
	}
	return extension
}

// MkdirAll creates a directory with the permissions the container user needs.
func MkdirAll(path string) error {
	return os.MkdirAll(path, dirPermissions)
}

func WriteFile(path string, data []byte) error {
	if failure := MkdirAll(filepath.Dir(path)); failure != nil {
		return failure
	}
	return os.WriteFile(path, data, filePermissions)
}
