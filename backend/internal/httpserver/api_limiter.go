package httpserver

import (
	"errors"
	"math"
	"sync"
	"time"
)

const (
	defaultAPIWindow                = time.Minute
	defaultPerimeterAttempts        = 600
	defaultSubjectAttempts          = 300
	defaultPerimeterConcurrency     = 64
	defaultSubjectConcurrency       = 16
	defaultMaximumTrackedSubjects   = 8192
	minimumRateLimitRetryAfter      = time.Second
	maximumRateLimitWindow          = 15 * time.Minute
	maximumRateLimitTrackedSubjects = 65536
)

var ErrAPILimited = errors.New("api rate limited")

type APILimiterConfig struct {
	Window               time.Duration
	PerimeterAttempts    int
	SubjectAttempts      int
	PerimeterConcurrency int
	SubjectConcurrency   int
	MaximumSubjects      int
}

type APILimiterClock interface {
	Now() time.Time
}

type apiSubjectLimit struct {
	windowStartedAt time.Time
	attempts        int
	active          int
}

type APILimiter struct {
	mu                sync.Mutex
	clock             APILimiterClock
	config            APILimiterConfig
	perimeterWindow   time.Time
	perimeterAttempts int
	perimeterActive   int
	subjects          map[string]*apiSubjectLimit
}

type apiLimitLease struct {
	once      sync.Once
	limiter   *APILimiter
	subject   string
	perimeter bool
}

type systemAPILimiterClock struct{}

func (systemAPILimiterClock) Now() time.Time { return time.Now() }

func NewAPILimiter(config APILimiterConfig, clock APILimiterClock) (*APILimiter, error) {
	if clock == nil || config.Window <= 0 || config.Window > maximumRateLimitWindow ||
		config.PerimeterAttempts <= 0 || config.SubjectAttempts <= 0 ||
		config.PerimeterConcurrency <= 0 || config.SubjectConcurrency <= 0 ||
		config.MaximumSubjects <= 0 || config.MaximumSubjects > maximumRateLimitTrackedSubjects {
		return nil, errors.New("invalid API limiter configuration")
	}
	return &APILimiter{
		clock:    clock,
		config:   config,
		subjects: make(map[string]*apiSubjectLimit),
	}, nil
}

func newDefaultAPILimiter() *APILimiter {
	limiter, _ := NewAPILimiter(APILimiterConfig{
		Window:            defaultAPIWindow,
		PerimeterAttempts: defaultPerimeterAttempts, SubjectAttempts: defaultSubjectAttempts,
		PerimeterConcurrency: defaultPerimeterConcurrency, SubjectConcurrency: defaultSubjectConcurrency,
		MaximumSubjects: defaultMaximumTrackedSubjects,
	}, systemAPILimiterClock{})
	return limiter
}

func (limiter *APILimiter) acquirePerimeter() (*apiLimitLease, time.Duration, error) {
	if limiter == nil {
		return nil, minimumRateLimitRetryAfter, ErrAPILimited
	}
	limiter.mu.Lock()
	defer limiter.mu.Unlock()

	now := limiter.clock.Now()
	if limiter.perimeterWindow.IsZero() || !now.Before(limiter.perimeterWindow.Add(limiter.config.Window)) {
		limiter.perimeterWindow = now
		limiter.perimeterAttempts = 0
	}
	if limiter.perimeterAttempts >= limiter.config.PerimeterAttempts ||
		limiter.perimeterActive >= limiter.config.PerimeterConcurrency {
		return nil, limiter.retryAfter(now, limiter.perimeterWindow), ErrAPILimited
	}
	limiter.perimeterAttempts++
	limiter.perimeterActive++
	return &apiLimitLease{limiter: limiter, perimeter: true}, 0, nil
}

func (limiter *APILimiter) acquireSubject(subject string) (*apiLimitLease, time.Duration, error) {
	if limiter == nil || subject == "" {
		return nil, minimumRateLimitRetryAfter, ErrAPILimited
	}
	limiter.mu.Lock()
	defer limiter.mu.Unlock()

	now := limiter.clock.Now()
	limiter.cleanupSubjectsLocked(now)
	entry := limiter.subjects[subject]
	if entry == nil {
		if len(limiter.subjects) >= limiter.config.MaximumSubjects {
			return nil, limiter.config.Window, ErrAPILimited
		}
		entry = &apiSubjectLimit{windowStartedAt: now}
		limiter.subjects[subject] = entry
	}
	if !now.Before(entry.windowStartedAt.Add(limiter.config.Window)) {
		entry.windowStartedAt = now
		entry.attempts = 0
	}
	if entry.attempts >= limiter.config.SubjectAttempts || entry.active >= limiter.config.SubjectConcurrency {
		return nil, limiter.retryAfter(now, entry.windowStartedAt), ErrAPILimited
	}
	entry.attempts++
	entry.active++
	return &apiLimitLease{limiter: limiter, subject: subject}, 0, nil
}

func (lease *apiLimitLease) release() {
	if lease == nil || lease.limiter == nil {
		return
	}
	lease.once.Do(func() {
		limiter := lease.limiter
		limiter.mu.Lock()
		defer limiter.mu.Unlock()
		if lease.perimeter {
			if limiter.perimeterActive > 0 {
				limiter.perimeterActive--
			}
			return
		}
		if entry := limiter.subjects[lease.subject]; entry != nil && entry.active > 0 {
			entry.active--
		}
	})
}

func (limiter *APILimiter) cleanupSubjectsLocked(now time.Time) {
	for subject, entry := range limiter.subjects {
		if entry.active == 0 && !now.Before(entry.windowStartedAt.Add(limiter.config.Window)) {
			delete(limiter.subjects, subject)
		}
	}
}

func (limiter *APILimiter) retryAfter(now, windowStartedAt time.Time) time.Duration {
	if windowStartedAt.IsZero() {
		return minimumRateLimitRetryAfter
	}
	retry := windowStartedAt.Add(limiter.config.Window).Sub(now)
	if retry < minimumRateLimitRetryAfter {
		return minimumRateLimitRetryAfter
	}
	return time.Duration(math.Ceil(retry.Seconds())) * time.Second
}
