package store

import (
	"database/sql"
	"encoding/json"
	"time"
)

const tsLayout = "2006-01-02T15:04:05.000Z07:00"

func nowUTC() time.Time { return time.Now().UTC().Truncate(time.Millisecond) }

// ts formats a time for storage.
func ts(t time.Time) string { return t.UTC().Format(tsLayout) }

// TS is the exported form of ts for callers building raw SQL arguments.
func TS(t time.Time) string { return ts(t) }

// tsp formats an optional time (nil → NULL).
func tsp(t *time.Time) any {
	if t == nil || t.IsZero() {
		return nil
	}
	return ts(*t)
}

// TSP is the exported form of tsp.
func TSP(t *time.Time) any { return tsp(t) }

func parseTS(s string) time.Time {
	if s == "" {
		return time.Time{}
	}
	t, err := time.Parse(tsLayout, s)
	if err != nil {
		t, _ = time.Parse(time.RFC3339Nano, s)
	}
	return t.UTC()
}

// ParseTS is the exported form of parseTS (zero for an empty value).
func ParseTS(s string) time.Time { return parseTS(s) }

func parseTSP(s sql.NullString) *time.Time {
	if !s.Valid || s.String == "" {
		return nil
	}
	t := parseTS(s.String)
	return &t
}

// js encodes a value as JSON text (never fails for our types).
func js(v any) string {
	b, err := json.Marshal(v)
	if err != nil {
		return "null"
	}
	return string(b)
}

// JS is the exported JSON encoder for callers.
func JS(v any) string { return js(v) }

func unjs(s string, v any) {
	if s == "" {
		return
	}
	_ = json.Unmarshal([]byte(s), v)
}

func unjsNull(s sql.NullString, v any) {
	if s.Valid {
		unjs(s.String, v)
	}
}

// nullStr converts "" to NULL.
func nullStr(s string) any {
	if s == "" {
		return nil
	}
	return s
}

// NullStr is the exported form of nullStr.
func NullStr(s string) any { return nullStr(s) }

func b2i(b bool) int {
	if b {
		return 1
	}
	return 0
}

func strs(v []string) []string {
	if v == nil {
		return []string{}
	}
	return v
}
