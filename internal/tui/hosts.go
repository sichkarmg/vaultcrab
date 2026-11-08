// SPDX-License-Identifier: MIT

package tui

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"

	"github.com/sichkarmg/vaultcrab/internal/core"
)

// hostList is the host browser (tab 0).
type hostList struct {
	m *Model

	filterIdx  int
	search     textinput.Model
	searching  bool
	query      core.Rule
	queryErr   string
	org        string // core.OrgAll / core.OrgPersonal / org ID
	collection string
	grouping   core.Grouping
	collapsed  map[string]bool

	rows     []core.Row
	cursor   int
	offset   int
	selected string // key of the selected row, kept across rebuilds

	listTop int // y of the first list row within the body (for mouse)
	listH   int
	filterX [][2]int         // x ranges of filter tabs
	selX    [selCount][2]int // x ranges of the selectors, the search and its buttons (second header row)
	widgets bool             // widget column toggled on (w)
	leftW   int              // width of the list part (the widget column is right of it)

	cardBtns    [][2]int // columns of [edit] [copy] in the first row of the details panel
	pwButtonRow int      // row of the "Copy password" button in the details panel (-1 = none)
	notesMaxTop int      // the last first line of the notes that scrolling reaches
	pwButtonX   int      // x of the details panel
	cardX       int      // x of the border left of the details panel (0 = no panel)
}

const headerLines = 3 // filter tabs, selectors, separator

func (l *hostList) init(m *Model) {
	l.m = m
	l.widgets = m.opt.Widgets
	l.collapsed = map[string]bool{}
	l.search = textinput.New()
	l.search.Prompt = "/"
	l.search.Placeholder = "search: words or rules (tag:linux, folder:Lab/**, scheme:ssh)"
	if !m.opt.ShowTree {
		l.grouping = core.Flat
	}
	l.rebuild()
}

func (l *hostList) options() core.ListOptions {
	o := core.ListOptions{
		Query:      l.query,
		Org:        l.org,
		Collection: l.collection,
		Grouping:   l.grouping,
		Collapsed:  l.collapsed,
	}
	if fs := l.m.svc.Filters(); l.filterIdx < len(fs) {
		o.Filter = fs[l.filterIdx].Rule
	}
	return o
}

func (l *hostList) rebuild() {
	l.rows = l.m.svc.Rows(l.options())
	found := false
	for i, r := range l.rows {
		if r.Key == l.selected {
			l.cursor, found = i, true
			break
		}
	}
	if !found {
		// The selection disappeared (filter, search): prefer the first host
		// over a group header.
		l.cursor = 0
		for i, r := range l.rows {
			if r.Kind == core.HostRow {
				l.cursor = i
				break
			}
		}
	}
	l.clamp()
}

func (l *hostList) clamp() {
	if l.cursor >= len(l.rows) {
		l.cursor = len(l.rows) - 1
	}
	if l.cursor < 0 {
		l.cursor = 0
	}
	if len(l.rows) > 0 {
		l.selected = l.rows[l.cursor].Key
	}
	if l.listH > 0 {
		if l.cursor < l.offset {
			l.offset = l.cursor
		}
		if l.cursor >= l.offset+l.listH {
			l.offset = l.cursor - l.listH + 1
		}
	}
	l.offset = max(0, min(l.offset, len(l.rows)-l.listH))
}

func (l *hostList) move(d int) {
	l.cursor += d
	l.clamp()
}

func (l *hostList) current() *core.Row {
	if l.cursor < 0 || l.cursor >= len(l.rows) {
		return nil
	}
	return &l.rows[l.cursor]
}

func (l *hostList) setCollapsed(key string, v bool) {
	if v {
		l.collapsed[key] = true
	} else {
		delete(l.collapsed, key)
	}
	l.rebuild()
}

