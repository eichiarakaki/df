package infra

import (
	"archive/zip"
	"encoding/csv"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/eichiarakaki/df/internals/logger"
	"github.com/eichiarakaki/df/internals/orchestrator/schema"
)

// rowParser parses a raw CSV row and returns its canonical timestamp (unix ms).
type rowParser func(row []string) (ts int64, payload []byte, err error)

// integrityParsers maps a Binance Vision data type to the row parser that
// knows its column layout. Data types absent from this map (e.g.
// "liquidationSnapshot") cannot be content-verified; the caller treats that
// as "assume valid" rather than failing the download.
var integrityParsers = map[string]rowParser{
	"klines":    schema.ParseKline,
	"aggTrades": schema.ParseAggTrade,
	"trades":    schema.ParseTrade,
	"bookDepth": schema.ParseBookDepth,
	"metrics":   schema.ParseMetrics,
}

// contentIsComplete inspects the local file already present at destPath and
// reports whether it holds complete, correct data for the calendar day
// embedded in key's filename (fileDate). It checks:
//
//  1. Self-contained: the oldest and newest row both belong to fileDate, and
//     for klines (a fixed-cadence data type) the first/last candle exactly
//     match the expected start/end of the day.
//  2. Cross-file continuity: when the next day's file is expected to exist
//     by now and is present locally, the last row of this file must chain
//     into the first row of that file with no gap or overlap.
//
// A file whose data type has no known row parser cannot be verified and is
// conservatively treated as complete.
func contentIsComplete(key, destPath string, fileDate time.Time) bool {
	dataType, interval := dataTypeAndInterval(key)
	parse, ok := integrityParsers[dataType]
	if !ok {
		return true
	}

	readPath := destPath
	if csvPath := siblingCSV(destPath); csvPath != destPath {
		if _, err := os.Stat(csvPath); err == nil {
			readPath = csvPath
		}
	}

	tsMin, tsMax, err := firstAndLastTimestamp(readPath, parse)
	if err != nil {
		logger.Warnf("integrity check %s: %v", filepath.Base(destPath), err)
		return false
	}

	dayStart := time.Date(fileDate.Year(), fileDate.Month(), fileDate.Day(), 0, 0, 0, 0, time.UTC).UnixMilli()
	dayEnd := dayStart + 86_400_000 // exclusive

	if tsMin < dayStart || tsMin >= dayEnd || tsMax < dayStart || tsMax >= dayEnd {
		logger.Infof("INTEGRITY FAIL %s: rows span [%d..%d], outside %s",
			filepath.Base(destPath), tsMin, tsMax, fileDate.Format("2006-01-02"))
		return false
	}

	today := time.Now().UTC()
	todayStart := time.Date(today.Year(), today.Month(), today.Day(), 0, 0, 0, 0, time.UTC).UnixMilli()
	isToday := dayStart == todayStart

	// klines have a fixed cadence, so completeness can be checked exactly -
	// but only for days that are over; today's file is still in progress.
	if strings.EqualFold(dataType, "klines") && !isToday {
		if stepMs, ok := fixedIntervalMs(interval); ok {
			expectedFirst := dayStart
			expectedLast := dayEnd - stepMs
			if tsMin != expectedFirst || tsMax != expectedLast {
				logger.Infof("INTEGRITY FAIL %s: expected candles [%d..%d], got [%d..%d]",
					filepath.Base(destPath), expectedFirst, expectedLast, tsMin, tsMax)
				return false
			}
		}
	}

	// Cross-file continuity with the next day, only when that file could
	// plausibly exist by now (i.e. this file is not for today or later).
	if dayStart >= todayStart {
		return true
	}

	nextDate := fileDate.AddDate(0, 0, 1)
	nextPath, found := findNeighborFile(destPath, fileDate, nextDate)
	if !found {
		// Can't verify continuity - fall back to the self-contained result above.
		return true
	}

	nextMin, _, err := firstAndLastTimestamp(nextPath, parse)
	if err != nil {
		// Neighbor exists but couldn't be read - don't penalize this file for it.
		return true
	}

	if strings.EqualFold(dataType, "klines") {
		if stepMs, ok := fixedIntervalMs(interval); ok && nextMin-tsMax != stepMs {
			logger.Infof("INTEGRITY FAIL %s: boundary gap/overlap with %s (last=%d, next_first=%d, expected step=%d)",
				filepath.Base(destPath), filepath.Base(nextPath), tsMax, nextMin, stepMs)
			return false
		}
		return true
	}

	if nextMin <= tsMax {
		logger.Infof("INTEGRITY FAIL %s: overlaps with %s (last=%d, next_first=%d)",
			filepath.Base(destPath), filepath.Base(nextPath), tsMax, nextMin)
		return false
	}
	return true
}

// removeStaleArtifacts deletes destPath and, when it is a .zip, its
// already-extracted .csv sibling, so a subsequent download and extraction
// pass produces fresh content instead of leaving stale data behind.
func removeStaleArtifacts(destPath string) error {
	if err := os.Remove(destPath); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("remove stale file %s: %w", destPath, err)
	}
	if csvPath := siblingCSV(destPath); csvPath != destPath {
		if err := os.Remove(csvPath); err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("remove stale extracted file %s: %w", csvPath, err)
		}
	}
	return nil
}

