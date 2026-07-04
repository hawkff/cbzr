//go:build windows

package render

// CellSize returns an assumed pixel size of one terminal cell.
func CellSize() (cw, ch float64) { return 8, 16 }

// QueryCellSize is a no-op on Windows.
func QueryCellSize() {}
