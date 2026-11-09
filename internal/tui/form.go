// SPDX-License-Identifier: MIT

package tui

import (
	"context"
	"fmt"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/textarea"
	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/sichkarmg/vaultcrab/internal/core"
)

// Form fields.
const (
	fName = iota
	fType
	fUser
	fHost
	fPort
	fPath
	fPassword
	fFolder
	fTags
	fKnock // SSH hosts only
	fNotes // a textarea, not in in[]
	fOrg
	fCollection
	fieldCount
)

var fieldLabels = [fieldCount]string{"Name", "Type", "User", "Host", "Port", "Path", "Password", "Folder", "Tags", "Knock", "Notes", "Organization", "Collection"}

// hostForm adds or edits a host item (only while the vault is online).
type hostForm struct {
	id     string // "" = new item
	copyOf string // new item copied from this item (password and other content kept)
	in     [fieldCount]textinput.Model
	focus  int
	scheme int // index in core.Schemes
	orgs   []core.Org
	org    int // 0 = personal, i = orgs[i-1]
	cols   []core.Collection
	col    int
	busy   bool
	frame  int // spinner frame while saving
	err    string

	// organization and collections of the edited item: a personal item can
	// be moved into an organization, an organization item can change its
	// collection (Bitwarden cannot move items out of an organization)
	origOrg  int // like org; -1 = an organization the form does not know
	origCols []string

	// notes of the item: read from the vault when the form opens (they may
	// hold secrets, so the host list does not keep them); saved only when
	// changed
	notes       textarea.Model
	notesFrom   string // item whose notes are loaded (the edited or copied one)
	notesLoaded bool
	notesOrig   string
	notesErr    string

	// the password shown (Ctrl+R or [Show]): what is typed, or the current
	// password of the item read from the vault (shown, never put into the
	// field: a saved key keeps its lines)
	pwShow    bool
	pwCur     string // the current password ("" = not read)
	pwReading bool
	pwErr     string
	pwRow     int // dialog row of the password field (mouse)
	pwBtnX    int // dialog column of [Show]/[Hide]
}

type formPasswordMsg struct {
	id   string
	text string
	err  error
}

const notesHeight = 4

type formNotesMsg struct {
	id    string
	notes string
	err   error
}

func newHostForm(m *Model, h *core.Host, folder string) *hostForm {
	f := &hostForm{}
	for i := range f.in {
		in := textinput.New()
		in.Prompt = ""
		in.CharLimit = 4096
		f.in[i] = in
	}
	f.in[fPassword].EchoMode = textinput.EchoPassword
	f.in[fPassword].EchoCharacter = '•'
	if m.opt.Icons.ASCII() {
		f.in[fPassword].EchoCharacter = '*'
	}
	f.in[fTags].Placeholder = "comma separated"
	f.in[fKnock].Placeholder = "port knocking: 7000,8000/udp,9000 (empty: none)"
	f.notes = textarea.New()
	f.notes.ShowLineNumbers = false
	f.notes.Prompt = ""
	f.notes.SetHeight(notesHeight)
	f.notes.CharLimit = 10000 // the vault limit
	f.notes.Placeholder = "notes (Enter: new line)"
	f.notes.FocusedStyle.CursorLine = lipgloss.NewStyle() // no highlighted line
	f.notesLoaded = true                                  // a new item has none
	f.in[fFolder].Placeholder = "A/B (new folders are created) · PgUp/PgDn: existing"
	f.in[fPath].Placeholder = "share or directory (smb, ftp, sftp)"
	snap := m.svc.Snapshot()
	f.orgs = append(f.orgs, snap.Orgs...)
	sort.Slice(f.orgs, func(i, j int) bool { return strings.ToLower(f.orgs[i].Name) < strings.ToLower(f.orgs[j].Name) })
	if h != nil {
		f.id, f.notesFrom, f.notesLoaded = h.ID, h.ID, false
		f.in[fName].SetValue(h.Name)
		f.in[fUser].SetValue(h.User)
		f.in[fHost].SetValue(h.Addr)
		if h.Port != 0 && h.Port != h.Scheme.DefaultPort() {
			f.in[fPort].SetValue(strconv.Itoa(h.Port))
		}
		f.in[fPath].SetValue(h.Path)
		f.in[fTags].SetValue(strings.Join(h.Tags, ", "))
		f.in[fKnock].SetValue(h.Fields[core.FieldKnock])
		f.in[fFolder].SetValue(strings.Join(snap.FolderPath(h), "/"))
		f.in[fPassword].Placeholder = "unchanged (type to replace; a pasted key may be one line)"
		for i, sc := range core.Schemes {
			if sc == h.Scheme {
				f.scheme = i
			}
		}
		for i, o := range f.orgs {
			if o.ID == h.OrgID {
				f.org = i + 1
			}
		}
		f.origOrg, f.origCols = f.org, h.CollectionIDs
		if h.OrgID != "" && f.org == 0 {
			f.origOrg = -1 // an organization not in the snapshot: left as is
		}
	} else {
		f.in[fFolder].SetValue(folder)
		f.in[fPassword].Placeholder = "password or private key (a pasted key may be one line)"
	}
	f.updatePort()
	f.loadCollections(m)
	f.pickCollection(f.origCols)
	f.setFocus(fName)
	return f
}

