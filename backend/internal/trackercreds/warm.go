// Package trackercreds loads, decrypts and warms a user's stored tracker
// credential so a caller can read a tracker page AS that user.
//
// It exists because a login-gated tracker does not answer an anonymous caller
// with an error — it answers with a stub. Toloka serves a guest a ~7KB page
// with an empty <title> and no torrent block, and `tracker.php` returns zero
// rows with no error at all. So a caller that resolves anonymously does not
// fail; it succeeds at reading nothing, and stores a topic with a placeholder
// name and no poster. The scheduler self-heals a placeholder display name on
// the first check, but nothing backfills an image, so that topic never gets
// one.
//
// Four callers need this and they live in three packages — the tracker search
// handler, the AddTopic preview handler, the POST /topics create path, and the
// Sonarr poller — which is why it is a package rather than a method.
package trackercreds

import (
	"context"

	"github.com/google/uuid"
	"github.com/rs/zerolog/log"

	"github.com/artyomsv/marauder/backend/internal/domain"
	"github.com/artyomsv/marauder/backend/internal/plugins/registry"
)

// Store is the read seam over the credential repository. *repo.TrackerCredentials
// satisfies it.
type Store interface {
	GetForTracker(ctx context.Context, userID uuid.UUID, trackerName string) (*domain.TrackerCredential, error)
}

// Decryptor unseals the stored secret and session blobs. *crypto.MasterKey
// satisfies it.
type Decryptor interface {
	Decrypt(ct, nonce []byte) ([]byte, error)
}

// Warm returns a usable credential for t, or nil to proceed anonymously.
//
// Every failure degrades to nil rather than erroring: none of the callers may
// hard-fail on credential trouble. Search reports
// registry.ErrSearchRequiresCredentials to the user; the preview and both
// create paths simply resolve less metadata.
//
// LoginFailed distinguishes "no credential stored" (false) from "a credential
// exists but could not be warmed" (true), so a caller can say "your login
// failed" instead of telling a user who has an account to go add one. Only
// search surfaces it today; the others treat both alike.
//
// Ordering is Verify-first, Login-on-miss: on a warm in-process session Verify
// is one cheap GET, and only a cold or dead session pays the Login round-trip.
// Deliberately neither the credentials handler's loginAndVerify (Login→Verify
// always, right for validating a freshly entered password, wasteful per call)
// nor the scheduler's Login-only loadCredentials.
func Warm(ctx context.Context, store Store, master Decryptor, userID uuid.UUID, t registry.Tracker) (creds *domain.TrackerCredential, loginFailed bool, loginErr error) {
	wc, needsCreds := t.(registry.WithCredentials)
	if !needsCreds || store == nil || master == nil {
		return nil, false, nil
	}
	stored, err := store.GetForTracker(ctx, userID, t.Name())
	if err != nil || stored == nil {
		return nil, false, nil
	}
	// Decrypt secret + session like the credentials handler's Test does —
	// session-cookie trackers validate the session blob, not the password.
	plain, err := master.Decrypt(stored.SecretEnc, stored.SecretNonce)
	if err != nil {
		log.Warn().Str("tracker", t.Name()).Err(err).
			Msg("tracker credential decrypt failed; continuing anonymously")
		return nil, true, err
	}
	transient := &domain.TrackerCredential{
		ID:          stored.ID,
		UserID:      userID,
		TrackerName: stored.TrackerName,
		Username:    stored.Username,
		SecretEnc:   plain,
	}
	if len(stored.SessionEnc) > 0 {
		sess, derr := master.Decrypt(stored.SessionEnc, stored.SessionNonce)
		if derr != nil {
			log.Warn().Str("tracker", t.Name()).Err(derr).
				Msg("tracker session decrypt failed; continuing anonymously")
			return nil, true, derr
		}
		transient.SessionEnc = sess
	}
	if ok, verr := wc.Verify(ctx, transient); verr == nil && ok {
		return transient, false, nil
	}
	if lerr := wc.Login(ctx, transient); lerr != nil {
		log.Debug().Str("tracker", t.Name()).Err(lerr).
			Msg("tracker credential login failed; continuing anonymously")
		return nil, true, lerr
	}
	return transient, false, nil
}
