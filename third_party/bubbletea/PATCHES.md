# Local patches (vaultcrab)

Fork of `github.com/charmbracelet/bubbletea@v1.3.10` (MIT, see LICENSE), used via `replace` in the root
`go.mod`. Tests, examples and tutorials are omitted.

1. `key_windows.go` (`readConInputs`): characters missing from the active keyboard layout (for example
   `@ # $ ^ & ~` with a Russian layout) arrive from ConPTY/conhost as an Alt+Numpad sequence whose
   character is carried by the key-up event of `VK_MENU`. Upstream drops all key-up events, so these
   characters were lost (e.g. in passwords typed over SSH into a Windows host). The patch emits the
   character from that key-up and skips the character-less events of the sequence.
2. `key_windows.go` (`mouseEventButton`): the previous button state saved from a `MOUSE_WHEELED` event
   includes the wheel delta in its high word, so the first click after scrolling produced a press with
   no button (and was ignored). Only the five button bits are compared now.
3. `standard_renderer.go` (`listen`, `write`, `flush`): upstream flushes on a ticker at the framerate
   (60 times a second) even while nothing changes, and each tick copied the whole frame to compare it
   with the last one. An idle program kept waking up (notably heating macOS laptops). Now `write` signals
   a pending frame and `listen` sleeps until then; frames are still throttled to the framerate, and the
   comparison does not copy.
4. `key_windows.go` (`peekConsInput`), `inputreader_windows.go`: upstream polls the console input every
   16 ms. The reader now waits on the console input handle and on an event set by `Cancel`
   (`WaitForMultipleObjects`), falling back to polling if the event cannot be created.