// activate opens the selected host or toggles the selected group.
func (l *hostList) activate() tea.Cmd {
	r := l.current()
	if r == nil {
		return nil
	}
	if r.Kind == core.GroupRow {
		l.setCollapsed(r.Key, !r.Collapsed)
		return nil
	}
	return l.m.activateHost(*r.Host)
}

func (l *hostList) key(msg tea.KeyMsg) tea.Cmd {
	if l.searching {
		switch msg.String() {
		case "enter":
			l.searching = false
			l.search.Blur()
			return nil
		case "esc":
			// leave the field, keep the search: the list keys work again;
			// a second Esc clears it, / edits it again
			l.searching = false
			l.search.Blur()
			return nil
		case "up", "down", "pgup", "pgdown":
			// fall through to list navigation
		default:
			var cmd tea.Cmd
			l.search, cmd = l.search.Update(msg)
			l.applyQuery()
			return cmd
		}
	}
	m := l.m
	switch msg.String() {
	case "up", "k":
		l.move(-1)
	case "down", "j":
		l.move(1)
	case "pgup":
		l.move(-max(1, l.listH-1))
	case "pgdown":
		l.move(max(1, l.listH-1))
	case "home":
		l.move(-len(l.rows))
	case "end":
		l.move(len(l.rows))
	case "enter":
		return l.activate()
	case "|", "-":
		// Open in a new pane of the last used session tab.
		r := l.current()
		if r == nil || r.Kind != core.HostRow {
			return nil
		}
		t := m.lastTab()
		if t == nil {
			return m.openHost(*r.Host)
		}
		dir := core.SplitRight
		if msg.String() == "-" {
			dir = core.SplitDown
		}
		return m.splitWith(t, *r.Host, dir)
	case "right", "l", " ":
		if r := l.current(); r != nil && r.Kind == core.GroupRow {
			if msg.String() == " " {
				l.setCollapsed(r.Key, !r.Collapsed)
			} else if r.Collapsed {
				l.setCollapsed(r.Key, false)
			} else {
				l.move(1)
			}
		}
	case "left", "h":
		if r := l.current(); r != nil {
			if r.Kind == core.GroupRow && !r.Collapsed {
				l.setCollapsed(r.Key, true)
			} else {
				l.toParent()
			}
		}
	case "tab":
		l.cycleFilter(1)
	case "shift+tab":
		l.cycleFilter(-1)
	case "/":
		return l.startSearch()
	case "esc":
		l.clearSearch()
	case "F":
		l.resetFilters()
	case "o":
		l.cycleOrg()
	case "c":
		l.cycleCollection()
	case "g":
		l.grouping = l.grouping.Next()
		l.rebuild()
	case "s":
		if st := m.svc.Status(); st.State == core.Offline || st.SyncErr != nil {
			m.notify(false, "checking the vault again…")
		} else {
			m.notify(false, "syncing vault…")
		}
		m.svc.Sync(m.ctx) // syncs vaults that are up, retries unreachable ones
	case "u":
		if _, ok := m.svc.LockedVault(); ok {
			m.modal = newUnlock(m)
		} else {
			m.notify(false, "vault is %s", m.svc.Status().State)
		}
	case "L":
		return m.login()
	case "r":
		m.reloadConfig()
	case "t":
		return m.openHost(m.svc.LocalHost())
	case "p":
		if r := l.current(); r != nil && r.Kind == core.HostRow {
			return m.copyPassword(*r.Host)
		}
	case "x":
		if r := l.current(); r != nil && r.Kind == core.HostRow {
			m.modal = newScriptMenu(m, *r.Host)
		}
	case "e", "a", "C", "D":
		return l.editAction(msg.String())
	case "?":
		m.modal = newHelp(m)
	case ",":
		m.modal = newSettings(m)
	case "w":
		if len(m.svc.Widgets()) == 0 {
			m.notify(false, "no widgets: add widgets.items to the config")
		}
		l.widgets = !l.widgets
	case "q", "ctrl+c":
		return m.confirmQuit()
	}
	return nil
}