// pickCollection selects the first of ids among the offered collections.
func (f *hostForm) pickCollection(ids []string) {
	for i, c := range f.cols {
		if len(ids) > 0 && c.ID == ids[0] {
			f.col = i
		}
	}
}

// moving reports whether saving moves a personal item into an organization.
func (f *hostForm) moving() bool { return !f.isNew() && f.origOrg == 0 && f.org > 0 }

func (f *hostForm) isNew() bool { return f.id == "" }

// loadNotes fills the notes field: from the details panel when it shows
// this item's notes, otherwise read from the vault.
func (f *hostForm) loadNotes(m *Model) tea.Cmd {
	if f.notesLoaded {
		return nil
	}
	if n := m.notes; n.id == f.notesFrom && !n.loading && n.err == nil {
		f.setNotes(n.text)
		return nil
	}
	if !m.svc.CanReadNotes() {
		f.notesErr = "cannot be read now (kept unchanged)"
		return nil
	}
	id, svc, ctx := f.notesFrom, m.svc, m.ctx
	return func() tea.Msg {
		c, cancel := context.WithTimeout(ctx, 30*time.Second)
		defer cancel()
		n, err := svc.Notes(c, id)
		return formNotesMsg{id: id, notes: n, err: err}
	}
}

func (f *hostForm) setNotes(text string) {
	f.notes.SetValue(text)
	f.notes.CursorStart()
	f.notesOrig, f.notesLoaded = f.notes.Value(), true
}

// newCopyForm opens the form of a new item prefilled from h; the name gets
// " copyN" (the first N not used yet). The password, notes and custom fields
// of h are copied by the vault when saving.
func newCopyForm(m *Model, h *core.Host) *hostForm {
	f := newHostForm(m, h, "")
	f.id, f.copyOf = "", h.ID
	names := map[string]bool{}
	for _, x := range m.svc.Snapshot().Hosts {
		names[strings.ToLower(x.Name)] = true
	}
	name := ""
	for n := 1; name == "" || names[strings.ToLower(name)]; n++ {
		name = fmt.Sprintf("%s copy%d", h.Name, n)
	}
	f.in[fName].SetValue(name)
	f.in[fPassword].Placeholder = "copied from " + h.Name + " (type to replace)"
	return f
}

func (f *hostForm) updatePort() {
	f.in[fPort].Placeholder = fmt.Sprintf("%d", core.Schemes[f.scheme].DefaultPort())
}

func (f *hostForm) loadCollections(m *Model) {
	f.cols, f.col = nil, 0
	if f.org == 0 {
		return
	}
	snap := m.svc.Snapshot()
	for _, c := range snap.Collections {
		if c.OrgID == f.orgs[f.org-1].ID {
			f.cols = append(f.cols, c)
		}
	}
}

// fields returns the navigable fields: the organization for new and
// personal items, the collection for organization items.
func (f *hostForm) fields() []int {
	out := []int{fName, fType, fUser, fHost, fPort, fPath, fPassword, fFolder, fTags}
	if core.Schemes[f.scheme] == core.SchemeSSH {
		out = append(out, fKnock)
	}
	out = append(out, fNotes)
	if f.isNew() || f.origOrg == 0 {
		out = append(out, fOrg)
	}
	if f.org > 0 {
		out = append(out, fCollection)
	}
	return out
}

func (f *hostForm) setFocus(i int) {
	f.focus = i
	if i == fNotes {
		f.notes.Focus()
	} else {
		f.notes.Blur()
	}
	for j := range f.in {
		if j == i {
			f.in[j].Focus()
		} else {
			f.in[j].Blur()
		}
	}
}

func (f *hostForm) move(d int) {
	fs := f.fields()
	at := 0
	for i, x := range fs {
		if x == f.focus {
			at = i
		}
	}
	f.setFocus(fs[(at+d+len(fs))%len(fs)])
}

type formSavedMsg struct {
	name string
	err  error
}

