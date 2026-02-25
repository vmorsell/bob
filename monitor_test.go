package main

import (
	"runtime"
	"testing"
	"time"
)

// drainHub gives the Hub's background goroutine time to process pending events
// and close file handles before t.TempDir cleanup runs.
func drainHub(t *testing.T) {
	t.Helper()
	t.Cleanup(func() {
		runtime.Gosched()
		time.Sleep(20 * time.Millisecond)
	})
}

func TestHub_ThreadJobMapping(t *testing.T) {
	hub := NewHub(t.TempDir())

	t.Run("register and active", func(t *testing.T) {
		hub.RegisterThreadJob("C1", "ts1", "job-1")
		if got := hub.ActiveJobForThread("C1", "ts1"); got != "job-1" {
			t.Errorf("ActiveJobForThread = %q, want %q", got, "job-1")
		}
	})

	t.Run("no registration returns empty", func(t *testing.T) {
		if got := hub.ActiveJobForThread("C2", "ts2"); got != "" {
			t.Errorf("ActiveJobForThread = %q, want empty", got)
		}
	})

	t.Run("unregister returns empty", func(t *testing.T) {
		hub.UnregisterThreadJob("C1", "ts1")
		if got := hub.ActiveJobForThread("C1", "ts1"); got != "" {
			t.Errorf("ActiveJobForThread after unregister = %q, want empty", got)
		}
	})

	t.Run("multiple threads isolated", func(t *testing.T) {
		hub.RegisterThreadJob("C3", "ts3", "job-3")
		hub.RegisterThreadJob("C4", "ts4", "job-4")
		if got := hub.ActiveJobForThread("C3", "ts3"); got != "job-3" {
			t.Errorf("thread C3 = %q, want job-3", got)
		}
		if got := hub.ActiveJobForThread("C4", "ts4"); got != "job-4" {
			t.Errorf("thread C4 = %q, want job-4", got)
		}
	})

	t.Run("nil hub safe", func(t *testing.T) {
		var h *Hub
		h.RegisterThreadJob("C", "ts", "j")
		h.UnregisterThreadJob("C", "ts")
		if got := h.ActiveJobForThread("C", "ts"); got != "" {
			t.Errorf("nil hub ActiveJobForThread = %q, want empty", got)
		}
	})
}

func TestHub_JobState(t *testing.T) {
	hub := NewHub(t.TempDir())

	t.Run("set and get", func(t *testing.T) {
		state := &JobState{Repo: "my-repo", Phase: PhasePlanning}
		hub.SetJobState("job-1", state)
		got, ok := hub.GetJobState("job-1")
		if !ok {
			t.Fatal("expected ok=true")
		}
		if got.Repo != "my-repo" {
			t.Errorf("Repo = %q, want %q", got.Repo, "my-repo")
		}
	})

	t.Run("unknown returns nil false", func(t *testing.T) {
		got, ok := hub.GetJobState("nonexistent")
		if ok || got != nil {
			t.Errorf("expected nil, false; got %v, %v", got, ok)
		}
	})

	t.Run("nil hub returns nil false", func(t *testing.T) {
		var h *Hub
		got, ok := h.GetJobState("j")
		if ok || got != nil {
			t.Errorf("expected nil, false; got %v, %v", got, ok)
		}
	})
}

func TestHub_SetPhase(t *testing.T) {
	drainHub(t)
	hub := NewHub(t.TempDir())

	t.Run("updates phase", func(t *testing.T) {
		hub.SetJobState("job-1", &JobState{Phase: PhasePlanning})
		hub.SetPhase("job-1", PhaseAwaitingApproval)
		state, _ := hub.GetJobState("job-1")
		if state.Phase != PhaseAwaitingApproval {
			t.Errorf("Phase = %q, want %q", state.Phase, PhaseAwaitingApproval)
		}
	})

	t.Run("unknown job no panic", func(t *testing.T) {
		hub.SetPhase("nonexistent", PhaseDone)
	})
}

