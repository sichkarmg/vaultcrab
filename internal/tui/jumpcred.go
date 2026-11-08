// SPDX-License-Identifier: MIT

package tui

import (
	"slices"
	"strings"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"

	"github.com/sichkarmg/vaultcrab/internal/core"
)

// credPicker chooses the credential of the addresses typed in step 2 of the
// jump picker (Ctrl+K): the default, a password typed once, the agent, a
// key file, or a vault item (marked "credential", or an SSH host).
type credPicker struct {
	in     textinput.Model
	all    []core.CredChoice
	shown  []core.CredChoice
	cur    int
	top    int
	path   bool // typing the path of a key file
	err    string
	listY  int
	rows   int
	choose func(ref string) // the choice is made
	back   func()           // Esc
}

func newCredPicker(m *Model, via string, cur string, choose func(string), back func()) *credPicker {
	c := &credPicker{all: m.svc.JumpCreds(via), choose: choose, back: back}
	c.in = textinput.New()
	c.in.Prompt = "> "
	c.in.Placeholder = "search credentials"
	c.in.CharLimit = 255
	c.in.Focus()
	c.filter()
	if i := slices.IndexFunc(c.shown, func(x core.CredChoice) bool { return x.Ref == cur && !x.AskPath }); i >= 0 {
		c.cur = i
		c.scroll()
	}
	return c
}

func (c *credPicker) filter() {
	q := strings.TrimSpace(c.in.Value())
	type scored struct {
		x core.CredChoice
		s int
	}
	var out []scored
	for _, x := range c.all {
		if s, ok := core.FuzzyMatch(q, x.Label+" "+x.Detail); ok {
			out = append(out, scored{x, s})
		}
	}
	if q != "" {
		slices.SortStableFunc(out, func(a, b scored) int { return b.s - a.s })
	}
	c.shown = c.shown[:0]
	for _, o := range out {
		c.shown = append(c.shown, o.x)
	}
	c.cur, c.top = 0, 0
}

func (c *credPicker) scroll() {
	if c.cur < c.top {
		c.top = c.cur
	}
	if c.cur >= c.top+jumpListRows {
		c.top = c.cur - jumpListRows + 1
	}
}

func (c *credPicker) move(d int) {
	if len(c.shown) == 0 {
		return
	}
	c.cur = max(0, min(len(c.shown)-1, c.cur+d))
	c.scroll()
}

func (c *credPicker) pick() {
	if c.path {
		ref, err := core.CheckKeyFile(c.in.Value())
		if err != nil {
			c.err = err.Error()
			return
		}
		c.choose(ref)
		return
	}
	if c.cur < 0 || c.cur >= len(c.shown) {
		return
	}
	x := c.shown[c.cur]
	if x.AskPath {
		c.path, c.err = true, ""
		c.in.SetValue("~/.ssh/")
		c.in.Placeholder = "path of a private key file"
		c.in.CursorEnd()
		return
	}
	c.choose(x.Ref)
}

func (c *credPicker) update(k tea.KeyMsg) tea.Cmd {
	switch k.String() {
	case "esc":
		if c.path {
			c.path, c.err = false, ""
			c.in.SetValue("")
			c.in.Placeholder = "search credentials"
			c.filter()
			return nil
		}
		c.back()
		return nil
	case "enter":
		c.pick()
		return nil
	case "up", "ctrl+p":
		c.move(-1)
		return nil
	case "down", "ctrl+n":
		c.move(1)
		return nil
	case "pgup":
		c.move(-jumpListRows)
		return nil
	case "pgdown":
		c.move(jumpListRows)
		return nil
	}
	old := c.in.Value()
	var cmd tea.Cmd
	c.in, cmd = c.in.Update(k)
	if c.in.Value() != old {
		c.err = ""
		if !c.path {
			c.filter()
		}
	}
	return cmd
}

// view renders the chooser into b; line is the dialog row of its first line.
func (c *credPicker) view(m *Model, b *strings.Builder, line, width int) {
	write := func(s string) {
		b.WriteString(s + "\n")
		line++
	}
	if c.path {
		write(stDim.Render("The private key file for the typed addresses (a passphrase is asked once):"))
		write("")
		write(c.in.View())
		if c.err != "" {
			write("")
			write(stErr.Render(c.err))
		}
		write("")
		write(stDim.Render("Enter: use this key · Esc: back"))
		c.rows = 0
		return
	}
	write(stDim.Render("Credentials for the typed addresses (remembered for this jump host):"))
	write("")
	write(c.in.View())
	write("")
	c.listY, c.rows = line, 0
	if len(c.shown) == 0 {
		write(stDim.Render("  nothing matches"))
	}
	for i := c.top; i < len(c.shown) && i < c.top+jumpListRows; i++ {
		x := c.shown[i]
		l := padRight(trunc(x.Label, 28), 28) + " " + stDim.Render(trunc(x.Detail, max(10, width-32)))
		if i == c.cur {
			l = stTabActive.Render("▸ ") + l
		} else {
			l = "  " + l
		}
		write(ansi.Truncate(l, width, m.g.ell))
		c.rows++
	}
	if n := len(c.shown); n > c.top+jumpListRows {
		write(stDim.Render("  " + m.g.ell + " " + itoa(n-c.top-jumpListRows) + " more"))
	}
	write("")
	write(stDim.Render("type to search · ↑/↓ choose · Enter: use · Esc: back"))
}

func (c *credPicker) mouse(msg tea.MouseMsg, y int) {
	switch msg.Button {
	case tea.MouseButtonWheelUp:
		c.move(-1)
		return
	case tea.MouseButtonWheelDown:
		c.move(1)
		return
	}
	if c.path || msg.Action != tea.MouseActionPress || msg.Button != tea.MouseButtonLeft {
		return
	}
	r := y - c.listY
	if r < 0 || r >= c.rows {
		return
	}
	i := c.top + r
	if i >= len(c.shown) {
		return
	}
	again := i == c.cur
	c.cur = i
	if again {
		c.pick()
	}
}
