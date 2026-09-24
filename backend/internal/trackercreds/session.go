package trackercreds

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/artyomsv/marauder/backend/internal/domain"
	"github.com/artyomsv/marauder/backend/internal/plugins/registry"
)

// sessionSlot serialises session work for one (tracker, user) and remembers
// who established the plugin's session and how the last attempt went.
type sessionSlot struct {
	// lock is a one-slot semaphore rather than a sync.Mutex so a waiter can
	// give up on its own context.
	lock chan struct{}

	// Guarded by lock.
	//
	// owner is the fingerprint of the credential whose sign-in last
	// succeeded; zero until one has.
	owner [sha256.Size]byte
	// last is the most recent attempt that ran to an answer — not one cut
	// short by its own caller's context.
	last outcome
}

type outcome struct {
	fp  [sha256.Size]byte
	at  time.Time
	err error // nil: the session was confirmed live for fp
}

// slots holds one sessionSlot per (tracker, user), process-wide: scheduler
// workers, the four request-path callers of Warm and the credentials handler
// all publish into the same per-user plugin session, so they must all take the
// same lock. Bounded by users × credentialed trackers.
var slots sync.Map

type slotKey struct {
	tracker string
	user    uuid.UUID
}

func slotFor(tracker string, user uuid.UUID) *sessionSlot {
	s, _ := slots.LoadOrStore(slotKey{tracker, user}, &sessionSlot{lock: make(chan struct{}, 1)})
	return s.(*sessionSlot)
}

func (s *sessionSlot) acquire(ctx context.Context) error {
	select {
	case s.lock <- struct{}{}:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (s *sessionSlot) release() { <-s.lock }

// settle records how an attempt for fp ended. The caller holds the lock.
func (s *sessionSlot) settle(ctx context.Context, fp [sha256.Size]byte, err error) {
	if err == nil {
		s.owner = fp
	} else {
		// Whatever session the plugin holds now, this credential did not
		// establish it.
		s.owner = [sha256.Size]byte{}
	}
	if ctx.Err() != nil {
		// Ended by the caller's own deadline, not by the tracker: nobody
		// else may inherit it (callers' budgets run from 5s to 35s).
		s.last = outcome{}
		return
	}
	s.last = outcome{fp: fp, at: time.Now(), err: err}
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
// 503.
//
// A caller that queued behind an attempt for the same credential takes that
// attempt's answer, success or refusal, when it finished after the caller
// arrived: it is as fresh as a request of its own would be, and without it N
// topics due together would pay N round-trips in a row on budgets that are
// already running. An attempt ended by its own caller's context is never
// handed on, and each caller waits and works on its own context — there is
// deliberately no shared deadline.
//
// Verify is trusted only for the credential that established the session. The
// plugins key the session by Marauder user, not tracker account, and Verify
// asks "is this session signed in", not "as whom" — so after a credential edit
// the previous account's session still verifies. Any change to the username,
// password or stored session cookie, and a slot nobody has signed in through
// since the process started, goes straight to Login. The credentials handler
// signs in through Establish so its sessions count.
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
	fp := fingerprint(creds)
	arrived := time.Now()
	if err := slot.acquire(ctx); err != nil {
		return err
	}
	defer slot.release()

	if slot.last.fp == fp && slot.last.at.After(arrived) {
		return slot.last.err
	}
	if slot.owner == fp {
		if live, err := wc.Verify(ctx, creds); err == nil && live {
			slot.settle(ctx, fp, nil)
			return nil
		}
	}
	err := wc.Login(ctx, creds)
	slot.settle(ctx, fp, err)
	return err
}

// Establish runs login — a caller's own sign-in sequence, such as the
// credentials handler's Login-then-Verify — under the session's lock, and
// records creds as the session's owner if it succeeds. It always runs login:
// its callers are validating a credential, so a recent answer is not enough.
func Establish(ctx context.Context, tr registry.Tracker, creds *domain.TrackerCredential, login func() error) error {
	slot := slotFor(tr.Name(), creds.UserID)
	fp := fingerprint(creds)
	if err := slot.acquire(ctx); err != nil {
		return err
	}
	defer slot.release()
	err := login()
	slot.settle(ctx, fp, err)
	return err
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