// editAction opens the form to edit (e), add (a) or copy (C) an item, or
// the confirmation to delete it (D); the vault must be online.
func (l *hostList) editAction(key string) tea.Cmd {
	m := l.m
	if err := m.svc.CanEdit(); err != nil {
		m.notify(true, "%v", err)
		return nil
	}
	r := l.current()
	isHost := r != nil && r.Kind == core.HostRow
	switch {
	case key == "e" && isHost:
		m.modal = newHostForm(m, r.Host, "")
		return m.modal.(*hostForm).loadNotes(m)
	case key == "a":
		m.modal = newHostForm(m, nil, l.currentFolder())
	case key == "C" && isHost:
		m.modal = newCopyForm(m, r.Host)
		return m.modal.(*hostForm).loadNotes(m)
	case key == "D" && isHost:
		m.modal = newDelete(*r.Host)
	}
	return nil
}

// currentFolder returns the folder path of the selected host or folder
// group (prefilled for a new item).
func (l *hostList) currentFolder() string {
	r := l.current()
	if r == nil {
		return ""
	}
	snap := l.m.svc.Snapshot()
	if r.Kind == core.HostRow {
		return strings.Join(snap.FolderPath(r.Host), "/")
	}
	// folder group key: "g:folders:<path>" ("\x00" = items without a folder)
	if p, ok := strings.CutPrefix(r.Key, "g:folders:"); ok && p != "\x00" {
		return p
	}
	return ""
}

func (l *hostList) toParent() {
	r := l.current()
	if r == nil || r.Depth == 0 {
		return
	}
	for i := l.cursor - 1; i >= 0; i-- {
		if l.rows[i].Kind == core.GroupRow && l.rows[i].Depth < r.Depth {
			l.cursor = i
			l.clamp()
			return
		}
	}
}

func (l *hostList) applyQuery() {
	q := strings.TrimSpace(l.search.Value())
	rule, err := core.ParseRule(q)
	if err != nil {
		l.queryErr = err.Error()
		rule, _ = core.ParseRule(strings.Trim(strings.ReplaceAll(q, `"`, ""), " "))
	} else {
		l.queryErr = ""
	}
	l.query = rule
	l.rebuild()
}

func (l *hostList) cycleFilter(d int) {
	n := len(l.m.svc.Filters())
	if n == 0 {
		return
	}
	l.filterIdx = (l.filterIdx + d + n) % n
	l.rebuild()
}

// orgChoices returns [all, personal, org IDs…].
func (l *hostList) orgChoices() []string {
	snap := l.m.svc.Snapshot()
	out := []string{core.OrgAll, core.OrgPersonal}
	orgs := append([]core.Org(nil), snap.Orgs...)
	sort.Slice(orgs, func(i, j int) bool { return strings.ToLower(orgs[i].Name) < strings.ToLower(orgs[j].Name) })
	for _, o := range orgs {
		out = append(out, o.ID)
	}
	return out
}

func (l *hostList) cycleOrg() {
	ch := l.orgChoices()
	i := 0
	for j, c := range ch {
		if c == l.org {
			i = j
		}
	}
	l.org = ch[(i+1)%len(ch)]
	l.collection = ""
	l.rebuild()
}

func (l *hostList) collectionChoices() []string {
	snap := l.m.svc.Snapshot()
	out := []string{""}
	for _, c := range snap.Collections {
		if l.org == core.OrgAll || c.OrgID == l.org {
			out = append(out, c.ID)
		}
	}
	return out
}

func (l *hostList) cycleCollection() {
	if l.org == core.OrgPersonal {
		l.m.notify(false, "personal items have no collections")
		return
	}
	ch := l.collectionChoices()
	i := 0
	for j, c := range ch {
		if c == l.collection {
			i = j
		}
	}
	l.collection = ch[(i+1)%len(ch)]
	l.rebuild()
}

