package infra

import (
	"archive/zip"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// klineRow builds a Binance Vision kline CSV row for the given open time and
// a 1h interval.
func klineRow(openTimeMs int64) string {
	closeTimeMs := openTimeMs + 3_600_000 - 1
	return fmt.Sprintf("%d,1,1,1,1,1,%d,1,1,1,1,0\n", openTimeMs, closeTimeMs)
}

func writeCSV(t *testing.T, path string, rows []string) {
	t.Helper()
	data := ""
	for _, r := range rows {
		data += r
	}
	if err := os.WriteFile(path, []byte(data), 0644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

func writeZip(t *testing.T, zipPath, entryName string, rows []string) {
	t.Helper()
	f, err := os.Create(zipPath)
	if err != nil {
		t.Fatalf("create %s: %v", zipPath, err)
	}
	defer f.Close()

	zw := zip.NewWriter(f)
	w, err := zw.Create(entryName)
	if err != nil {
		t.Fatalf("zip create entry: %v", err)
	}
	for _, r := range rows {
		if _, err := w.Write([]byte(r)); err != nil {
			t.Fatalf("zip write: %v", err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatalf("zip close: %v", err)
	}
}

func fullDayKlines(dayStart int64) []string {
	rows := make([]string, 24)
	for i := 0; i < 24; i++ {
		rows[i] = klineRow(dayStart + int64(i)*3_600_000)
	}
	return rows
}

const klinesKey = "data/futures/um/daily/klines/BTCUSDT/1h/BTCUSDT-1h-2024-01-10.zip"

func dayStartMs(y int, m time.Month, d int) int64 {
	return time.Date(y, m, d, 0, 0, 0, 0, time.UTC).UnixMilli()
}

func TestContentIsComplete_ValidWithNextDayZip(t *testing.T) {
	dir := t.TempDir()
	dayStart := dayStartMs(2024, 1, 10)
	nextDayStart := dayStartMs(2024, 1, 11)

	destPath := filepath.Join(dir, "BTCUSDT-1h-2024-01-10.zip")
	writeZip(t, destPath, "BTCUSDT-1h-2024-01-10.csv", fullDayKlines(dayStart))

	// Neighbor present as a plain .csv - exercises the CSV-preferred lookup.
	nextPath := filepath.Join(dir, "BTCUSDT-1h-2024-01-11.csv")
	writeCSV(t, nextPath, fullDayKlines(nextDayStart))

	fileDate := time.Date(2024, 1, 10, 0, 0, 0, 0, time.UTC)
	if !contentIsComplete(klinesKey, destPath, fileDate) {
		t.Fatal("expected content to be complete")
	}
}

func TestContentIsComplete_MissingLastCandle(t *testing.T) {
	dir := t.TempDir()
	dayStart := dayStartMs(2024, 1, 10)

	rows := fullDayKlines(dayStart)[:23] // drop the last hour
	destPath := filepath.Join(dir, "BTCUSDT-1h-2024-01-10.csv")
	writeCSV(t, destPath, rows)

	fileDate := time.Date(2024, 1, 10, 0, 0, 0, 0, time.UTC)
	if contentIsComplete(klinesKey, destPath, fileDate) {
		t.Fatal("expected content to be flagged incomplete (missing last candle)")
	}
}

func TestContentIsComplete_BoundaryGapWithNextDay(t *testing.T) {
	dir := t.TempDir()
	dayStart := dayStartMs(2024, 1, 10)
	nextDayStart := dayStartMs(2024, 1, 11)

	destPath := filepath.Join(dir, "BTCUSDT-1h-2024-01-10.csv")
	writeCSV(t, destPath, fullDayKlines(dayStart))

	// Next day is missing its first candle - creates a 2h gap at the boundary.
	nextPath := filepath.Join(dir, "BTCUSDT-1h-2024-01-11.csv")
	writeCSV(t, nextPath, fullDayKlines(nextDayStart)[1:])

	fileDate := time.Date(2024, 1, 10, 0, 0, 0, 0, time.UTC)
	if contentIsComplete(klinesKey, destPath, fileDate) {
		t.Fatal("expected content to be flagged incomplete (boundary gap)")
	}
}

func TestContentIsComplete_NoNeighborFallsBackToSelfContained(t *testing.T) {
	dir := t.TempDir()
	dayStart := dayStartMs(2024, 1, 10)

	destPath := filepath.Join(dir, "BTCUSDT-1h-2024-01-10.csv")
	writeCSV(t, destPath, fullDayKlines(dayStart)) // complete, but no D+1 file at all

	fileDate := time.Date(2024, 1, 10, 0, 0, 0, 0, time.UTC)
	if !contentIsComplete(klinesKey, destPath, fileDate) {
		t.Fatal("expected content to be valid when the next day's file is simply absent")
	}
}

func TestContentIsComplete_TodayIsExemptFromFullDayCheck(t *testing.T) {
	dir := t.TempDir()
	today := time.Now().UTC()
	todayStart := time.Date(today.Year(), today.Month(), today.Day(), 0, 0, 0, 0, time.UTC)

	// Only a few hours in - nowhere near a full day.
	rows := fullDayKlines(todayStart.UnixMilli())[:3]
	destPath := filepath.Join(dir, "BTCUSDT-1h-today.csv")
	writeCSV(t, destPath, rows)

	if !contentIsComplete(klinesKey, destPath, todayStart) {
		t.Fatal("expected today's partial file to be considered valid")
	}
}

func TestContentIsComplete_UnknownDataTypeAssumesValid(t *testing.T) {
	dir := t.TempDir()
	destPath := filepath.Join(dir, "BTCUSDT-liquidationSnapshot-2024-01-10.csv")
	writeCSV(t, destPath, []string{"garbage,not,parseable\n"})

	key := "data/futures/um/daily/liquidationSnapshot/BTCUSDT/BTCUSDT-liquidationSnapshot-2024-01-10.csv"
	fileDate := time.Date(2024, 1, 10, 0, 0, 0, 0, time.UTC)
	if !contentIsComplete(key, destPath, fileDate) {
		t.Fatal("expected unverifiable data type to be assumed valid")
	}
}

func aggTradeRow(transactTimeMs int64, id int64) string {
	return fmt.Sprintf("%d,1.0,1.0,%d,%d,%d,false\n", id, id, id, transactTimeMs)
}

func TestContentIsComplete_AggTradeOverlapWithNextDay(t *testing.T) {
	dir := t.TempDir()
	dayStart := dayStartMs(2024, 1, 10)
	nextDayStart := dayStartMs(2024, 1, 11)

	key := "data/futures/um/daily/aggTrades/BTCUSDT/BTCUSDT-aggTrades-2024-01-10.csv"
	destPath := filepath.Join(dir, "BTCUSDT-aggTrades-2024-01-10.csv")
	writeCSV(t, destPath, []string{
		aggTradeRow(dayStart+1000, 1),
		aggTradeRow(dayStart+86_000_000, 2), // last trade near end of day D
	})

	nextPath := filepath.Join(dir, "BTCUSDT-aggTrades-2024-01-11.csv")
	writeCSV(t, nextPath, []string{
		// Duplicate/overlapping timestamp with D's last row - simulates the
		// "insertion program" corruption scenario.
		aggTradeRow(dayStart+86_000_000, 3),
		aggTradeRow(nextDayStart+2000, 4),
	})

	fileDate := time.Date(2024, 1, 10, 0, 0, 0, 0, time.UTC)
	if contentIsComplete(key, destPath, fileDate) {
		t.Fatal("expected overlap at day boundary to be flagged incomplete")
	}
}