func TestHub_TryStartImplementation(t *testing.T) {
	t.Run("AwaitingApproval transitions to Implementing", func(t *testing.T) {
		drainHub(t)
		hub := NewHub(t.TempDir())
		hub.SetJobState("job-1", &JobState{Phase: PhaseAwaitingApproval})
		if !hub.TryStartImplementation("job-1") {
			t.Error("expected true")
		}
		state, _ := hub.GetJobState("job-1")
		if state.Phase != PhaseImplementing {
			t.Errorf("Phase = %q, want %q", state.Phase, PhaseImplementing)
		}
	})

	t.Run("second call returns false", func(t *testing.T) {
		drainHub(t)
		hub := NewHub(t.TempDir())
		hub.SetJobState("job-2", &JobState{Phase: PhaseAwaitingApproval})
		hub.TryStartImplementation("job-2")
		if hub.TryStartImplementation("job-2") {
			t.Error("second call should return false")
		}
	})

	t.Run("Planning returns false", func(t *testing.T) {
		hub := NewHub(t.TempDir())
		hub.SetJobState("job-3", &JobState{Phase: PhasePlanning})
		if hub.TryStartImplementation("job-3") {
			t.Error("Planning phase should return false")
		}
	})

	t.Run("Done returns false", func(t *testing.T) {
		hub := NewHub(t.TempDir())
		hub.SetJobState("job-4", &JobState{Phase: PhaseDone})
		if hub.TryStartImplementation("job-4") {
			t.Error("Done phase should return false")
		}
	})

	t.Run("nil hub returns false", func(t *testing.T) {
		var h *Hub
		if h.TryStartImplementation("j") {
			t.Error("nil hub should return false")
		}
	})
}

func TestHub_ClearImplementation(t *testing.T) {
	t.Run("Implementing reverts to AwaitingApproval", func(t *testing.T) {
		drainHub(t)
		hub := NewHub(t.TempDir())
		hub.SetJobState("job-1", &JobState{Phase: PhaseImplementing})
		hub.ClearImplementation("job-1")
		state, _ := hub.GetJobState("job-1")
		if state.Phase != PhaseAwaitingApproval {
			t.Errorf("Phase = %q, want %q", state.Phase, PhaseAwaitingApproval)
		}
	})

	t.Run("AwaitingApproval unchanged", func(t *testing.T) {
		hub := NewHub(t.TempDir())
		hub.SetJobState("job-2", &JobState{Phase: PhaseAwaitingApproval})
		hub.ClearImplementation("job-2")
		state, _ := hub.GetJobState("job-2")
		if state.Phase != PhaseAwaitingApproval {
			t.Errorf("Phase = %q, want %q", state.Phase, PhaseAwaitingApproval)
		}
	})

	t.Run("nil hub safe", func(t *testing.T) {
		var h *Hub
		h.ClearImplementation("j")
	})
}

func TestHub_ChannelRepos(t *testing.T) {
	t.Run("set and get", func(t *testing.T) {
		hub := NewHub(t.TempDir())
		hub.SetChannelRepo("C1", "my-repo")
		if got := hub.GetChannelRepo("C1"); got != "my-repo" {
			t.Errorf("GetChannelRepo = %q, want %q", got, "my-repo")
		}
	})

	t.Run("no set returns empty", func(t *testing.T) {
		hub := NewHub(t.TempDir())
		if got := hub.GetChannelRepo("C2"); got != "" {
			t.Errorf("GetChannelRepo = %q, want empty", got)
		}
	})

	t.Run("clear removes mapping", func(t *testing.T) {
		hub := NewHub(t.TempDir())
		hub.SetChannelRepo("C3", "repo")
		hub.ClearChannelRepo("C3")
		if got := hub.GetChannelRepo("C3"); got != "" {
			t.Errorf("GetChannelRepo after clear = %q, want empty", got)
		}
	})

	t.Run("persistence across hub instances", func(t *testing.T) {
		dir := t.TempDir()
		hub1 := NewHub(dir)
		hub1.SetChannelRepo("C4", "persisted-repo")

		// SetChannelRepo saves synchronously, but give broadcast goroutine a moment.
		time.Sleep(50 * time.Millisecond)

		hub2 := NewHub(dir)
		if got := hub2.GetChannelRepo("C4"); got != "persisted-repo" {
			t.Errorf("GetChannelRepo on new hub = %q, want %q", got, "persisted-repo")
		}
	})
}