func (l *hostList) orgName() string {
	switch l.org {
	case core.OrgAll:
		return "all"
	case core.OrgPersonal:
		return "personal"
	}
	snap := l.m.svc.Snapshot()
	if o := snap.Org(l.org); o != nil {
		return o.Name
	}
	return l.org
}

func (l *hostList) collectionName() string {
	if l.collection == "" {
		return "all"
	}
	snap := l.m.svc.Snapshot()
	if c := snap.Collection(l.collection); c != nil {
		return strings.Join(c.Path, "/")
	}
	return l.collection
}

// mouse handles a click/wheel in the body at body-relative (x, y).
func (l *hostList) mouse(msg tea.MouseMsg, x, y int) tea.Cmd {
	if l.leftW > 0 && x >= l.leftW {
		return nil // widget column
	}
	if l.cardX > 0 && x >= l.cardX && y >= l.listTop {
		return l.cardMouse(msg, x, y) // the card is not part of the list
	}
	return l.listMouse(msg, x, y)
}

// cardMouse handles the mouse over the details panel: its buttons, and the
// wheel scrolls the notes.
func (l *hostList) cardMouse(msg tea.MouseMsg, x, y int) tea.Cmd {
	switch msg.Button {
	case tea.MouseButtonWheelUp:
		l.m.notes.top = max(0, l.m.notes.top-3)
		return nil
	case tea.MouseButtonWheelDown:
		l.m.notes.top = min(l.m.notes.top+3, l.notesMaxTop)
		return nil
	}
	if msg.Action == tea.MouseActionPress && msg.Button == tea.MouseButtonLeft && y == l.listTop {
		switch spanAt(l.cardBtns, x-l.pwButtonX) {
		case 0:
			return l.editAction("e")
		case 1:
			return l.editAction("C")
		}
	}
	if l.pwButtonRow >= 0 && msg.Action == tea.MouseActionPress && msg.Button == tea.MouseButtonLeft &&
		y == l.listTop+l.pwButtonRow && x > l.pwButtonX && x <= l.pwButtonX+ansi.StringWidth(copyPasswordLabel) {
		if r := l.current(); r != nil && r.Kind == core.HostRow {
			return l.m.copyPassword(*r.Host)
		}
	}
	return nil
}

// listMouse handles the mouse over the filter tabs, selectors and the list.
func (l *hostList) listMouse(msg tea.MouseMsg, x, y int) tea.Cmd {
	switch msg.Button {
	case tea.MouseButtonWheelUp:
		l.offset = max(0, l.offset-3)
		l.cursor = max(min(l.cursor, l.offset+l.listH-1), l.offset)
		l.clamp()
		return nil
	case tea.MouseButtonWheelDown:
		l.offset = max(0, min(l.offset+3, len(l.rows)-l.listH))
		l.cursor = max(min(l.cursor, l.offset+l.listH-1), l.offset)
		l.clamp()
		return nil
	}
	if msg.Action != tea.MouseActionPress || msg.Button != tea.MouseButtonLeft {
		return nil
	}
	if y == 0 {
		for i, r := range l.filterX {
			if x >= r[0] && x < r[1] {
				l.filterIdx = i
				l.rebuild()
			}
		}
		return nil
	}
	if y == 1 {
		return l.selectorClick(x)
	}
	i := l.offset + y - l.listTop
	if y < l.listTop || i >= len(l.rows) {
		return nil
	}
	l.cursor = i
	l.clamp()
	key := l.rows[i].Key
	now := time.Now()
	double := key == l.m.lastClickKey && now.Sub(l.m.lastClick) < 400*time.Millisecond
	l.m.lastClick, l.m.lastClickKey = now, key
	r := l.rows[i]
	if r.Kind == core.GroupRow {
		indent := 2 * r.Depth
		if double || x < indent+2 {
			l.m.lastClickKey = ""
			l.setCollapsed(r.Key, !r.Collapsed)
		}
		return nil
	}
	if double {
		l.m.lastClickKey = ""
		return l.activate()
	}
	return nil
}

