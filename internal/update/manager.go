package update

import (
	"context"
	"sync"
	"time"
)

// Manager checks for updates now and then, installs them when asked (or automatically),
// and tells the app when a restart will finish the job.
type Manager struct {
	Version string      // the running version
	Auto    func() bool // whether automatic updates are on right now
	// Restart stops Letta and relaunches the new version. reopen: the person pressed
	// Update on the settings page, so the new version should open a fresh page (the old
	// one's link stops working; each run gets a new private link).
	Restart func(reopen bool) error
	Every   time.Duration // how often to check (default 12h)
	First   time.Duration // wait before the first check (default 30s, so Letta connects first)
	// OnChange is called when the status changes (e.g. to update the tray menu).
	OnChange func(Status)

	// seams for tests
	latest  func(context.Context) (Release, error)
	install func(context.Context, Release, func(done, total int64)) error

	mu     sync.Mutex
	status Status
}

// Status is what the page and tray show.
type Status struct {
	Running   string    `json:"running"`
	Available string    `json:"available"` // newer version on GitHub, "" if none
	Notes     string    `json:"notes"`
	URL       string    `json:"url"`
	Phase     string    `json:"phase"` // "", "checking", "downloading", "restarting", "failed"
	Error     string    `json:"error"`
	Done      int64     `json:"done"`
	Total     int64     `json:"total"`
	Checked   time.Time `json:"checked"`
}

func (m *Manager) Status() Status {
	m.mu.Lock()
	defer m.mu.Unlock()
	s := m.status
	s.Running = m.Version
	return s
}

func (m *Manager) set(f func(*Status)) {
	m.mu.Lock()
	f(&m.status)
	s := m.status
	s.Running = m.Version
	cb := m.OnChange
	m.mu.Unlock()
	if cb != nil {
		cb(s)
	}
}

// Run checks shortly after start and then every m.Every, until ctx ends.
func (m *Manager) Run(ctx context.Context) {
	every := m.Every
	if every <= 0 {
		every = 12 * time.Hour
	}
	first := m.First
	if first <= 0 {
		first = 30 * time.Second
	}
	t := time.NewTimer(first)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
		if m.Check(ctx) && m.Auto != nil && m.Auto() {
			_ = m.Apply(ctx)
		}
		t.Reset(every)
	}
}

// Check asks GitHub and reports whether a newer version is available.
func (m *Manager) Check(ctx context.Context) bool {
	if m.Version == "" || m.Version == "dev" {
		return false // a build from source doesn't update itself
	}
	latest := m.latest
	if latest == nil {
		latest = Latest
	}
	m.set(func(s *Status) {
		if s.Phase == "" || s.Phase == "failed" {
			s.Phase = "checking"
		}
	})
	rel, err := latest(ctx)
	if err != nil {
		// not finding out isn't an error worth showing; try again next time
		m.set(func(s *Status) {
			if s.Phase == "checking" {
				s.Phase = ""
			}
			s.Checked = time.Now()
		})
		return false
	}
	newer := Newer(m.Version, rel.Tag)
	m.set(func(s *Status) {
		if s.Phase == "checking" {
			s.Phase = ""
		}
		s.Checked = time.Now()
		s.Available, s.Notes, s.URL = "", "", ""
		if newer {
			s.Available, s.Notes, s.URL = rel.Tag, rel.Notes, rel.URL
		}
	})
	return newer
}

// Apply downloads and installs the newest version, then restarts into it quietly.
// Only one runs at a time; extra calls while one is going are ignored.
func (m *Manager) Apply(ctx context.Context) error { return m.apply(ctx, false) }

// ApplyFromPage is Apply when the person pressed Update on the settings page.
func (m *Manager) ApplyFromPage(ctx context.Context) error { return m.apply(ctx, true) }

func (m *Manager) apply(ctx context.Context, reopen bool) error {
	m.mu.Lock()
	if m.status.Phase == "downloading" || m.status.Phase == "restarting" {
		m.mu.Unlock()
		return nil
	}
	m.status.Phase, m.status.Error, m.status.Done, m.status.Total = "downloading", "", 0, 0
	m.mu.Unlock()
	m.set(func(*Status) {}) // announce it (tray: "Updating Letta Tether…")

	latest, install := m.latest, m.install
	if latest == nil {
		latest = Latest
	}
	if install == nil {
		install = func(ctx context.Context, rel Release, p func(int64, int64)) error {
			t, err := CurrentTarget()
			if err != nil {
				return err
			}
			return Install(ctx, rel, t, p)
		}
	}
	fail := func(err error) error {
		m.set(func(s *Status) { s.Phase, s.Error = "failed", err.Error() })
		return err
	}
	rel, err := latest(ctx)
	if err != nil {
		return fail(err)
	}
	if !Newer(m.Version, rel.Tag) {
		m.set(func(s *Status) { s.Phase, s.Available = "", "" })
		return nil
	}
	err = install(ctx, rel, func(done, total int64) {
		m.set(func(s *Status) { s.Done, s.Total = done, total })
	})
	if err != nil {
		return fail(err)
	}
	m.set(func(s *Status) { s.Phase = "restarting" })
	if m.Restart != nil {
		if err := m.Restart(reopen); err != nil {
			return fail(err)
		}
	}
	return nil
}
