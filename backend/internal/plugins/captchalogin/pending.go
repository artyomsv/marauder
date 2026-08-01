// Package captchalogin provides a reusable, human-in-the-loop interactive
// login flow for trackers that gate authentication behind an image
// captcha. A tracker supplies a Config; the Engine handles the stateful
// begin -> (refresh)* -> complete dance and harvests session cookies.
package captchalogin

import (
	"crypto/rand"
	"encoding/hex"
	"sync"
	"time"

	"github.com/artyomsv/marauder/backend/internal/domain"
	"github.com/artyomsv/marauder/backend/internal/plugins/trackers/forumcommon"
)

const pendingTTL = 5 * time.Minute

type pending struct {
	sess  *forumcommon.Session
	creds *domain.TrackerCredential
	// spec carries the per-challenge captcha details (image URL, hidden
	// fields, answer field name) so Complete can submit an answer that the
	// tracker still associates with the picture the user was shown.
	spec    ChallengeSpec
	expires time.Time
}

type pendingStore struct {
	mu  sync.Mutex
	m   map[string]*pending
	now func() time.Time // injectable for tests
}

func newPendingStore() *pendingStore {
	return &pendingStore{m: map[string]*pending{}, now: time.Now}
}

func (p *pendingStore) put(sess *forumcommon.Session, creds *domain.TrackerCredential, spec ChallengeSpec) (string, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	id := hex.EncodeToString(b)
	p.mu.Lock()
	defer p.mu.Unlock()
	p.evictLocked()
	p.m[id] = &pending{sess: sess, creds: creds, spec: spec, expires: p.now().Add(pendingTTL)}
	return id, nil
}

// updateSpec replaces the stored challenge details after a Refresh minted a
// new captcha, so Complete submits the cap_sid that matches the picture the
// user is actually looking at.
func (p *pendingStore) updateSpec(id string, spec ChallengeSpec) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if e, ok := p.m[id]; ok {
		e.spec = spec
	}
}

func (p *pendingStore) get(id string) (*pending, bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.evictLocked()
	e, ok := p.m[id]
	return e, ok
}

func (p *pendingStore) del(id string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	delete(p.m, id)
}

func (p *pendingStore) evictLocked() {
	now := p.now()
	for k, v := range p.m {
		if now.After(v.expires) {
			delete(p.m, k)
		}
	}
}