func (f *hostForm) update(m *Model, msg tea.Msg) tea.Cmd {
	if _, ok := msg.(spinTickMsg); ok {
		if !f.busy {
			return nil
		}
		f.frame++
		return spinTick()
	}
	if r, ok := msg.(formSavedMsg); ok {
		f.busy = false
		if r.err != nil {
			f.err = r.err.Error()
			return nil
		}
		m.modal = nil
		m.notes = noteState{} // read again for the details panel
		m.notify(false, "saved %s", r.name)
		return nil
	}
	if r, ok := msg.(formPasswordMsg); ok {
		f.pwReading = false
		if r.id == f.pwSource() && f.pwShow {
			if r.err != nil {
				f.pwErr = r.err.Error()
			} else {
				f.pwCur = r.text
				if f.pwCur == "" {
					f.pwErr = "the item has no password"
				}
			}
		}
		return nil
	}
	if r, ok := msg.(formNotesMsg); ok {
		if r.id == f.notesFrom && !f.notesLoaded {
			if r.err != nil {
				f.notesErr = r.err.Error() + " (kept unchanged)"
			} else {
				f.setNotes(r.notes)
			}
		}
		return nil
	}
	k, ok := msg.(tea.KeyMsg)
	if !ok {
		if f.focus == fNotes {
			var cmd tea.Cmd
			f.notes, cmd = f.notes.Update(msg)
			return cmd
		}
		var cmd tea.Cmd
		f.in[f.focus], cmd = f.in[f.focus].Update(msg)
		return cmd
	}
	if f.busy {
		return nil
	}
	if f.focus == fNotes {
		// Enter and the arrows edit the notes; Tab leaves, Ctrl+S saves
		switch k.String() {
		case "esc", "tab", "shift+tab", "ctrl+s":
		default:
			if f.notesLoaded {
				var cmd tea.Cmd
				f.notes, cmd = f.notes.Update(msg)
				return cmd
			}
			return nil
		}
	}
	switch k.String() {
	case "esc":
		f.in[fPassword].SetValue("")
		f.hidePassword()
		m.modal = nil
		return nil
	case "ctrl+r":
		if f.focus == fPassword {
			return f.togglePassword(m)
		}
	case "tab", "down":
		f.move(1)
		return nil
	case "shift+tab", "up":
		f.move(-1)
		return nil
	case "enter", "ctrl+s":
		return f.save(m)
	}
	switch f.focus {
	case fType, fOrg, fCollection:
		d := 0
		switch k.String() {
		case "right", " ", "l":
			d = 1
		case "left", "h":
			d = -1
		}
		switch f.focus {
		case fType:
			f.scheme = (f.scheme + d + len(core.Schemes)) % len(core.Schemes)
			f.updatePort()
		case fOrg:
			f.org = (f.org + d + len(f.orgs) + 1) % (len(f.orgs) + 1)
			f.loadCollections(m)
			if f.org == f.origOrg {
				f.pickCollection(f.origCols)
			}
		case fCollection:
			if len(f.cols) > 0 {
				f.col = (f.col + d + len(f.cols)) % len(f.cols)
			}
		}
		return nil
	case fFolder:
		if k.String() == "pgup" || k.String() == "pgdown" {
			f.cycleFolder(m, k.String() == "pgdown")
			return nil
		}
	}
	var cmd tea.Cmd
	f.in[f.focus], cmd = f.in[f.focus].Update(msg)
	return cmd
}

// cycleFolder replaces the folder text with the next/previous existing folder.
func (f *hostForm) cycleFolder(m *Model, next bool) {
	snap := m.svc.Snapshot()
	paths := []string{""}
	for _, fo := range snap.Folders {
		paths = append(paths, strings.Join(fo.Path, "/"))
	}
	cur := f.in[fFolder].Value()
	i := 0
	for j, p := range paths {
		if strings.EqualFold(p, cur) {
			i = j
		}
	}
	if next {
		i = (i + 1) % len(paths)
	} else {
		i = (i + len(paths) - 1) % len(paths)
	}
	f.in[fFolder].SetValue(paths[i])
	f.in[fFolder].CursorEnd()
}

