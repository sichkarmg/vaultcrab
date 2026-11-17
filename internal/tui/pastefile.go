// SPDX-License-Identifier: MIT

package tui

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/cursor"
	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"

	"github.com/sichkarmg/vaultcrab/internal/core"
)

// The "To a file" page of the paste dialog writes the text into a file on
// the server with a here-document typed into its shell:
//
//	cat > ~/'dir/file' << 'VAULTCRAB_EOF'
//	...the text...
//	VAULTCRAB_EOF
//
// Tab completes the path from a listing made in a channel of its own next
// to the shell (Session.Exec), which does not disturb it. Relative paths
// are taken from the home directory, both for the completion and for the
// file (the directory the shell is in now cannot be known).

// pasteFile is the state of the page.
type pasteFile struct {
	in     textinput.Model
	append bool // >> instead of >

	cands []string // completions of the last Tab (directories end with /)
	cand  int      // the candidate in the input (-1: their common prefix)
	shown string   // the input as the last completion left it
	seq   int      // of the last request (older answers are dropped)

	busy    string // what is running ("listing", "checking")
	err     string
	warn    string
	confirm bool // the next Enter writes (the file exists, or could not be checked)
}

type pasteCompMsg struct {
	seq   int
	input string
	names []string
	err   error
}

type pasteCheckMsg struct {
	seq           int
	exists, isDir bool
	err           error
}

func newPasteFile() *pasteFile {
	f := &pasteFile{cand: -1}
	f.in = textinput.New()
	f.in.Prompt = ""
	f.in.CharLimit = 4096
	f.in.Placeholder = "path: /etc/app.conf, ~/notes.txt, dir/file (from the home directory)"
	f.in.Cursor.SetMode(cursor.CursorStatic) // no blink timer while idle
	f.in.Focus()
	return f
}

// shellQuote quotes s for a POSIX shell.
func shellQuote(s string) string {
	if s == "" {
		return ""
	}
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// shellPath is path as a shell word: absolute as it is, "~" and "~/..."
// with the home directory, anything else relative to the home directory.
func shellPath(path string) string {
	switch {
	case path == "~":
		return "~"
	case strings.HasPrefix(path, "~/"):
		return "~/" + shellQuote(path[2:])
	case strings.HasPrefix(path, "/"):
		return shellQuote(path)
	}
	return "~/" + shellQuote(path)
}

// displayPath is path as the file will be: relative ones under ~/.
func displayPath(path string) string {
	if path == "~" || strings.HasPrefix(path, "~/") || strings.HasPrefix(path, "/") {
		return path
	}
	return "~/" + path
}

// splitPath splits the input at its last slash: the directory (with the
// slash) and the start of a name in it.
func splitPath(v string) (dir, base string) {
	i := strings.LastIndex(v, "/")
	return v[:i+1], v[i+1:]
}

// listCmd lists the directory dir of the input: one name a line,
// directories with a trailing slash, hidden ones too.
func listCmd(dir string) string {
	word := "~"
	if dir != "" {
		word = shellPath(dir)
	}
	return "cd -- " + word + " 2>/dev/null && ls -1Ap"
}

// completions filters a listing by the start of a name; hidden names only
// when it starts with a dot, as shells do.
func completions(listing []string, base string) []string {
	var out []string
	for _, n := range listing {
		if n == "" || n == "./" || n == "../" || !strings.HasPrefix(n, base) {
			continue
		}
		if strings.HasPrefix(n, ".") && !strings.HasPrefix(base, ".") {
			continue
		}
		out = append(out, n)
	}
	return out
}

func commonPrefix(names []string) string {
	if len(names) == 0 {
		return ""
	}
	p := names[0]
	for _, n := range names[1:] {
		for !strings.HasPrefix(n, p) {
			_, size := lastRune(p)
			p = p[:len(p)-size]
		}
	}
	return p
}

// lastRune returns the last rune of s and its size (the prefix is cut by
// whole characters).
func lastRune(s string) (rune, int) {
	for i := len(s) - 1; i >= 0; i-- {
		if s[i]&0xc0 != 0x80 {
			r := []rune(s[i:])
			return r[0], len(s) - i
		}
	}
	return 0, len(s)
}

// hereDoc is the command that writes text into path: its end marker is a
// line the text does not have.
func hereDoc(path, text string, appendTo bool) string {
	text = strings.NewReplacer("\r\n", "\n", "\r", "\n").Replace(text)
	text = strings.TrimSuffix(text, "\n")
	lines := map[string]bool{}
	for _, l := range strings.Split(text, "\n") {
		lines[l] = true
	}
	mark := "VAULTCRAB_EOF"
	for i := 2; lines[mark]; i++ {
		mark = fmt.Sprintf("VAULTCRAB_EOF_%d", i)
	}
	op := ">"
	if appendTo {
		op = ">>"
	}
	body := text + "\n"
	if text == "" {
		body = ""
	}
	return fmt.Sprintf("cat %s %s << '%s'\n%s%s", op, shellPath(path), mark, body, mark)
}

// execCmd runs cmd next to the shell of the session as a tea.Cmd.
func execCmd(s *core.Session, cmd string, done func([]byte, error) tea.Msg) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		out, err := s.Exec(ctx, cmd)
		return done(out, err)
	}
}

