# Local patches (vaultcrab)

Fork of `github.com/charmbracelet/x/vt@v0.1.0` (MIT, see LICENSE), used via `replace` in the root
`go.mod`. Upstream tests are omitted except `scrollback_test.go`; `scrollback_pack_test.go` is ours.

1. `scrollback.go`: packed ring-buffer storage. Upstream keeps every scrollback line as `[]uv.Cell`
   (~112 bytes and several pointers per cell, i.e. ~85 MB for 10k lines × 120 columns) and removes the
   oldest line with an O(n) `slices.Delete`. Now a line is stored as its text, 2 bytes per cell
   (byte length and width) and a list of style runs; `Line`/`CellAt` unpack a copy on demand.
2. `emulator.go`: `Read` no longer checks `e.closed` (racy when `Close` runs on another goroutine
   while a reader is blocked); the pipe already returns `io.EOF` after `Close`.
3. `handlers.go`: DECSTBM/DECSLRM margins larger than the screen are clamped to the screen size (as in
   xterm) instead of producing a region past the last row; MikroTik RouterOS sends `ESC[1;9999r`, after
   which nothing scrolled and lines overwrote each other. DECID (`ESC Z`) is answered like DA1 —
   RouterOS probes the terminal with it.
4. Reflow on resize (`reflow.go`, `screen.go`, `scrollback.go`, `utf8.go`, `emulator.go`): a line
   that reaches the right margin and wraps is marked (`Screen.wrap`, kept through scrolling and
   insert/delete line, ended by erasing the end of the line; `packedLine.wrapped` in the scrollback,
   whose wrapped lines keep their trailing blanks). `Resize` rebuilds the main screen from the
   logical lines at the new width, keeping the cursor and the saved cursor in place in the text;
   a lower screen moves its top lines to the scrollback, a full screen that gets taller takes them
   back. The scrollback is rewrapped lazily, on its next read (`Scrollback.want`), so a window drag
   costs only the screen (≈3 ms with 10k lines of history instead of ≈350 ms). The alternate screen
   is not reflowed. A wide character that does not fit at the end of a line wraps whole (as in
   xterm) instead of being written past the margin.
