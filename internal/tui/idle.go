// SPDX-License-Identifier: MIT

package tui

import (
	"time"

	tea "github.com/charmbracelet/bubbletea"
)

// An idle vaultcrab must not wake up: there is no periodic tick. The screen is
// redrawn after events (input, session output, vault and widget changes),
// and the one timer of the model (m.wake, waited for together with the
// events in listen) is set only for the next moment the screen changes by
// itself (a message or a mark expiring).

// minWake is the shortest delay of a scheduled tick.
const minWake = time.Second

// tickMsg redraws the screen at a scheduled moment.
type tickMsg struct{}

// deadlines are the moments at which the screen changes by itself.
func (m *Model) deadlines() []time.Time {
	var out []time.Time
	if m.toast != "" {
		out = append(out, m.toastUntil)
	}
	if m.osc != "" {
		out = append(out, m.oscUntil)
	}
	if m.pwCopiedHost != "" {
		out = append(out, m.pwCopiedAt.Add(pwCopiedShown))
	}
	return out
}

// scheduleWake sets the timer to the earliest future deadline unless it is
// already set no later than that.
func (m *Model) scheduleWake() {
	now := time.Now()
	var at time.Time
	for _, d := range m.deadlines() {
		if d.After(now) && (at.IsZero() || d.Before(at)) {
			at = d
		}
	}
	if at.IsZero() || m.wakeAt.After(now) && !m.wakeAt.After(at) {
		return
	}
	if at.Before(now.Add(minWake)) {
		at = now.Add(minWake)
	}
	m.wakeAt = at
	m.wake.Reset(at.Sub(now))
}

// focus records whether the terminal window has the focus (terminals that
// report it): widgets pause while it does not.
func (m *Model) focus(msg tea.Msg) {
	_, m.blurred = msg.(tea.BlurMsg)
	debugf("focus %v", !m.blurred)
	// the config file is checked only while the window has the focus (and
	// at once when it comes back: it may have been edited meanwhile)
	m.svc.SetConfigWatch(!m.blurred)
}