// complete is Tab (back: shift+tab): it cycles through the candidates of
// the last completion, or asks for the listing of the directory.
func (f *pasteFile) complete(p *pasteModal, back bool) tea.Cmd {
	if len(f.cands) > 1 && f.in.Value() == f.shown {
		d := 1
		if back {
			d = len(f.cands) - 1
		}
		f.cand = (f.cand + d) % len(f.cands)
		if f.cand < 0 {
			f.cand = 0
		}
		dir, _ := splitPath(f.in.Value())
		f.setValue(dir + f.cands[f.cand])
		return nil
	}
	f.seq++
	seq, input := f.seq, f.in.Value()
	dir, _ := splitPath(input)
	f.busy, f.err = "listing "+dirLabel(dir), ""
	return execCmd(p.s, listCmd(dir), func(out []byte, err error) tea.Msg {
		return pasteCompMsg{seq: seq, input: input, names: strings.Split(strings.TrimRight(string(out), "\n"), "\n"), err: err}
	})
}

// dirLabel names the directory part of the input ("~" when empty).
func dirLabel(dir string) string {
	if dir == "" {
		return "~"
	}
	return displayPath(dir)
}

func (f *pasteFile) setValue(v string) {
	f.in.SetValue(v)
	f.in.CursorEnd()
	f.shown = v
}

func (f *pasteFile) completed(msg pasteCompMsg) {
	if msg.seq != f.seq || msg.input != f.in.Value() {
		return // typed on meanwhile
	}
	f.busy = ""
	dir, base := splitPath(msg.input)
	if msg.err != nil {
		if errors.Is(msg.err, core.ErrNoExec) {
			f.err = "no completion: " + msg.err.Error()
		} else {
			f.err = "no directory " + dirLabel(dir)
		}
		return
	}
	c := completions(msg.names, base)
	switch len(c) {
	case 0:
		f.err, f.cands = "nothing matches", nil
	case 1:
		f.cands = nil
		f.setValue(dir + c[0])
	default:
		f.cands, f.cand = c, -1
		f.setValue(dir + commonPrefix(c))
	}
}

// submit is Enter: checks the path, then writes (after a confirmation if
// the file exists).
func (f *pasteFile) submit(m *Model, p *pasteModal) tea.Cmd {
	path := strings.TrimSpace(f.in.Value())
	switch {
	case path == "" || path == "~":
		f.err = "type the path of the file"
		return nil
	case strings.HasSuffix(path, "/"):
		f.err = "the path is a directory: add a file name"
		return nil
	}
	if st, _ := p.s.State(); st != core.SessionConnected || p.s.Exited() {
		f.err = "the session is not connected"
		return nil
	}
	if p.s.Term.AltScreen() {
		f.err = "a full-screen program runs in the session: leave it for the shell prompt first"
		return nil
	}
	if f.confirm {
		return f.write(m, p, path)
	}
	f.seq++
	seq, w := f.seq, shellPath(path)
	f.busy, f.err = "checking "+displayPath(path), ""
	cmd := fmt.Sprintf("if [ -d %s ]; then echo dir; elif [ -e %s ]; then echo file; else echo none; fi", w, w)
	return execCmd(p.s, cmd, func(out []byte, err error) tea.Msg {
		r := strings.TrimSpace(string(out))
		return pasteCheckMsg{seq: seq, exists: r == "file", isDir: r == "dir", err: err}
	})
}

func (f *pasteFile) checked(m *Model, p *pasteModal, msg pasteCheckMsg) tea.Cmd {
	if msg.seq != f.seq {
		return nil
	}
	f.busy = ""
	path := strings.TrimSpace(f.in.Value())
	switch {
	case msg.err != nil:
		f.warn, f.confirm = "could not check the file ("+msg.err.Error()+"): Enter writes anyway", true
	case msg.isDir:
		f.err = displayPath(path) + " is a directory"
	case msg.exists && !f.append:
		f.warn, f.confirm = displayPath(path)+" exists: Enter replaces it (Ctrl+O: append to it instead)", true
	default:
		return f.write(m, p, path)
	}
	return nil
}