func (l *hostList) status() string {
	r := l.current()
	if r == nil {
		return "no hosts"
	}
	if r.Kind == core.GroupRow {
		return fmt.Sprintf("%s · %d host(s) · Enter/Space fold · ? help", r.Label, r.Count)
	}
	if l.searching {
		return r.Host.Target() + " · typing the search · Up/Down move · Enter/Esc leave the field (the search stays)"
	}
	return r.Host.Target() + " · Enter connect · x scripts · e edit · a add · C copy · D delete · / search · t local shell · ? help"
}

// schemeIcon returns the type icon of h, padded to the widest type icon so
// that the names line up (an icon may be two cells wide, e.g. ">_").
func (l *hostList) schemeIcon(h *core.Host) string {
	ic := l.m.opt.Icons.Get(string(h.Scheme)) // icon names equal scheme names
	if ic == "" {
		ic = l.m.opt.Icons.Get(core.IconSSH)
	}
	w := 0
	for _, sc := range core.Schemes {
		w = max(w, ansi.StringWidth(l.m.opt.Icons.Get(string(sc))))
	}
	return padRight(ic, w)
}

// sessionsOf returns the open sessions for a host.
func (l *hostList) sessionsOf(id string) []*core.Session {
	var out []*core.Session
	for _, s := range l.m.svc.Sessions() {
		if s.Host.ID == id {
			out = append(out, s)
		}
	}
	return out
}

func (l *hostList) view(w, h int) []string {
	if l.widgetsShown(w) {
		ww := l.m.opt.WidgetsWidth
		l.leftW = w - ww - 1
		left := l.listView(l.leftW, h)
		right := l.widgetLines(ww, h)
		for i := range left {
			left[i] = padRight(left[i], w-ww-1) + stBorder.Render(l.m.g.v) + right[i]
		}
		return left
	}
	l.leftW = w
	return l.listView(w, h)
}

// listView renders the filters, selectors, the list and the details.
func (l *hostList) listView(w, h int) []string {
	m := l.m
	ic := m.opt.Icons
	lines := make([]string, 0, h)

	// 1. filter tabs
	var fb strings.Builder
	l.filterX = l.filterX[:0]
	x := 0
	for i, f := range m.svc.Filters() {
		label := f.Name
		if icon := filterIcon(ic, &f); icon != "" && icon != f.Name {
			label = icon + " " + f.Name
		}
		label = fmt.Sprintf(" %s %d ", label, m.svc.CountMatching(f.Rule, core.ListOptions{Query: l.query, Org: l.org, Collection: l.collection}))
		lw := ansi.StringWidth(label)
		l.filterX = append(l.filterX, [2]int{x, x + lw})
		if i == l.filterIdx {
			fb.WriteString(stTabActive.Render(label))
		} else {
			fb.WriteString(label)
		}
		fb.WriteString(" ")
		x += lw + 1
	}
	lines = append(lines, fb.String())

	// 2. selectors | search | reset
	lines = append(lines, l.selectorRow())
	lines = append(lines, stBorder.Render(strings.Repeat(m.g.h, w)))

	// 3. list (+ details on wide screens)
	l.listTop = headerLines
	l.listH = max(1, h-headerLines)
	l.clamp()
	listW := w
	detailsW := 0
	if w >= 100 {
		detailsW = min(48, w/3)
		if m.opt.ListWidth > 0 && m.opt.ListWidth < w-20 {
			detailsW = w - m.opt.ListWidth - 1
		}
		listW = w - detailsW - 1
	}
	var details []string
	l.pwButtonRow, l.cardBtns, l.cardX = -1, nil, 0
	if detailsW > 0 {
		l.pwButtonX, l.cardX = listW+1, listW
		details = l.details(detailsW, l.listH)
	}
	snap := m.svc.Snapshot()
	var empty []string
	if len(l.rows) == 0 {
		msg := "no hosts"
		switch st := m.svc.Status(); {
		case len(snap.Hosts) == 0 && st.State == core.Locked:
			msg = "vault is locked — press u to unlock"
		case len(snap.Hosts) == 0 && st.State == core.NeedLogin:
			msg = "bw is not logged in — press L to run `bw login`"
		case len(snap.Hosts) == 0 && (st.State == core.Starting || st.State == core.Syncing):
			msg = "loading…"
		case len(snap.Hosts) == 0 && st.State == core.Crashed:
			msg = "bw serve stopped, restarting it…"
		case len(snap.Hosts) == 0 && st.State == core.Offline:
			msg = "offline and no cache: " + errText(st.Err)
		case len(snap.Hosts) > 0:
			msg = "no hosts match the filter"
		}
		empty = []string{"", "  " + stDim.Render(msg)}
	}
	for i := 0; i < l.listH; i++ {
		var row string
		if idx := l.offset + i; idx < len(l.rows) {
			row = l.renderRow(&l.rows[idx], listW, idx == l.cursor)
		} else if i < len(empty) {
			row = empty[i]
		}
		row = padRight(row, listW)
		if detailsW > 0 {
			d := ""
			if i < len(details) {
				d = details[i]
			}
			row += stBorder.Render(m.g.v) + padRight(d, detailsW)
		}
		lines = append(lines, row)
	}
	return lines
}

