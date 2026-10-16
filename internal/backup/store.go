// Package backup encrypts portable OTP exports and binds single-use import previews.
package backup

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"sync"
	"time"
)

const maxBytes = 8 * 1024 * 1024
const lifetime = 5 * time.Minute

type preview struct {
	uid     uint32
	session [32]byte
	expires time.Time
	data    []byte
}

// Manager bounds scrypt work and owns short-lived decrypted preview data.
type Manager struct {
	mu       sync.Mutex
	entries  map[[32]byte]preview
	capacity int
	work     chan struct{}
	done     chan struct{}
	once     sync.Once
}

// New permits at most four previews per active user and one expensive crypto operation.
func New(maxUsers int) *Manager {
	manager := &Manager{entries: make(map[[32]byte]preview), capacity: maxUsers * 4, work: make(chan struct{}, 1), done: make(chan struct{})}
	go func() {
		ticker := time.NewTicker(time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-manager.done:
				return
			case <-ticker.C:
				manager.mu.Lock()
				manager.expire()
				manager.mu.Unlock()
			}
		}
	}()
	return manager
}

func (manager *Manager) expire() {
	for key, value := range manager.entries {
		if !time.Now().Before(value.expires) {
			clear(value.data)
			delete(manager.entries, key)
		}
	}
}

func (manager *Manager) issue(uid uint32, session [32]byte, data []byte) (string, error) {
	var random [32]byte
	if _, err := rand.Read(random[:]); err != nil {
		return "", err
	}
	token := base64.RawURLEncoding.EncodeToString(random[:])
	key := sha256.Sum256([]byte(token))
	manager.mu.Lock()
	defer manager.mu.Unlock()
	manager.expire()
	count := 0
	for _, entry := range manager.entries {
		if entry.uid == uid {
			count++
		}
	}
	if count >= 4 || len(manager.entries) >= manager.capacity {
		return "", errors.New("preview capacity reached")
	}
	manager.entries[key] = preview{uid: uid, session: session, expires: time.Now().Add(lifetime), data: data}
	return token, nil
}

func (manager *Manager) consume(uid uint32, session [32]byte, token string) ([]byte, error) {
	if len(token) != 43 {
		return nil, errors.New("invalid preview")
	}
	manager.mu.Lock()
	defer manager.mu.Unlock()
	manager.expire()
	key := sha256.Sum256([]byte(token))
	entry, ok := manager.entries[key]
	if !ok || entry.uid != uid || entry.session != session {
		return nil, errors.New("preview expired or belongs to another session")
	}
	delete(manager.entries, key)
	return entry.data, nil
}

// Close discards outstanding decrypted previews at gateway shutdown.
func (manager *Manager) Close() {
	manager.once.Do(func() { close(manager.done) })
	manager.mu.Lock()
	defer manager.mu.Unlock()
	for key, entry := range manager.entries {
		clear(entry.data)
		delete(manager.entries, key)
	}
}