// write types the here-document into the shell and runs it.
func (f *pasteFile) write(m *Model, p *pasteModal, path string) tea.Cmd {
	m.modal = nil
	paste(p.s, hereDoc(path, p.text, f.append))
	p.s.Term.SendKey(core.Key{Code: core.KeyEnter})
	verb := "written to"
	if f.append {
		verb = "appended to"
	}
	m.notify(false, "%d lines %s %s by a here-document in the shell", len(p.lines), verb, displayPath(path))
	return nil
}

func (f *pasteFile) update(m *Model, p *pasteModal, k tea.KeyMsg) tea.Cmd {
	switch k.String() {
	case "esc":
		p.page = pagePreview
		return nil
	case "enter":
		return f.submit(m, p)
	case "tab":
		return f.complete(p, false)
	case "shift+tab":
		return f.complete(p, true)
	case "ctrl+o":
		f.append = !f.append
		f.confirm, f.warn = false, ""
		return nil
	}
	before := f.in.Value()
	f.in, _ = f.in.Update(k)
	if f.in.Value() != before {
		f.cands, f.cand, f.err, f.warn, f.confirm = nil, -1, "", "", false
	}
	return nil
}

func (f *pasteFile) view(m *Model, p *pasteModal, w int) string {
	var b strings.Builder
	b.WriteString(fmt.Sprintf("Write %s into a file on %s\n", stBold.Render(fmt.Sprintf("%d lines", len(p.lines))), stBold.Render(p.s.Host.Name)))
	b.WriteString(stDim.Render("by a here-document typed into its shell: the session must be at the shell prompt (sh, bash, zsh).") + "\n\n")
	f.in.Width = max(20, min(80, w-16))
	b.WriteString(stKey.Render("File ") + " " + f.in.View() + "\n")
	mode := "overwrite (>)"
	if f.append {
		mode = "append (>>)"
	}
	b.WriteString(stDim.Render("Mode ") + " " + mode + stDim.Render("   Ctrl+O: change") + "\n")
	if len(f.cands) > 0 {
		b.WriteString("\n" + candidateGrid(f.cands, f.cand, w-8, 6) + "\n")
	}
	b.WriteString("\n")
	switch {
	case f.busy != "":
		b.WriteString(stDim.Render(f.busy+m.g.ell) + "\n")
	case f.err != "":
		b.WriteString(stErr.Render(f.err) + "\n")
	case f.warn != "":
		b.WriteString(stWarn.Render(f.warn) + "\n")
	default:
		b.WriteString("\n")
	}
	if !p.s.Term.BracketedPaste() && strings.Contains(p.text, "\t") {
		b.WriteString(stWarn.Render("The shell takes no bracketed paste: it may complete the tabs of the text.") + "\n")
	}
	b.WriteString("\n")
	path := strings.TrimSpace(f.in.Value())
	if path == "" {
		path = "FILE"
	}
	doc := strings.Split(hereDoc(path, p.text, f.append), "\n")
	b.WriteString(stDim.Render(ansi.Truncate(doc[0], w-8, m.g.ell)) + "\n")
	b.WriteString(stDim.Render(fmt.Sprintf("  %s %d lines %s", m.g.ell, len(p.lines), m.g.ell)) + "\n")
	b.WriteString(stDim.Render(doc[len(doc)-1]) + "\n\n")
	b.WriteString(stDim.Render("Enter: write · Tab: complete the path · Ctrl+O: overwrite/append · Esc: back"))
	return b.String()
}

// candidateGrid lays the completions out in columns, at most rows lines;
// cur is highlighted.
func candidateGrid(cands []string, cur, w, rows int) string {
	cw := 0
	for _, c := range cands {
		cw = max(cw, ansi.StringWidth(c))
	}
	cw = min(cw+2, max(10, w))
	cols := max(1, w/cw)
	n := min(len(cands), cols*rows)
	lines := (n + cols - 1) / cols
	var b strings.Builder
	for r := 0; r < lines; r++ {
		for c := 0; c < cols; c++ {
			i := c*lines + r
			if i >= n {
				break
			}
			cell := padRight(ansi.Truncate(cands[i], cw-2, "~"), cw-2)
			if i == cur {
				cell = stSelected.Render(cell)
			} else if strings.HasSuffix(cands[i], "/") {
				cell = stGroup.Render(cell)
			}
			b.WriteString(cell + "  ")
		}
		if r < lines-1 {
			b.WriteString("\n")
		}
	}
	if n < len(cands) {
		b.WriteString("\n" + stDim.Render(fmt.Sprintf("… %d more: type more of the name", len(cands)-n)))
	}
	return b.String()
}
