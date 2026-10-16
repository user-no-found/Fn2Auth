package instance

import (
	"context"
	"errors"
	"path/filepath"
	"sync"
	"time"

	"fn2auth/internal/config"
	"fn2auth/internal/vault"
	"github.com/user-no-found/FnPackKit/runtime/identity"
)

type entry struct {
	user     identity.User
	ready    chan struct{}
	worker   *Worker
	err      error
	active   int
	last     time.Time
	retiring bool
}

// Pool merges concurrent startups per user and counts starting/stopping workers.
type Pool struct {
	config               config.Config
	version, displayName string
	mu                   sync.Mutex
	entries              map[uint32]*entry
	stopping             bool
	resolve              func(identity.Request) (identity.User, error)
}

// New creates a per-business-user pool under the package OS account.
func New(cfg config.Config, version, displayName string, resolve func(identity.Request) (identity.User, error)) *Pool {
	return &Pool{config: cfg, version: version, displayName: displayName, entries: make(map[uint32]*entry), resolve: resolve}
}

// Resolve uses the server-owned account authority; production supplies identity.Resolve.
func (pool *Pool) Resolve(request identity.Request) (identity.User, error) {
	return pool.resolve(request)
}

// Acquire resolves current identity without holding the pool lock during NSS or startup.
func (pool *Pool) Acquire(ctx context.Context, request identity.Request) (*Worker, func(), error) {
	user, err := pool.Resolve(request)
	if err != nil {
		return nil, nil, errors.New("current NAS identity unavailable")
	}
	parent := filepath.Join(pool.config.StateDir, "users")
	if vault.Revoked(parent, request.UID) {
		return nil, nil, errors.New("vault binding revoked; manual review required")
	}
	pool.mu.Lock()
	if pool.stopping {
		pool.mu.Unlock()
		return nil, nil, errors.New("service is stopping")
	}
	current := pool.entries[user.UID]
	if current != nil && (current.user.Name != user.Name || current.user.Home != user.Home || current.user.GID != user.GID) {
		pool.mu.Unlock()
		vault.Revoke(parent, request.UID)
		return nil, nil, errors.New("NAS identity changed; vault quarantined")
	}
	if current != nil {
		select {
		case <-current.ready:
			if current.active == 0 && !current.retiring && (current.worker == nil || !current.worker.Child.Alive()) {
				if current.worker != nil {
					current.worker.Transport.CloseIdleConnections()
				}
				delete(pool.entries, user.UID)
				current = nil
			}
		default:
		}
	}
	owner := current == nil
	if owner {
		if len(pool.entries) >= pool.config.MaxUsers {
			pool.mu.Unlock()
			return nil, nil, errors.New("active vault limit reached")
		}
		current = &entry{user: user, ready: make(chan struct{}), last: time.Now()}
		pool.entries[user.UID] = current
	}
	pool.mu.Unlock()
	if owner {
		pool.start(ctx, current)
	}
	select {
	case <-ctx.Done():
		return nil, nil, ctx.Err()
	case <-current.ready:
	}
	pool.mu.Lock()
	defer pool.mu.Unlock()
	if pool.stopping || current.retiring {
		return nil, nil, errors.New("instance is stopping")
	}
	if current.err != nil {
		return nil, nil, current.err
	}
	if current.worker == nil || !current.worker.Child.Alive() {
		return nil, nil, errors.New("instance unavailable")
	}
	current.active++
	current.last = time.Now()
	release := func() { pool.mu.Lock(); current.active--; current.last = time.Now(); pool.mu.Unlock() }
	return current.worker, release, nil
}

func (pool *Pool) start(ctx context.Context, current *entry) {
	startup, cancel := context.WithTimeout(ctx, pool.config.StartupTimeout())
	defer cancel()
	state, err := vault.Prepare(filepath.Join(pool.config.StateDir, "users"), current.user)
	var worker *Worker
	if err == nil {
		worker, err = Start(startup, pool.config, state, pool.version, pool.displayName)
	}
	pool.mu.Lock()
	current.worker, current.err = worker, err
	close(current.ready)
	pool.mu.Unlock()
}

// RetireIdle waits for child shutdown outside the map lock; failed stops retain their slots.
func (pool *Pool) RetireIdle() {
	var selected []*entry
	pool.mu.Lock()
	for _, current := range pool.entries {
		select {
		case <-current.ready:
		default:
			continue
		}
		if current.active != 0 || current.retiring {
			continue
		}
		if current.err == nil && time.Since(current.last) < time.Duration(pool.config.IdleSeconds)*time.Second {
			continue
		}
		current.retiring = true
		selected = append(selected, current)
	}
	pool.mu.Unlock()
	for _, current := range selected {
		pool.retire(current)
	}
}

func (pool *Pool) retire(current *entry) error {
	var err error
	if current.worker != nil {
		err = current.worker.Child.Terminate(time.Duration(pool.config.ShutdownSeconds) * time.Second)
		if err == nil {
			current.worker.Transport.CloseIdleConnections()
		}
	}
	pool.mu.Lock()
	defer pool.mu.Unlock()
	if err == nil {
		delete(pool.entries, current.user.UID)
	} else {
		current.err = err
		current.retiring = false
	}
	return err
}

// Close refuses to claim success if active requests or an un-stopped child remain.
func (pool *Pool) Close() error {
	pool.mu.Lock()
	pool.stopping = true
	selected := make([]*entry, 0, len(pool.entries))
	for _, current := range pool.entries {
		selected = append(selected, current)
	}
	pool.mu.Unlock()
	var result error
	for _, current := range selected {
		<-current.ready
		pool.mu.Lock()
		active, busy := current.active, current.retiring
		if active == 0 && !busy {
			current.retiring = true
		}
		pool.mu.Unlock()
		if active != 0 || busy {
			result = errors.New("instance still busy")
			continue
		}
		if err := pool.retire(current); err != nil {
			result = err
		}
	}
	return result
}