func (f *hostForm) save(m *Model) tea.Cmd {
	e := core.HostEdit{
		Name:   strings.TrimSpace(f.in[fName].Value()),
		Scheme: core.Schemes[f.scheme],
		User:   strings.TrimSpace(f.in[fUser].Value()),
		Addr:   strings.TrimSpace(f.in[fHost].Value()),
		Path:   strings.Trim(strings.TrimSpace(f.in[fPath].Value()), "/"),
	}
	if p := strings.TrimSpace(f.in[fPort].Value()); p != "" {
		n, err := strconv.Atoi(p)
		if err != nil || n < 1 || n > 65535 {
			f.err = "port must be 1-65535"
			return nil
		}
		e.Port = n
	}
	for _, t := range strings.FieldsFunc(f.in[fTags].Value(), func(r rune) bool { return r == ',' || r == ' ' || r == ';' }) {
		e.Tags = append(e.Tags, t)
	}
	if core.Schemes[f.scheme] == core.SchemeSSH {
		k := strings.TrimSpace(f.in[fKnock].Value())
		if err := core.CheckKnock(k); err != nil {
			f.err = err.Error()
			return nil
		}
		e.Knock = &k
	}
	if pw := f.in[fPassword].Value(); pw != "" || f.isNew() && f.copyOf == "" {
		e.Password = []byte(pw)
	}
	e.CopyOf = f.copyOf
	if n := f.notes.Value(); f.notesLoaded && n != f.notesOrig {
		e.Notes = &n
	}
	if f.org > 0 && (f.isNew() || f.moving() || len(f.cols) > 0 && !slices.Contains(f.origCols, f.cols[f.col].ID)) {
		// a new item, a move into the organization or another collection
		e.OrgID = f.orgs[f.org-1].ID
		if len(f.cols) > 0 {
			e.CollectionIDs = []string{f.cols[f.col].ID}
		}
	}
	if err := e.Validate(); err != nil {
		f.err = err.Error()
		return nil
	}
	f.in[fPassword].SetValue("")
	f.hidePassword()
	f.busy, f.err, f.frame = true, "", 0
	id, folder, svc, ctx := f.id, strings.TrimSpace(f.in[fFolder].Value()), m.svc, m.ctx
	return tea.Batch(spinTick(), func() tea.Msg {
		c, cancel := context.WithTimeout(ctx, 2*time.Minute)
		defer cancel()
		_, err := svc.SaveHost(c, id, e, folder) // wipes e.Password
		return formSavedMsg{name: e.Name, err: err}
	})
}

// pwSource is the item whose current password [Show] reads: the edited
// item, or the copied one ("" for a new item).
func (f *hostForm) pwSource() string {
	if f.id != "" {
		return f.id
	}
	return f.copyOf
}

// togglePassword shows or hides the password: what is typed, and, while
// nothing is typed, the current password of the item from the vault.
func (f *hostForm) togglePassword(m *Model) tea.Cmd {
	if f.pwShow {
		f.hidePassword()
		return nil
	}
	f.pwShow, f.pwErr = true, ""
	f.in[fPassword].EchoMode = textinput.EchoNormal
	id := f.pwSource()
	if id == "" || f.pwCur != "" {
		return nil
	}
	f.pwReading = true
	svc, ctx := m.svc, m.ctx
	return func() tea.Msg {
		c, cancel := context.WithTimeout(ctx, 30*time.Second)
		defer cancel()
		sec, err := svc.Secret(c, id)
		if err != nil {
			return formPasswordMsg{id: id, err: err}
		}
		text := string(sec.Value) // shown in the form: a string
		sec.Wipe()
		return formPasswordMsg{id: id, text: text}
	}
}

// hidePassword masks the field and forgets the current password read.
func (f *hostForm) hidePassword() {
	f.pwShow, f.pwCur, f.pwErr, f.pwReading = false, "", "", false
	f.in[fPassword].EchoMode = textinput.EchoPassword
}

// currentPassword renders the current password of the item below the
// field: a private key by its first line and the number of lines.
func (f *hostForm) currentPassword(m *Model) string {
	switch {
	case f.pwErr != "":
		return stErr.Render(f.pwErr)
	case f.pwReading:
		return stDim.Render("reading the password" + m.g.ell)
	case f.pwCur == "":
		return ""
	}
	text := strings.ReplaceAll(f.pwCur, "\x1b", "^[")
	if lines := strings.Split(strings.TrimSpace(strings.ReplaceAll(text, "\r", "")), "\n"); len(lines) > 1 || strings.Contains(text, "PRIVATE KEY") {
		return stDim.Render("current: ") + lines[0] + stDim.Render(fmt.Sprintf(" … (a private key, %d lines)", len(lines)))
	}
	return stDim.Render("current: ") + text
}

// mouse: [Show]/[Hide] next to the password.
func (f *hostForm) mouse(m *Model, msg tea.MouseMsg, x, y int) tea.Cmd {
	if f.busy || msg.Action != tea.MouseActionPress || msg.Button != tea.MouseButtonLeft {
		return nil
	}
	if y == f.pwRow && x >= f.pwBtnX && x < f.pwBtnX+8 {
		f.setFocus(fPassword)
		return f.togglePassword(m)
	}
	return nil
}