func errText(err error) string {
	if err == nil {
		return "unknown error"
	}
	return err.Error()
}

func trunc(s string, n int) string {
	r := []rune(s)
	if len(r) > n {
		return string(r[:n-1]) + "~"
	}
	return s
}

func (l *hostList) renderRow(r *core.Row, w int, selected bool) string {
	ic := l.m.opt.Icons
	indent := strings.Repeat("  ", r.Depth)
	if r.Kind == core.GroupRow {
		fold := ic.Get(core.IconFolderOpen)
		if r.Collapsed {
			fold = ic.Get(core.IconFolder)
		}
		label := fmt.Sprintf("%s%s %s (%d)", indent, fold, r.Label, r.Count)
		if selected {
			return stSelected.Render(padRight(label, w))
		}
		if r.IsOrg {
			return stOrg.Render(ic.Get(core.IconOrg)+" ") + stOrg.Render(strings.TrimPrefix(label, indent))
		}
		return stGroup.Render(label)
	}
	h := r.Host
	state := " "
	if ss := l.sessionsOf(h.ID); len(ss) > 0 {
		state = l.m.sessionIcon(ss[len(ss)-1])
	}
	fav := ""
	if h.Favorite {
		fav = " " + ic.Get(core.IconFav)
	}
	var badges []string
	for _, f := range r.Badges {
		if b := filterIcon(ic, f); b != "" {
			badges = append(badges, b)
		}
	}
	left := fmt.Sprintf("%s%s %s%s", indent, l.schemeIcon(h), h.Name, fav)
	right := h.Target()
	if len(badges) > 0 {
		right = strings.Join(badges, " ") + "  " + right
	}
	room := w - 2 - ansi.StringWidth(left) - 1
	if room < 8 {
		right = ""
	} else if ansi.StringWidth(right) > room {
		right = ansi.Truncate(right, room, l.m.g.ell)
	}
	gap := max(1, w-2-ansi.StringWidth(left)-ansi.StringWidth(right))
	if selected {
		return state + " " + stSelected.Render(left+strings.Repeat(" ", gap)+right)
	}
	return state + " " + left + strings.Repeat(" ", gap) + stDim.Render(right)
}

