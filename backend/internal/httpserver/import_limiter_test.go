package httpserver

import (
	"testing"
	"time"
)

type importLimiterClockStub struct{ now time.Time }

func (clock *importLimiterClockStub) Now() time.Time { return clock.now }

func testImportLimiter(t *testing.T, clock *importLimiterClockStub) *ImportLimiter {
	t.Helper()
	limiter, err := NewImportLimiter(ImportLimiterConfig{
		Window: 15 * time.Minute, PreviewAttempts: 5,
		ConfirmAttempts: 3, MaximumTrackedKeys: 8,
	}, clock)
	if err != nil {
		t.Fatalf("new limiter: %v", err)
	}
	return limiter
}

func TestImportLimiterAttemptBoundariesAndWindow(t *testing.T) {
	clock := &importLimiterClockStub{now: time.Unix(100, 0)}
	limiter := testImportLimiter(t, clock)
	for attempt := 0; attempt < 5; attempt++ {
		lease, err := limiter.acquirePreview("actor", "household")
		if err != nil {
			t.Fatalf("preview attempt %d: %v", attempt+1, err)
		}
		lease.release(false)
	}
	if _, err := limiter.acquirePreview("actor", "household"); err != ErrImportLimited {
		t.Fatalf("sixth preview error=%v", err)
	}
	clock.now = clock.now.Add(15 * time.Minute)
	lease, err := limiter.acquirePreview("actor", "household")
	if err != nil {
		t.Fatalf("attempt at exact new window: %v", err)
	}
	lease.release(false)

	for attempt := 0; attempt < 3; attempt++ {
		lease, err = limiter.acquireConfirm("other", "household")
		if err != nil {
			t.Fatalf("confirm attempt %d: %v", attempt+1, err)
		}
		lease.release(false)
	}
	if _, err := limiter.acquireConfirm("other", "household"); err != ErrImportLimited {
		t.Fatalf("fourth confirm error=%v", err)
	}
}

func TestImportLimiterConcurrencyIsolationReleaseAndReplayRefund(t *testing.T) {
	clock := &importLimiterClockStub{now: time.Unix(200, 0)}
	limiter := testImportLimiter(t, clock)

	preview, err := limiter.acquirePreview("actor-a", "household-a")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := limiter.acquireConfirm("actor-a", "household-b"); err != ErrImportLimited {
		t.Fatalf("same actor concurrent error=%v", err)
	}
	other, err := limiter.acquirePreview("actor-b", "household-b")
	if err != nil {
		t.Fatalf("isolated actor/household rejected: %v", err)
	}
	other.release(false)
	preview.release(false)
	preview.release(false)

	confirm, err := limiter.acquireConfirm("actor-a", "household-a")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := limiter.acquireConfirm("actor-b", "household-a"); err != ErrImportLimited {
		t.Fatalf("same household concurrent confirm error=%v", err)
	}
	confirm.release(true)
	for attempt := 0; attempt < 4; attempt++ {
		lease, err := limiter.acquireConfirm("actor-a", "household-a")
		if err != nil {
			t.Fatalf("replay-refunded confirm %d: %v", attempt+1, err)
		}
		lease.release(true)
	}
}

func TestImportLimiterBoundedCleanupAndInvalidConfig(t *testing.T) {
	clock := &importLimiterClockStub{now: time.Unix(300, 0)}
	limiter, err := NewImportLimiter(ImportLimiterConfig{
		Window: time.Minute, PreviewAttempts: 1, ConfirmAttempts: 1, MaximumTrackedKeys: 1,
	}, clock)
	if err != nil {
		t.Fatal(err)
	}
	lease, _ := limiter.acquirePreview("a", "h1")
	lease.release(false)
	if _, err := limiter.acquirePreview("b", "h2"); err != ErrImportLimited {
		t.Fatalf("bounded map error=%v", err)
	}
	clock.now = clock.now.Add(time.Minute)
	lease, err = limiter.acquirePreview("b", "h2")
	if err != nil {
		t.Fatalf("stale key was not cleaned: %v", err)
	}
	lease.release(false)
	if len(limiter.entries) != 1 {
		t.Fatalf("tracked entries=%d", len(limiter.entries))
	}

	invalid := []ImportLimiterConfig{{}, {Window: time.Minute, PreviewAttempts: 0, ConfirmAttempts: 1, MaximumTrackedKeys: 1}}
	for _, config := range invalid {
		if _, err := NewImportLimiter(config, clock); err == nil {
			t.Fatal("invalid limiter config accepted")
		}
	}
	if _, err := NewImportLimiter(ImportLimiterConfig{Window: time.Minute, PreviewAttempts: 1, ConfirmAttempts: 1, MaximumTrackedKeys: 1}, nil); err == nil {
		t.Fatal("nil clock accepted")
	}
}