// busyView shows the progress of saving instead of the fields, like the
// unlock dialog: writing the item, then syncing the vault.
func (f *hostForm) busyView(m *Model, title string, w int) []string {
	frames := spinUnicode
	if m.opt.Icons.ASCII() {
		frames = spinASCII
	}
	phase := "Saving " + f.in[fName].Value() + " to the vault" + m.g.ell
	switch {
	case m.svc.Status().State == core.Syncing:
		phase = "Syncing the vault" + m.g.ell
	case f.moving():
		phase = "Saving and moving to " + f.orgs[f.org-1].Name + m.g.ell
	}
	return m.box(title, "\n"+stKey.Render(frames[f.frame%len(frames)])+" "+phase+"\n", w)
}

func (f *hostForm) view(m *Model, w, h int) []string {
	title := "New item"
	if f.copyOf != "" {
		title = "Copy of an item"
	}
	if !f.isNew() {
		title = "Edit " + f.in[fName].Value()
	}
	if f.busy {
		return f.busyView(m, title, w)
	}
	var b strings.Builder
	l, r := "‹ ", " ›"
	if m.opt.Icons.ASCII() {
		l, r = "< ", " >"
	}
	choice := func(s string, focused bool) string {
		if focused {
			return stKey.Render(l) + stSelected.Render(" "+s+" ") + stKey.Render(r)
		}
		return s
	}
	for _, i := range f.fields() {
		label := fmt.Sprintf("%-13s", fieldLabels[i])
		if i == f.focus {
			label = stKey.Render(label)
		} else {
			label = stDim.Render(label)
		}
		var val string
		switch i {
		case fType:
			val = choice(string(core.Schemes[f.scheme]), i == f.focus)
		case fOrg:
			name := "personal (no organization)"
			if f.org > 0 {
				name = f.orgs[f.org-1].Name
			}
			val = choice(name, i == f.focus)
		case fCollection:
			name := "(the organization has no collections)"
			if len(f.cols) > 0 {
				name = strings.Join(f.cols[f.col].Path, "/")
			}
			val = choice(name, i == f.focus)
		case fNotes:
			f.notes.SetWidth(max(20, min(60, w-24)))
			switch {
			case f.notesErr != "":
				val = stDim.Render(f.notesErr)
			case !f.notesLoaded:
				val = stDim.Render("reading the notes" + m.g.ell)
			default:
				// the textarea lines after the first are indented under the value
				lines := strings.Split(f.notes.View(), "\n")
				val = strings.Join(lines, "\n"+strings.Repeat(" ", 14))
			}
		case fPassword:
			f.in[i].Width = max(20, min(52, w-32))
			btn := "[Show]"
			if f.pwShow {
				btn = "[Hide]"
			}
			f.pwRow = boxPadY + boxTitleGap + strings.Count(b.String(), "\n")
			f.pwBtnX = boxPadX + 14 + f.in[i].Width + 3
			val = padRight(f.in[i].View(), f.in[i].Width+2) + " " + stButton.Render(btn)
			if cur := f.currentPassword(m); cur != "" {
				val += "\n" + strings.Repeat(" ", 14) + cur
			}
		default:
			f.in[i].Width = max(20, min(60, w-24))
			val = f.in[i].View()
		}
		b.WriteString(label + " " + val + "\n")
	}
	switch {
	case !f.isNew() && f.origOrg > 0:
		b.WriteString(stDim.Render(fmt.Sprintf("%-13s %s (Bitwarden cannot move items out of an organization)", "Organization", f.orgs[f.org-1].Name)) + "\n")
	case f.moving():
		b.WriteString(stWarn.Render(fmt.Sprintf("Saving moves the item into %s; Bitwarden cannot move it back.", f.orgs[f.org-1].Name)) + "\n")
	}
	b.WriteString("\n")
	switch {
	case f.err != "":
		b.WriteString(stErr.Render(f.err))
	case f.focus == fNotes:
		b.WriteString(stDim.Render("Notes: Enter new line · Tab: next field · Ctrl+S: save · Esc: cancel"))
	case f.focus == fPassword:
		b.WriteString(stDim.Render("Ctrl+R or [Show]: show / hide the password · Tab: next field · Enter/Ctrl+S: save · Esc: cancel"))
	default:
		b.WriteString(stDim.Render("Tab/Up/Down: field · Left/Right: choose · Enter/Ctrl+S: save · Esc: cancel"))
	}
	return m.box(title, b.String(), w)
}
