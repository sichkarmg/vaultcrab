# Local patches (vaultcrab)

Fork of `github.com/charmbracelet/ultraviolet@v0.0.0-20260303162955-0b88c25f3fff` (MIT, see LICENSE),
used via `replace` in the root `go.mod`. Tests and testdata are omitted.

1. `buffer.go`: `InsertLineArea` / `DeleteLineArea` rotate line slices for full-width regions
   instead of copying every cell. Scrolling a 120x40 screen no longer copies ~4800 cells per line,
   which makes bulk output (`yes`, `cat` of large files) usable in the embedded terminal.
