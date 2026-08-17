// Package storage owns the on-disk layout of everything the app persists.
//
// Everything a user owns lives under one directory, addressed by the account's
// public id, so deleting an account is a single RemoveAll and its disk usage a
// single du:
//
//	{root}/user/{publicID}/design/{designID}/version/{version}/{path}  model files
//	{root}/user/{publicID}/design/{designID}/cover.{ext}               design cover
//	{root}/user/{publicID}/design/{designID}/pictures/{file}           gallery images
//	{root}/user/{publicID}/blobs/{hash[0:2]}/{hash[2:4]}/{hash}        content store
//	{root}/user/{publicID}/account/avatar/{file}                       profile image
//	{root}/user/{publicID}/tmp/{…}                                     in-flight downloads
//
// The directory is named after the public id rather than the numeric users.id:
// the cover of a design is served over an unauthenticated URL that contains this
// path, and a sequential number there would let anyone count the accounts and
// address them one by one.
//
// Version directories hold real files, but each one is a hard link to the blob
// of the same content, so a file that survives ten syncs occupies the disk once
// while every version stays a browsable directory. The blob store is per user:
// it keeps an account self-contained at the price of storing a file twice when
// two users own the same one. Backups have to be told about the links
// (tar --hard-links, rsync -H) or they expand every version into a full copy.
//
// Paths are stored in the database relative to the root, so moving the data
// directory does not invalidate every row.
package storage

import (
	"log"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// dirPermissions and filePermissions are what the container's own user needs;
// the entrypoint chowns the volume to it before dropping privileges.
const (
	dirPermissions  os.FileMode = 0o775
	filePermissions os.FileMode = 0o664
)

// defaultCoverExtension is used when a cover image arrives without a usable
// one. It has to be a shape the public image endpoint accepts, otherwise the
// file is stored but can never be served.
const defaultCoverExtension = "jpg"

// Layout resolves the paths of one data root.
type Layout struct {
	root string
}

// New builds a layout for the given data root (BASE_PATH_DATA).
func New(root string) Layout {
	return Layout{root: strings.TrimRight(filepath.Clean(root), "/")}
}

// Root is the data directory every other path is relative to.
func (layout Layout) Root() string {
	return layout.root
}

// User narrows the layout to one account. Everything below an account is only
// reachable through the returned value, so code holding nothing but a numeric
// id cannot build a path at all - the compiler asks for the public id instead.
func (layout Layout) User(publicID string) UserLayout {
	return UserLayout{root: layout.root, publicID: publicID}
}

// Abs turns a path stored in the database into an absolute one. Absolute input
// is returned unchanged, so rows written before a path became relative still
// resolve.
func (layout Layout) Abs(stored string) string {
	if stored == "" {
		return ""
	}
	if filepath.IsAbs(stored) {
		return stored
	}
	return filepath.Join(layout.root, filepath.FromSlash(stored))
}

// Rel turns an absolute path into the form stored in the database: relative to
// the root and always with forward slashes, so the value is independent of both
// the data directory and the host's separator.
func (layout Layout) Rel(absolute string) string {
	relative, failure := filepath.Rel(layout.root, absolute)
	if failure != nil || strings.HasPrefix(relative, "..") {
		// Returning the absolute path keeps the caller working, but it is also the
		// one way an absolute value can end up in a path column again, which is
		// what breaks a deployment that later moves its data directory. Say so.
		log.Printf("[storage] path outside the data root stays absolute: %s", absolute)
		return absolute
	}
	return filepath.ToSlash(relative)
}

// Contains reports whether path stays inside the data root once symlinks and
// ".." are resolved. Every handler that builds a path from request data has to
// ask this before touching the filesystem.
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

// UserLayout resolves the paths of one account, addressed by its public id.
type UserLayout struct {
	root     string
	publicID string
}

// Root is the directory holding everything of this account.
func (user UserLayout) Root() string {
	return filepath.Join(user.root, "user", user.publicID)
}

// PublicID is the account this layout addresses.
func (user UserLayout) PublicID() string {
	return user.publicID
}

// Design is the directory of one design: its versions, cover and pictures.
func (user UserLayout) Design(designID int) string {
	return filepath.Join(user.Root(), "design", strconv.Itoa(designID))
}

// Version is the directory of one version of a design ("1.0", "2.0", …).
func (user UserLayout) Version(designID int, version string) string {
	return filepath.Join(user.Design(designID), "version", version)
}

// Pictures is the gallery directory of a design.
func (user UserLayout) Pictures(designID int) string {
	return filepath.Join(user.Design(designID), "pictures")
}

// Cover is the title image of a design. The extension is normalised here rather
// than at the call sites: the public image endpoint only serves a cover whose
// name matches "cover.{1-5 alphanumerics}", so an image arriving without a
// usable extension would otherwise be stored under a name nothing can request.
func (user UserLayout) Cover(designID int, extension string) string {
	return filepath.Join(user.Design(designID), "cover."+normalizeExtension(extension))
}

// Avatar is the directory holding the profile image of this account.
func (user UserLayout) Avatar() string {
	return filepath.Join(user.Root(), "account", "avatar")
}

// Temp is the scratch directory for downloads in flight. It sits inside the
// account directory so a half-finished download is removed with the account and
// stays on the same filesystem as its target - the move into the blob store is
// then a rename, not a copy.
func (user UserLayout) Temp() string {
	return filepath.Join(user.Root(), "tmp")
}

// Blob is the content-addressed path of one file. The two nested prefix levels
// keep the per-directory entry count reasonable on large libraries.
func (user UserLayout) Blob(hash string) string {
	if len(hash) < 4 {
		return ""
	}
	return filepath.Join(user.Root(), "blobs", hash[0:2], hash[2:4], hash)
}

// Abs, Rel and Contains are delegated so a UserLayout is self-contained and no
// caller has to carry both values around.
func (user UserLayout) Abs(stored string) string   { return user.layout().Abs(stored) }
func (user UserLayout) Rel(absolute string) string { return user.layout().Rel(absolute) }
func (user UserLayout) Contains(path string) bool  { return user.layout().Contains(path) }

func (user UserLayout) layout() Layout {
	return Layout{root: user.root}
}

// normalizeExtension reduces a file extension to the shape the public image
// endpoint accepts, falling back to jpg for anything unusable.
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

// MkdirAll creates a directory inside the layout with the permissions the
// container user needs.
func MkdirAll(path string) error {
	return os.MkdirAll(path, dirPermissions)
}

// WriteFile writes a file with the layout's permissions, creating its directory.
func WriteFile(path string, data []byte) error {
	if failure := MkdirAll(filepath.Dir(path)); failure != nil {
		return failure
	}
	return os.WriteFile(path, data, filePermissions)
}