// details renders the side panel for the selected row.
func (l *hostList) details(w, height int) []string {
	r := l.current()
	if r == nil || r.Kind != core.HostRow {
		return nil
	}
	h := r.Host
	snap := l.m.svc.Snapshot()
	var out []string
	add := func(k, v string) {
		if v == "" {
			return
		}
		prefix := " " + stDim.Render(fmt.Sprintf("%-11s", k)) + " "
		out = append(out, prefix+ansi.Truncate(v, max(1, w-13), l.m.g.ell))
	}
	// the name, and opposite it the buttons [edit] [copy]
	const btns = "[edit] [copy]"
	bx := w - 1 - len(btns)
	l.cardBtns = nil
	if bx > 8 {
		name := padRight(" "+stBold.Render(ansi.Truncate(h.Name, bx-2, l.m.g.ell)), bx)
		out = append(out, name+stKey.Render("[edit]")+" "+stKey.Render("[copy]"))
		l.cardBtns = [][2]int{{bx, bx + 6}, {bx + 7, bx + 13}}
	} else {
		out = append(out, " "+stBold.Render(ansi.Truncate(h.Name, w-2, l.m.g.ell)))
	}
	out = append(out, "")
	if vs := l.m.svc.Vaults(); len(vs) > 1 {
		v := l.m.svc.VaultOf(h.ID)
		add("vault", v.Label()+" · "+v.Where())
	}
	add("type", string(h.Scheme))
	add("user", h.User)
	add("address", h.Addr)
	add("port", fmt.Sprint(h.Port))
	add("path", h.Path)
	add("folder", strings.Join(snap.FolderPath(h), "/"))
	if o := snap.Org(h.OrgID); o != nil {
		add("org", o.Name)
	}
	var cols []string
	for _, id := range h.CollectionIDs {
		if c := snap.Collection(id); c != nil {
			cols = append(cols, strings.Join(c.Path, "/"))
		}
	}
	add("collections", strings.Join(cols, ", "))
	add("tags", strings.Join(h.Tags, ", "))
	if scs := l.m.svc.Scripts(*h); len(scs) > 0 {
		def, hasDef := l.m.svc.DefaultScript(*h)
		var names []string
		for _, sc := range scs {
			n := sc.Name
			if hasDef && sc.Name == def.Name {
				n += " (Enter)"
			}
			names = append(names, n)
		}
		add("scripts [x]", strings.Join(names, ", "))
	}
	keys := make([]string, 0, len(h.Fields))
	for k := range h.Fields {
		if k != "tags" && k != "tag" && k != "port" {
			keys = append(keys, k)
		}
	}
	sort.Strings(keys)
	for _, k := range keys {
		add(k, h.Fields[k])
	}
	const dateFmt = "02.01.2006 15:04"
	if !h.Created.IsZero() {
		add("created", h.Created.Local().Format(dateFmt))
	}
	if !h.Revised.IsZero() {
		add("modified", h.Revised.Local().Format(dateFmt))
	}
	if ss := l.sessionsOf(h.ID); len(ss) > 0 {
		out = append(out, "")
		for _, s := range ss {
			st, err := s.State()
			line := l.m.sessionIcon(s) + " " + st.String()
			if err != nil {
				line += ": " + err.Error()
			}
			out = append(out, " "+ansi.Truncate(line, w-2, l.m.g.ell))
		}
	}
	out = append(out, "", " "+stDim.Render("password/key is read from the vault"), " "+stDim.Render("only when connecting"), "")
	l.pwButtonRow = len(out) // row of the button within the details panel
	out = append(out, l.m.passwordButton(h.ID)...)
	// the notes last: read with a delay, they must not move the button
	notes, maxTop := l.m.notesLines(h, w, height-len(out))
	l.notesMaxTop = maxTop
	return append(out, notes...)
}

// filterIcon returns the icon of a filter tab or badge; the built-in type
// filters use the type's icon from the icon set.
func filterIcon(ic core.IconSet, f *core.Filter) string {
	if f.IconName != "" {
		return ic.Get(f.IconName)
	}
	return ic.Pick(f.Icon, f.ASCIIIcon)
}
