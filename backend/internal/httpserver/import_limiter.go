package httpserver

import (
	"errors"
	"sync"
	"time"
)

const (
	defaultImportWindow      = 15 * time.Minute
	defaultPreviewAttempts   = 5
	defaultConfirmAttempts   = 3
	defaultImportLimiterKeys = 4096
)

var ErrImportLimited = errors.New("backup import rate limited")

type ImportLimiterClock interface {
	Now() time.Time
}

type ImportLimiterConfig struct {
	Window             time.Duration
	PreviewAttempts    int
	ConfirmAttempts    int
	MaximumTrackedKeys int
}

type importLimitKey struct {
	actor       string
	householdID string
}

type importLimitEntry struct {
	windowStartedAt time.Time
	previewAttempts int
	confirmAttempts int
}

type ImportLimiter struct {
	mu             sync.Mutex
	clock          ImportLimiterClock
	config         ImportLimiterConfig
	entries        map[importLimitKey]*importLimitEntry
	activeActors   map[string]struct{}
	activeConfirms map[string]struct{}
}

type importLease struct {
	once    sync.Once
	limiter *ImportLimiter
	key     importLimitKey
	confirm bool
}

type systemImportLimiterClock struct{}

func (systemImportLimiterClock) Now() time.Time { return time.Now() }

func NewImportLimiter(config ImportLimiterConfig, clock ImportLimiterClock) (*ImportLimiter, error) {
	if clock == nil || config.Window <= 0 || config.PreviewAttempts <= 0 ||
		config.ConfirmAttempts <= 0 || config.MaximumTrackedKeys <= 0 {
		return nil, errors.New("invalid import limiter configuration")
	}
	return &ImportLimiter{
		clock:          clock,
		config:         config,
		entries:        make(map[importLimitKey]*importLimitEntry),
		activeActors:   make(map[string]struct{}),
		activeConfirms: make(map[string]struct{}),
	}, nil
}

func newDefaultImportLimiter() *ImportLimiter {
	limiter, _ := NewImportLimiter(ImportLimiterConfig{
		Window: defaultImportWindow, PreviewAttempts: defaultPreviewAttempts,
		ConfirmAttempts: defaultConfirmAttempts, MaximumTrackedKeys: defaultImportLimiterKeys,
	}, systemImportLimiterClock{})
	return limiter
}

func (limiter *ImportLimiter) acquirePreview(actor, householdID string) (*importLease, error) {
	return limiter.acquire(actor, householdID, false)
}

func (limiter *ImportLimiter) acquireConfirm(actor, householdID string) (*importLease, error) {
	return limiter.acquire(actor, householdID, true)
}

func (limiter *ImportLimiter) acquire(actor, householdID string, confirm bool) (*importLease, error) {
	if limiter == nil {
		return nil, ErrImportLimited
	}
	limiter.mu.Lock()
	defer limiter.mu.Unlock()

	now := limiter.clock.Now()
	limiter.cleanupLocked(now)
	if _, busy := limiter.activeActors[actor]; busy {
		return nil, ErrImportLimited
	}
	if confirm {
		if _, busy := limiter.activeConfirms[householdID]; busy {
			return nil, ErrImportLimited
		}
	}

	key := importLimitKey{actor: actor, householdID: householdID}
	entry := limiter.entries[key]
	if entry == nil {
		if len(limiter.entries) >= limiter.config.MaximumTrackedKeys {
			return nil, ErrImportLimited
		}
		entry = &importLimitEntry{windowStartedAt: now}
		limiter.entries[key] = entry
	}
	if !now.Before(entry.windowStartedAt.Add(limiter.config.Window)) {
		entry.windowStartedAt = now
		entry.previewAttempts = 0
		entry.confirmAttempts = 0
	}
	if confirm {
		if entry.confirmAttempts >= limiter.config.ConfirmAttempts {
			return nil, ErrImportLimited
		}
		entry.confirmAttempts++
		limiter.activeConfirms[householdID] = struct{}{}
	} else {
		if entry.previewAttempts >= limiter.config.PreviewAttempts {
			return nil, ErrImportLimited
		}
		entry.previewAttempts++
	}
	limiter.activeActors[actor] = struct{}{}
	return &importLease{limiter: limiter, key: key, confirm: confirm}, nil
}

// release always frees concurrency slots. A confirmed replay refunds the
// reserved first-confirm attempt because it did not perform a new import.
func (lease *importLease) release(replayed bool) {
	if lease == nil || lease.limiter == nil {
		return
	}
	lease.once.Do(func() {
		limiter := lease.limiter
		limiter.mu.Lock()
		defer limiter.mu.Unlock()
		delete(limiter.activeActors, lease.key.actor)
		if lease.confirm {
			delete(limiter.activeConfirms, lease.key.householdID)
			if replayed {
				if entry := limiter.entries[lease.key]; entry != nil && entry.confirmAttempts > 0 {
					entry.confirmAttempts--
				}
			}
		}
	})
}

func (limiter *ImportLimiter) cleanupLocked(now time.Time) {
	for key, entry := range limiter.entries {
		if now.Before(entry.windowStartedAt.Add(limiter.config.Window)) {
			continue
		}
		if _, active := limiter.activeActors[key.actor]; active {
			continue
		}
		if _, active := limiter.activeConfirms[key.householdID]; active {
			continue
		}
		delete(limiter.entries, key)
	}
}
