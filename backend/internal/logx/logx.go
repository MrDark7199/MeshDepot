// Package logx filters the log output by the level an operator configured.
//
// LOG_LEVEL used to be read into the configuration and then ignored, so every
// line went out whatever the setting said. The levels are the ones the compose
// file has always documented: none, error, warning, info, all.
//
// Fatal messages are deliberately not part of this - a container that dies must
// say why, even at "none".
package logx

import (
	"log"
	"strings"
	"sync/atomic"
)

type Level int32

const (
	LevelNone Level = iota
	LevelError
	LevelWarning
	LevelInfo
	LevelAll
)

// current is read from every goroutine that logs and written once at startup.
var current atomic.Int32

func init() { current.Store(int32(LevelAll)) }

var names = map[string]Level{
	"none": LevelNone, "error": LevelError, "warning": LevelWarning,
	"info": LevelInfo, "all": LevelAll,
}

// Parse maps a configured name to its level. ok=false for anything unknown.
func Parse(name string) (Level, bool) {
	level, ok := names[strings.ToLower(strings.TrimSpace(name))]
	return level, ok
}

// Configure sets the level for the process. An unknown name keeps everything
// and says so, rather than quietly silencing the log.
func Configure(name string) {
	level, ok := Parse(name)
	if !ok {
		current.Store(int32(LevelAll))
		log.Printf("[config] LOG_LEVEL %q is not one of none|error|warning|info|all - logging everything", name)
		return
	}
	current.Store(int32(level))
}

// Enabled reports whether messages of that level are currently written.
func Enabled(level Level) bool { return Level(current.Load()) >= level }

// Errorf writes something that failed and that an operator has to know about.
func Errorf(format string, args ...any) {
	if Enabled(LevelError) {
		log.Printf(format, args...)
	}
}

// Warnf writes something that went wrong but was handled.
func Warnf(format string, args ...any) {
	if Enabled(LevelWarning) {
		log.Printf(format, args...)
	}
}

// Infof writes the ordinary course of events: startup, sync runs, downloads.
func Infof(format string, args ...any) {
	if Enabled(LevelInfo) {
		log.Printf(format, args...)
	}
}

// Debugf writes what is only useful while chasing something down.
func Debugf(format string, args ...any) {
	if Enabled(LevelAll) {
		log.Printf(format, args...)
	}
}
