package trackercreds

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"sync"

	"github.com/google/uuid"

	"github.com/artyomsv/marauder/backend/internal/domain"
	"github.com/artyomsv/marauder/backend/internal/plugins/registry"
)

// sessionSlot serialises session work for one (tracker, user) and remembers
// which credential the plugin's session was last established with.
type sessionSlot struct {
	// lock is a one-slot semaphore rather than a sync.Mutex so a waiter can
	// give up on its own context.
	lock chan struct{}
	// owner is the fingerprint of the credential whose Login last succeeded;
	// zero until one has. Guarded by lock.
	owner [sha256.Size]byte
}

// slots holds one sessionSlot per (tracker, user), process-wide: scheduler
// workers and the four request-path callers of Warm all publish into the same
// per-user plugin session, so they must all take the same lock. Bounded by
// users × credentialed trackers.
var slots sync.Map

type slotKey struct {
	tracker string
	user    uuid.UUID
}

func slotFor(tracker string, user uuid.UUID) *sessionSlot {
	s, _ := slots.LoadOrStore(slotKey{tracker, user}, &sessionSlot{lock: make(chan struct{}, 1)})
	return s.(*sessionSlot)
}

// EnsureSession makes creds' tracker session usable: Verify first, Login only
// on a miss, with at most one Verify or Login in flight per (tracker, user).
//
// Verify-first because a login is the expensive, rate-limited request: the
// forum plugins keep one session per user in memory and share it across every
// topic, so a live session needs one GET, not a password POST. Serialised
// because Verify-first alone does not stop a burst — after a restart or a
// session expiry, every worker holding one of that user's topics sees the same
// cold session at once. Issue #198 measured the scheduler's old
// Login-every-check sending four Tapochek logins inside 20 ms, all answered
// 503. Now the first caller logs in and the rest queue, then find the session
// live with one Verify each.
//
// Each caller works on its own context, and a waiter gives up on its own
// deadline. There is deliberately no shared result: callers carry budgets from
// 5s (AddTopic preview) to 35s (a scheduler check), and a shared attempt would
// fail the long ones on the short one's deadline.
//
// Verify is trusted only for the credential that established the session. The
// plugins key the session by Marauder user, not tracker account, and Verify
// asks "is this session signed in", not "as whom" — so after a credential edit
// the previous account's session still verifies. Any change to the username,
// password or stored session cookie, and a cold slot after a restart, goes
// straight to Login. That also covers an edit racing a login: the lock orders
// the two, and the newer credential cannot pass Verify on the older one's
// session.
//
// An errored Verify still falls through to Login: it says nothing about the
// password, and ErrVerifyUnsupported plugins (AniDub, for a page it cannot
// classify) would otherwise never log in. Login's error is returned unchanged,
// so the scheduler still sees ErrSessionExpired.
func EnsureSession(ctx context.Context, tr registry.Tracker, creds *domain.TrackerCredential) error {
	wc, ok := tr.(registry.WithCredentials)
	if !ok || creds == nil {
		return nil
	}
	slot := slotFor(tr.Name(), creds.UserID)
	select {
	case slot.lock <- struct{}{}:
	case <-ctx.Done():
		return ctx.Err()
	}
	defer func() { <-slot.lock }()

	fp := fingerprint(creds)
	if slot.owner == fp {
		if live, err := wc.Verify(ctx, creds); err == nil && live {
			return nil
		}
	}
	if err := wc.Login(ctx, creds); err != nil {
		// Whatever session the plugin holds now, this credential did not
		// establish it.
		slot.owner = [sha256.Size]byte{}
		return err
	}
	slot.owner = fp
	return nil
}

// fingerprint identifies the account a credential signs in as. Hashed so the
// slot never holds the password itself.
func fingerprint(c *domain.TrackerCredential) [sha256.Size]byte {
	var buf []byte
	for _, part := range [][]byte{[]byte(c.Username), c.SecretEnc, c.SessionEnc} {
		// Length-prefixed so ("ab","c") and ("a","bc") differ.
		buf = binary.AppendUvarint(buf, uint64(len(part)))
		buf = append(buf, part...)
	}
	return sha256.Sum256(buf)
}