// siblingCSV returns the .csv path corresponding to a .zip path, or path
// unchanged if it is not a .zip.
func siblingCSV(path string) string {
	if !strings.HasSuffix(path, ".zip") {
		return path
	}
	return strings.TrimSuffix(path, ".zip") + ".csv"
}

// dataTypeAndInterval recovers the Binance data type and, for klines, the
// interval subfolder from an S3 key of the form:
//
//	data/futures/um/daily/<dataType>/<symbol>/[<interval>/]<filename>
func dataTypeAndInterval(key string) (dataType, interval string) {
	parts := strings.Split(key, "/")
	if len(parts) < 7 {
		return "", ""
	}
	dataType = parts[4]
	if strings.EqualFold(dataType, "klines") && len(parts) >= 8 {
		interval = parts[6]
	}
	return dataType, interval
}

// fixedIntervalMs converts a fixed-duration kline interval (e.g. "1m", "4h",
// "1d") to its length in milliseconds. It returns (0, false) for
// variable-length intervals (weeks/months) that cannot be boundary-checked
// this way.
func fixedIntervalMs(interval string) (int64, bool) {
	if len(interval) < 2 {
		return 0, false
	}
	n, err := strconv.ParseInt(interval[:len(interval)-1], 10, 64)
	if err != nil {
		return 0, false
	}
	switch interval[len(interval)-1] {
	case 's':
		return n * 1_000, true
	case 'm':
		return n * 60_000, true
	case 'h':
		return n * 3_600_000, true
	case 'd':
		return n * 86_400_000, true
	default: // 'w', 'M' - variable-length, not supported
		return 0, false
	}
}

// findNeighborFile locates the local file (preferring an extracted .csv over
// a .zip) for the day right after fileDate, in the same directory as
// destPath. Returns ("", false) if neither is present.
func findNeighborFile(destPath string, fileDate, nextDate time.Time) (string, bool) {
	base := filepath.Base(destPath)
	oldStamp := fileDate.Format("2006-01-02")
	newStamp := nextDate.Format("2006-01-02")
	neighborBase := strings.Replace(base, oldStamp, newStamp, 1)
	if neighborBase == base {
		return "", false
	}

	dir := filepath.Dir(destPath)
	stem := strings.TrimSuffix(strings.TrimSuffix(neighborBase, ".zip"), ".csv")

	csvCandidate := filepath.Join(dir, stem+".csv")
	if _, err := os.Stat(csvCandidate); err == nil {
		return csvCandidate, true
	}
	zipCandidate := filepath.Join(dir, stem+".zip")
	if _, err := os.Stat(zipCandidate); err == nil {
		return zipCandidate, true
	}
	return "", false
}

// firstAndLastTimestamp streams path (a .csv file, or a .zip containing
// exactly one .csv entry) through parse and returns the smallest and largest
// timestamp found. Unparseable rows (e.g. a header line) are skipped.
func firstAndLastTimestamp(path string, parse rowParser) (min int64, max int64, err error) {
	if strings.HasSuffix(strings.ToLower(path), ".zip") {
		return firstAndLastTimestampInZip(path, parse)
	}

	f, err := os.Open(path)
	if err != nil {
		return 0, 0, fmt.Errorf("open %s: %w", path, err)
	}
	defer f.Close()

	return scanTimestamps(f, parse, path)
}

func firstAndLastTimestampInZip(zipPath string, parse rowParser) (int64, int64, error) {
	zr, err := zip.OpenReader(zipPath)
	if err != nil {
		return 0, 0, fmt.Errorf("open zip %s: %w", zipPath, err)
	}
	defer zr.Close()

	for _, zf := range zr.File {
		if !strings.HasSuffix(strings.ToLower(zf.Name), ".csv") {
			continue
		}
		rc, err := zf.Open()
		if err != nil {
			return 0, 0, fmt.Errorf("open %s in %s: %w", zf.Name, zipPath, err)
		}
		defer rc.Close()
		return scanTimestamps(rc, parse, zipPath)
	}

	return 0, 0, fmt.Errorf("no .csv entry found in %s", zipPath)
}

func scanTimestamps(r io.Reader, parse rowParser, source string) (min int64, max int64, err error) {
	cr := csv.NewReader(r)
	cr.FieldsPerRecord = -1

	seen := false
	for {
		record, readErr := cr.Read()
		if readErr == io.EOF {
			break
		}
		if readErr != nil {
			return 0, 0, fmt.Errorf("read %s: %w", source, readErr)
		}

		ts, _, parseErr := parse(record)
		if parseErr != nil {
			continue // header row or malformed line - skip
		}

		if !seen || ts < min {
			min = ts
		}
		if !seen || ts > max {
			max = ts
		}
		seen = true
	}

	if !seen {
		return 0, 0, fmt.Errorf("no valid rows found in %s", source)
	}
	return min, max, nil
}
