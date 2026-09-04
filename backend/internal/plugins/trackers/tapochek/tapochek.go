// Package tapochek implements a tracker plugin for tapochek.net.
//
// Tapochek is a Russian-language phpBB-derived tracker. Everything about a
// release that Marauder needs is behind a login, so an account is required.
//
// # What the plugin had to be rebuilt around (measured 2026-09-04)
//
// Tapochek publishes NO infohash and NO magnet — not on the guest page, and
// not to a signed-in member either (zero 40-hex runs on five live release
// pages). The original plugin looked for an English `Info hash` label
// followed by 40 hex characters, which exists nowhere on this site, so every
// check it ever ran failed with "no infohash found". Exactly the anidub and
// toloka problem, and the fix is the same: derive a change token from the
// release's torrent block instead.
//
// The token digests the download id, the .torrent filename, the release size
// and the registration timestamp. Download counts and seeder/leecher counts
// are deliberately excluded — they drift on their own and would make every
// check look like a new release.
//
// # Login gating
//
// A guest sees a topic's title and description but the download box is
// replaced with a link to the registration page; there is no size, no
// seeders, no registration date and no torrent table at all. Some forums are
// guest-readable and most are not — a gated topic answers 302 to
// `login.php?redirect=...`. `tracker.php` and `search.php` are gated too.
//
// So a missing torrent block is far more often a lost session than a changed
// page, which is why Check and Download route it through gateError rather
// than reporting a parse failure. `download.php` answers a session it does
// not accept with **200 and an HTML login page**, so Download must check the
// bytes are bencoded or it would hand that page to a torrent client.
//
// # Session state
//
// The server keeps a `bb_data` cookie holding a PHP-serialised array:
//
//	a:3:{s:2:"uk";N;s:3:"uid";i:<account id>;s:3:"sid";s:20:"<session>";}
//
// `uid` is the signed-in marker, and a guest is issued **no bb_data at all**,
// so its absence is as meaningful as a non-positive id. That single
// server-supplied signal drives both Login and Verify. It replaced a check
// for `logout.php?sid=` in the page, a string this site never emits — so
// Verify reported every live session as dead — paired with a Login that
// inspected neither status nor body and so reported a rejected password as a
// success. Both signals were broken at once, in opposite directions.
//
// A successful login answers **302 with an empty body**; a rejected one
// answers 200 with "Вы ввели неверное имя пользователя или неверный пароль."
//
// # Encoding
//
// The site serves `windows-1251`. Titles happen to arrive as HTML numeric
// entities (`&#1086;`) rather than raw cp1251 bytes, but the rest of the page
// does not, so cleanTitle decodes cp1251 before unescaping entities — an
// undecoded cp1251 title is invalid UTF-8 that Postgres rejects outright
// (SQLSTATE 22021).
//
// **Validation status:** verified end-to-end against the live site with a
// real account on 2026-09-04 — login, rejection of a wrong password, session
// verification, change detection, metadata, and a real `.torrent` download.
// See tapochek_live_test.go (build tag `live`).
package tapochek

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"html"
	"io"
	"net/http"
	"net/url"
	"path"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"github.com/artyomsv/marauder/backend/internal/domain"
	"github.com/artyomsv/marauder/backend/internal/plugins/registry"
	"github.com/artyomsv/marauder/backend/internal/plugins/trackers/forumcommon"
)

const (
	pluginName    = "tapochek"
	displayName   = "Tapochek.net"
	defaultDomain = "tapochek.net"
	userAgent     = "Marauder/0.4 (+https://marauder.cc)"

	// sessionCookie carries the account id the server has assigned this jar.
	sessionCookie = "bb_data"

	// maxBodyBytes caps a response. A release .torrent on this site is tens
	// of KB; 8MB is generous and stops a hostile or broken response from
	// being read into memory unbounded.
	maxBodyBytes = 8 << 20

	// maxRedirects bounds the redirect chain. Login depends on following one
	// hop, so the chain cannot simply be refused. Every hop is re-checked
	// against the same host allowlist, so this is a cost bound, not a
	// security one.
	maxRedirects = 5
)

// urlPattern is host-agnostic; CanParse gates the captured host against the
// known + admin-configured domain allowlist (the SSRF barrier — see
// registry.DomainAllowed).
var urlPattern = regexp.MustCompile(`^https?://(?:www\.)?([^/]+)/viewtopic\.php\?t=(\d+)`)

var knownDomains = []string{defaultDomain}

type plugin struct {
	sessions  *forumcommon.SessionStore
	domain    string
	transport http.RoundTripper
}

func init() {
	registry.RegisterTracker(&plugin{sessions: forumcommon.New(), domain: defaultDomain})
}

func (p *plugin) Name() string        { return pluginName }
func (p *plugin) DisplayName() string { return displayName }

var _ registry.WithDomains = (*plugin)(nil)

// Domains implements registry.WithDomains; first entry is canonical.
func (p *plugin) Domains() []string { return knownDomains }

// effectiveDomain resolves the domain every request is built against: a
// test-injected p.domain wins, then the admin-configured active domain, then
// the compiled default.
func (p *plugin) effectiveDomain() string {
	if p.domain != "" && p.domain != defaultDomain {
		return p.domain
	}
	if active := registry.ActiveDomain(pluginName); active != "" {
		return active
	}
	if p.domain != "" {
		return p.domain
	}
	return defaultDomain
}

func (p *plugin) baseURL() string { return "https://" + p.effectiveDomain() }

// canonicalURL rebuilds a stored topic URL against the active domain and
// forces https, so a topic added over http cannot put the session cookie on
// the wire in plaintext.
func (p *plugin) canonicalURL(rawURL string) (string, error) {
	m := urlPattern.FindStringSubmatch(strings.TrimSpace(rawURL))
	if m == nil {
		return "", fmt.Errorf("tapochek: unrecognised topic URL %q", rawURL)
	}
	return fmt.Sprintf("%s/viewtopic.php?t=%s", p.baseURL(), m[2]), nil
}

func (p *plugin) CanParse(rawURL string) bool {
	m := urlPattern.FindStringSubmatch(strings.TrimSpace(rawURL))
	return m != nil && registry.DomainAllowed(pluginName, m[1], knownDomains)
}

func (p *plugin) Parse(_ context.Context, rawURL string) (*domain.Topic, error) {
	m := urlPattern.FindStringSubmatch(strings.TrimSpace(rawURL))
	if m == nil {
		return nil, fmt.Errorf("tapochek: unrecognised topic URL %q", rawURL)
	}
	if !registry.DomainAllowed(pluginName, m[1], knownDomains) {
		return nil, fmt.Errorf("tapochek: host %q is not an allowed domain", m[1])
	}
	// topic_id stays an int: that is what this plugin has always stored, and
	// changing the type would silently reshape the Extra blob of every topic
	// added before this rewrite.
	//
	// The error is handled rather than discarded: the capture is digits only,
	// so the only reachable failure is overflow — and Atoi returns a CLAMPED
	// value alongside it, which would key the topic to the wrong id.
	id, err := strconv.Atoi(m[2])
	if err != nil {
		return nil, fmt.Errorf("tapochek: topic id %q is out of range: %w", m[2], err)
	}
	return &domain.Topic{
		TrackerName: pluginName,
		URL:         rawURL,
		DisplayName: fmt.Sprintf("Tapochek topic %d", id),
		Extra:       map[string]any{"topic_id": id},
	}, nil
}

// --- session ------------------------------------------------------------

// cookieUserIDRe reads the account id out of the serialised bb_data array.
// The key is `uid`, not phpBB's usual `user_id`.
var cookieUserIDRe = regexp.MustCompile(`"uid";i:(-?\d+);`)

// sessionUserID reports the account id the SERVER has assigned this jar, and
// whether the jar is authenticated at all. A guest is issued no bb_data
// cookie, so a missing cookie is a legitimate "not signed in" rather than an
// error. The value arrives percent-encoded and net/http/cookiejar stores it
// verbatim, so it is unescaped before matching — falling back to the raw
// value rather than failing, since an unescape error must not be reported as
// "logged out".
func sessionUserID(sess *forumcommon.Session, base string) (int, bool) {
	u, err := url.Parse(base)
	if err != nil {
		return 0, false
	}
	raw, ok := forumcommon.CookiesByName(sess, u, []string{sessionCookie})[sessionCookie]
	if !ok {
		return 0, false
	}
	decoded := raw
	// PathUnescape, not QueryUnescape: the latter also turns "+" into a
	// space, which would corrupt any future bb_data field that can contain
	// one. Only digits are read today, so this is insurance, not a fix.
	if s, uerr := url.PathUnescape(raw); uerr == nil {
		decoded = s
	}
	m := cookieUserIDRe.FindStringSubmatch(decoded)
	if m == nil {
		return 0, false
	}
	id, cerr := strconv.Atoi(m[1])
	if cerr != nil {
		return 0, false
	}
	return id, id > 0
}

// configure installs the plugin's redirect guard (and any test transport) on
// a freshly built session. It must run exactly once, at creation: the store
// hands one *Session to every topic of a user and the scheduler's worker pool
// drives them concurrently, so assigning Client.CheckRedirect on each use
// would race a reader inside Client.Do.
func (p *plugin) configure(sess *forumcommon.Session) {
	sess.Client.CheckRedirect = p.checkRedirect
	if p.transport != nil {
		sess.Client.Transport = p.transport
	}
}

func (p *plugin) newSession() *forumcommon.Session {
	sess := forumcommon.NewSession(userAgent)
	p.configure(sess)
	return sess
}

func (p *plugin) session(creds *domain.TrackerCredential) *forumcommon.Session {
	key := pluginName + ":nocreds"
	if creds != nil {
		key = forumcommon.SessionKey(pluginName, creds.UserID.String())
	}
	return p.sessions.GetOrCreateWith(key, userAgent, p.configure)
}

// --- WithCredentials ----------------------------------------------------

// wrongCredentialsRe matches the message the live site returns on a bad
// login: "Вы ввели неверное имя пользователя или неверный пароль." It only
// picks the wording of the error — the authoritative success signal is the
// session cookie, so a rephrasing downgrades the message rather than
// breaking login detection.
var wrongCredentialsRe = regexp.MustCompile(`(?i)неверно[ае] имя пользователя|неверный пароль`)

func (p *plugin) Login(ctx context.Context, creds *domain.TrackerCredential) error {
	if creds == nil || creds.Username == "" {
		return errors.New("tapochek credentials are required")
	}
	// Validate on an unstored jar: posting a password onto an already
	// authenticated session proves nothing about the password, and a shared
	// jar must never be published in an anonymous state (see
	// SessionStore.Invalidate's doc).
	sess := p.newSession()
	form := url.Values{
		"login_username": {creds.Username},
		"login_password": {string(creds.SecretEnc)},
		// The form ships an empty form_token to guests; sending what the form
		// sends costs nothing and keeps a future CSRF check from silently
		// rejecting us.
		"form_token": {""},
		// phpBB checks for the submit field's presence, not its value.
		"login": {"1"},
		// autologin is deliberately NOT sent. It asks the tracker to mint the
		// durable `uk` key, which signs in without the password and stays
		// valid server-side until someone logs out. Marauder holds the jar in
		// memory for at most forumcommon.sessionTTL and never persists it, so
		// the persistence buys nothing — while the scheduler logs in before
		// every check, so each tick would leave another live key behind. uid
		// is set in bb_data either way.
	}
	body, err := p.post(ctx, sess, p.baseURL()+"/login.php", form)
	if err != nil {
		return fmt.Errorf("tapochek login: %w", err)
	}
	// The server's own view of the jar, not a phrase in the page: a
	// successful login answers 302 with an EMPTY body, so any body-matching
	// check sees nothing and would report success for a wrong password too.
	if _, ok := sessionUserID(sess, p.baseURL()); !ok {
		// The failure page is windows-1251 like every other page, so the
		// message has to be decoded before it can be matched. Without this
		// a rejected password reports the vague "no session was
		// established" instead of naming the credentials — measured
		// against the live site on 2026-09-04.
		if wrongCredentialsRe.MatchString(forumcommon.DecodeWindows1251(string(body))) {
			return errors.New("tapochek login failed: username or password rejected")
		}
		return errors.New("tapochek login failed: no session was established")
	}
	sess.LoggedIn = true
	p.sessions.Put(forumcommon.SessionKey(pluginName, creds.UserID.String()), sess)
	return nil
}

// Verify reports whether the stored session is still live. It makes a real
// request first so the server can refresh (or decline to refresh) bb_data on
// that response, then reads the jar.
func (p *plugin) Verify(ctx context.Context, creds *domain.TrackerCredential) (bool, error) {
	sess := p.session(creds)
	if _, err := p.get(ctx, sess, p.baseURL()+"/index.php"); err != nil {
		return false, fmt.Errorf("tapochek verify: %w", err)
	}
	_, ok := sessionUserID(sess, p.baseURL())
	return ok, nil
}

// --- topic parsing ------------------------------------------------------

var (
	titleRe = regexp.MustCompile(`(?s)<title>([^<]*)</title>`)

	// torrentBlockOpenRe opens the release's torrent table. Exactly one such
	// table exists per topic page and it holds every field Check and Download
	// need, so anchoring here keeps quoted posts and the "similar releases"
	// chrome from contributing to the change token (verified on five live
	// topics, 2026-09-04).
	//
	// The class is matched as a TOKEN anywhere in the attribute, not as its
	// prefix. Live markup is `class="attach bordered med"`, but CSS class
	// order carries no meaning, so a template that reorders it to
	// `class="bordered attach med"` would otherwise stop every Tapochek
	// check — a cosmetic edit disabling the plugin.
	torrentBlockOpenRe = openTagWithClass("table", "attach")

	// dlHrefRe is the one structurally stable field in the block: a numeric
	// attachment id, not a Russian label a template change could rename.
	dlHrefRe = regexp.MustCompile(`href="(download\.php\?id=(\d+))"`)

	// fileNameRe reads the .torrent filename from the block's header cell.
	// Same token match: today the cell is `class="genmed"` alone, but adding
	// a second class to it must not lose the filename.
	fileNameRe = regexp.MustCompile(`(?s)<th[^>]*` + classToken("genmed") + `[^>]*>([^<]+)</th>`)

	// regDateRe steps from the "Зарегистрирован" label straight into the
	// <span> holding the timestamp. It must not use a lazy `.*?` across the
	// cell: the span carries a title attribute ("11 лет") that a loose match
	// would capture instead of the date.
	regDateRe = regexp.MustCompile(`(?s)Зарегистрирован\s*(?:&nbsp;|\s)*\[\s*<span[^>]*>([^<]+)</span>`)

	// sizeRe anchors on the whole label cell. A loose match on "Размер"
	// would find "Размер .torrent файла 9 KB" in the download cell — the
	// size of the torrent file, not of the release.
	// The cells tolerate attributes: every sibling cell in the same table
	// already carries one (`<td width="15%">`), so requiring an
	// attribute-free <td> made this match luck of the template — and a
	// dropped size moves every stored token at once.
	sizeRe = regexp.MustCompile(`(?s)<td[^>]*>Размер:</td>\s*<td[^>]*>([^<]+)</td>`)

	// posterVarRe finds every <var> tag; posterURL then inspects the tag's
	// attributes rather than demanding a fixed class order and a `title` that
	// immediately follows `class`. Live markup happens to be
	// `class="postImg postImgAligned img-right" title="..."`, but neither CSS
	// class order nor HTML attribute order carries meaning, so pinning them
	// would let a purely cosmetic template edit silently drop cover art.
	posterVarRe = regexp.MustCompile(`(?s)<var\b[^>]*>`)

	// tagAttrRe reads one named attribute out of a tag. Single and double
	// quotes are both accepted because the surrounding markup mixes them.
	tagAttrRe = regexp.MustCompile(`(?s)\b([a-zA-Z_:][-\w:.]*)\s*=\s*(?:"([^"]*)"|'([^']*)')`)

	// firstPostOpenRe scopes the poster search to the opening post. Replies
	// have their own post_body and may quote images of their own. Token
	// match again: `class="post_body signed"` must still open the block.
	firstPostOpenRe = openTagWithClass("div", "post_body")
)

// classToken builds the fragment matching a class ATTRIBUTE that contains
// name as one of its whitespace-separated tokens, in any position.
//
// `class="[^"]*name[^"]*"` would be wrong in both directions: it matches
// `class="unattached"` for the token `attach`, and a prefix-anchored
// `class="name[^"]*"` misses `class="bordered name"`. The optional
// whitespace-terminated prefix and whitespace-led suffix pin the token
// boundaries exactly, with no lookaround (RE2 has none).
func classToken(name string) string {
	return `class="(?:[^"]*\s)?` + regexp.QuoteMeta(name) + `(?:\s[^"]*)?"`
}

// openTagWithClass builds an opening-tag pattern for TagBlockInner: the
// named tag carrying class as one of its tokens, with the class attribute
// anywhere among the tag's attributes.
func openTagWithClass(tag, class string) *regexp.Regexp {
	return regexp.MustCompile(`(?s)<` + tag + `\b[^>]*` + classToken(class) + `[^>]*>`)
}

// tagAttrs parses a tag's attributes into a map. Values keep their raw
// (still entity-encoded) form; callers decode what they use.
func tagAttrs(tag string) map[string]string {
	out := map[string]string{}
	for _, m := range tagAttrRe.FindAllStringSubmatch(tag, -1) {
		value := m[2]
		if value == "" {
			value = m[3]
		}
		out[strings.ToLower(m[1])] = value
	}
	return out
}

// hasClassToken reports whether attrs' class attribute carries name as one of
// its whitespace-separated tokens.
func hasClassToken(attrs map[string]string, name string) bool {
	return slices.Contains(strings.Fields(attrs["class"]), name)
}

// cleanTitle turns a raw <title> into a display name. Tapochek serves
// windows-1251 and renders Cyrillic titles as HTML numeric entities, so both
// passes are needed and neither is redundant: DecodeWindows1251 is a no-op on
// the ASCII entity form but rescues a page that emits raw cp1251 bytes, and
// UnescapeString turns `&#1086;` into a letter rather than storing the
// entity text as the topic's name.
func cleanTitle(raw string) string {
	s := html.UnescapeString(forumcommon.DecodeWindows1251(raw))
	return strings.Join(strings.Fields(s), " ")
}

// torrentBlock returns the inner HTML of the release's torrent table.
func torrentBlock(body []byte) (string, bool) {
	return forumcommon.TagBlockInner(string(body), torrentBlockOpenRe, "table")
}

// firstPostBody returns the opening post's body, where the cover lives.
func firstPostBody(body []byte) (string, bool) {
	return forumcommon.TagBlockInner(string(body), firstPostOpenRe, "div")
}

// posterURL returns the release cover, or "" when the topic has none. Not
// every release carries one, and an absent cover must not fail a resolve and
// cost the topic its real title too.
func posterURL(body []byte) string {
	scope, ok := firstPostBody(body)
	if !ok {
		// Fail open to the whole page rather than losing the poster: the
		// worst case is a cover taken from a reply, which is still a cover.
		scope = string(body)
	}
	for _, tag := range posterVarRe.FindAllString(scope, -1) {
		attrs := tagAttrs(tag)
		if !hasClassToken(attrs, "postImgAligned") {
			continue
		}
		// img-center is excluded: it appears mid-description on decorated
		// topics rather than as the cover.
		if !hasClassToken(attrs, "img-right") && !hasClassToken(attrs, "img-left") {
			continue
		}
		if url := strings.TrimSpace(html.UnescapeString(attrs["title"])); url != "" {
			return url
		}
	}
	return ""
}

// fingerprintInput builds the human-readable string the change token
// digests. Split out from pageFingerprint so a test can assert something a
// reviewer can read instead of only a golden digest.
//
// Download counts and seeder/leecher counts are deliberately excluded: they
// change on their own and would make every check look like a new release.
// The NUL separator cannot occur in HTML text, so no value can forge a field
// boundary.
func fingerprintInput(block string) string {
	// EVERY field is required. A field that silently drops out of the token
	// moves every stored token at once, and the next tick then treats every
	// Tapochek topic as a new release — N spurious downloads, and a stacked
	// duplicate torrent for each topic that is not on replace-on-update.
	// Nothing in the logs would say a selector broke, because the checks all
	// succeed. Returning "" instead turns that same drift into a visible
	// failed check on one topic.
	//
	// The download id is the only structurally stable one; the other three
	// hang off a Russian label a template edit could rename, which is exactly
	// why none of them may be optional.
	id := dlHrefRe.FindStringSubmatch(block)
	name := fileNameRe.FindStringSubmatch(block)
	size := sizeRe.FindStringSubmatch(block)
	// The registration timestamp is the field Tapochek moves when an uploader
	// replaces a torrent — the event being watched.
	date := regDateRe.FindStringSubmatch(block)
	if id == nil || name == nil || size == nil || date == nil {
		return ""
	}
	return strings.Join([]string{
		"id=" + id[2],
		"name=" + normalizeCell(name[1]),
		"size=" + normalizeCell(size[1]),
		"registered=" + normalizeCell(date[1]),
	}, "\x00")
}

// normalizeCell decodes entities and collapses whitespace so the token
// depends on the CONTENT, not on the encoding. A token keyed to "&nbsp;"
// would change for every Tapochek topic at once — re-downloading all of
// them — the day the template emits a plain space instead.
func normalizeCell(s string) string {
	return strings.Join(strings.Fields(html.UnescapeString(forumcommon.DecodeWindows1251(s))), " ")
}

// pageFingerprint derives the change token for a topic.
//
// domain.Check.Hash is a change token, not an infohash: the scheduler only
// compares it to the previous value to decide whether something was
// published. The real infohash used for delivery tracking is computed
// downstream from the .torrent by the infohash package, so nothing needs one
// here — which is just as well, because Tapochek publishes none.
func pageFingerprint(block string) string {
	input := fingerprintInput(block)
	if input == "" {
		return ""
	}
	sum := sha256.Sum256([]byte(input))
	return hex.EncodeToString(sum[:])
}

func (p *plugin) Check(ctx context.Context, topic *domain.Topic, creds *domain.TrackerCredential) (*domain.Check, error) {
	target, err := p.canonicalURL(topic.URL)
	if err != nil {
		return nil, err
	}
	body, err := p.fetchPage(ctx, target, creds)
	if err != nil {
		return nil, err
	}
	check := &domain.Check{}
	if m := titleRe.FindSubmatch(body); m != nil {
		check.DisplayName = cleanTitle(string(m[1]))
	}
	block, ok := torrentBlock(body)
	if !ok {
		return nil, p.gateError(creds, errors.New("tapochek: no torrent block on the topic page"))
	}
	fp := pageFingerprint(block)
	if fp == "" {
		// Through gateError like every other "content is not there" path: a
		// ratio-limited account still gets the table but with a register link
		// where the download link was, and reporting that as a parse failure
		// sends the user hunting for a broken selector instead of
		// re-authenticating. With a live session it returns this unchanged.
		return nil, p.gateError(creds, errors.New("tapochek: torrent block carried no usable fields"))
	}
	check.Hash = fp
	return check, nil
}

func (p *plugin) Download(ctx context.Context, topic *domain.Topic, _ *domain.Check, creds *domain.TrackerCredential) (*domain.Payload, error) {
	target, err := p.canonicalURL(topic.URL)
	if err != nil {
		return nil, err
	}
	body, err := p.fetchPage(ctx, target, creds)
	if err != nil {
		return nil, err
	}
	block, ok := torrentBlock(body)
	if !ok {
		return nil, p.gateError(creds, errors.New("tapochek: no torrent block on the topic page"))
	}
	m := dlHrefRe.FindStringSubmatch(block)
	if m == nil {
		return nil, p.gateError(creds, errors.New("tapochek: no download link in the torrent block"))
	}
	torrent, err := p.fetch(ctx, p.baseURL()+"/"+m[1], creds)
	if err != nil {
		return nil, err
	}
	// download.php answers 200 with an HTML login page to a session it does
	// not accept, so a status check alone would hand that page to a torrent
	// client as if it were a file.
	if !isTorrent(torrent) {
		return nil, p.gateError(creds, errors.New("tapochek: download did not return a .torrent"))
	}
	name := "tapochek.torrent"
	if fm := fileNameRe.FindStringSubmatch(block); fm != nil {
		if n := safeFileName(normalizeCell(fm[1])); n != "" {
			name = n
		}
	}
	return &domain.Payload{TorrentFile: torrent, FileName: name}, nil
}

// isTorrent reports whether body looks like a bencoded dictionary. Cheap
// enough to run on every download and the only thing separating a real file
// from the login gate's HTML.
func isTorrent(body []byte) bool { return len(body) > 0 && body[0] == 'd' }

// gateError turns "the content is not there" into the typed sentinel the
// scheduler acts on when the reason is actually a lost session. Every field
// the plugin reads is login-gated, so a missing torrent block is far more
// often an expired session than a changed page — but only the cookie can
// tell, so it is consulted before making the claim.
//
// It answers from the jar rather than making a request: the fetch that just
// produced fallback already gave the server its chance to refresh bb_data.
// That matters because an expired session is a persistent state — every topic
// would otherwise make two requests per check.
func (p *plugin) gateError(creds *domain.TrackerCredential, fallback error) error {
	if creds == nil {
		return fallback
	}
	if _, ok := sessionUserID(p.session(creds), p.baseURL()); ok {
		return fallback
	}
	return fmt.Errorf("%w: tapochek session is no longer signed in", registry.ErrSessionExpired)
}

// --- WithMetadata -------------------------------------------------------

var _ registry.WithMetadata = (*plugin)(nil)

// ResolveMetadata returns the release title and cover so a new topic shows a
// real name and poster instead of a "Tapochek topic 155445" placeholder.
//
// It is read as the signed-in user: a guest gets a stub for most topics and
// no torrent block for any of them, so an anonymous resolve would store a
// placeholder name and no image — and while the scheduler self-heals a
// placeholder name on the first check, nothing backfills an image.
func (p *plugin) ResolveMetadata(ctx context.Context, rawURL string, creds *domain.TrackerCredential) (*registry.Metadata, error) {
	target, err := p.canonicalURL(rawURL)
	if err != nil {
		return nil, fmt.Errorf("resolve metadata: %w", err)
	}
	body, err := p.fetchPage(ctx, target, creds)
	if err != nil {
		return nil, fmt.Errorf("resolve metadata: %w", err)
	}
	m := titleRe.FindSubmatch(body)
	if m == nil {
		return nil, errors.New("tapochek: no title on the page")
	}
	title := cleanTitle(string(m[1]))
	if title == "" {
		return nil, p.gateError(creds, errors.New("tapochek: the page carried no title"))
	}
	return &registry.Metadata{Title: title, ImageURL: posterURL(body)}, nil
}

// --- transport ----------------------------------------------------------

// checkTarget is the guard applied to every URL before it is dialed — the
// initial request and each redirect hop alike. The plugin previously had no
// host guard at all, unlike rutor/rutracker/nnmclub/toloka.
//
// https only, not "https or http": every URL this plugin builds is already
// https (baseURL, canonicalURL), so the sole way a plain-http request could
// be dialled is a redirect hop — and Go's cookie jar attaches a non-Secure
// cookie to it, putting the session on the wire in plaintext. That is exactly
// what canonicalURL's https forcing exists to prevent, so the guard must not
// hand it back at the next hop.
func (p *plugin) checkTarget(u *url.URL) error {
	if u.Scheme != "https" {
		return fmt.Errorf("tapochek: refusing non-https URL scheme %q", u.Scheme)
	}
	if p.hostAllowed(u.Hostname()) {
		return nil
	}
	return fmt.Errorf("tapochek: refusing to fetch off-site host %q", u.Hostname())
}

// hostAllowed accepts the domain the plugin itself resolved plus the
// known and admin-configured allowlist.
//
// effectiveDomain() is trusted because it is operator-controlled — a
// test-injected host or the admin's active-domain setting — never a value
// scraped from a page. Without it the plugin can refuse its OWN configured
// host: registry.DomainAllowed consults the known list and the admin's
// custom list but not the active setting, so an active domain that is not
// also listed as custom would be built into every URL and then rejected
// before it was dialled. Same shape as the kinozal guard.
func (p *plugin) hostAllowed(host string) bool {
	// ToLower BEFORE TrimPrefix, matching registry.DomainAllowed: the other
	// order leaves "WWW." attached and the comparison silently misses.
	normalize := func(s string) string {
		return strings.TrimPrefix(strings.ToLower(strings.TrimSpace(s)), "www.")
	}
	if h := normalize(host); h != "" && h == normalize(p.effectiveDomain()) {
		return true
	}
	return registry.DomainAllowed(pluginName, host, knownDomains)
}

// checkRedirect re-runs the host guard on every hop. Login depends on
// following one redirect, so the chain cannot simply be refused.
func (p *plugin) checkRedirect(req *http.Request, via []*http.Request) error {
	if len(via) >= maxRedirects {
		return fmt.Errorf("tapochek: stopped after %d redirects", maxRedirects)
	}
	return p.checkTarget(req.URL)
}

func (p *plugin) fetch(ctx context.Context, target string, creds *domain.TrackerCredential) ([]byte, error) {
	return p.get(ctx, p.session(creds), target)
}

// fetchPage is fetch for HTML, decoded from windows-1251 to UTF-8.
//
// It is separate from fetch, not folded into it, because the same transport
// carries the .torrent: running a charset decode over those bytes would
// rewrite every byte above 0x7F and hand a corrupted file to the client.
// Decoding is required rather than cosmetic — every label the parser anchors
// on is Cyrillic ("Зарегистрирован", "Размер:"), so against the raw cp1251
// bytes the regexes match nothing and Check reports a torrent block with no
// fields. Measured against the live site on 2026-09-04.
func (p *plugin) fetchPage(ctx context.Context, target string, creds *domain.TrackerCredential) ([]byte, error) {
	body, err := p.fetch(ctx, target, creds)
	if err != nil {
		return nil, err
	}
	return []byte(forumcommon.DecodeWindows1251(string(body))), nil
}

func (p *plugin) get(ctx context.Context, sess *forumcommon.Session, target string) ([]byte, error) {
	return p.do(ctx, sess, http.MethodGet, target, nil)
}

func (p *plugin) post(ctx context.Context, sess *forumcommon.Session, target string, form url.Values) ([]byte, error) {
	return p.do(ctx, sess, http.MethodPost, target, form)
}

func (p *plugin) do(ctx context.Context, sess *forumcommon.Session, method, target string, form url.Values) ([]byte, error) {
	u, err := url.Parse(strings.TrimSpace(target))
	if err != nil {
		return nil, fmt.Errorf("tapochek: invalid URL: %w", err)
	}
	if err := p.checkTarget(u); err != nil {
		return nil, err
	}
	var reader io.Reader
	if form != nil {
		reader = strings.NewReader(form.Encode())
	}
	req, err := http.NewRequestWithContext(ctx, method, u.String(), reader)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", userAgent)
	if form != nil {
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	}
	resp, err := sess.Client.Do(req)
	if err != nil {
		// A *url.Error renders the FULL request URL, and on a refused
		// redirect Go rewrites its URL field to the Location header — so the
		// bare error would put a query string, or the off-site host the guard
		// just refused, into last_error and the UI tooltip. Unwrapping keeps
		// this path to the same "path only" promise as the status branch
		// below, and ue.Err preserves the wording classifyError matches on
		// ("context deadline exceeded", "no such host").
		var ue *url.Error
		if errors.As(err, &ue) {
			return nil, fmt.Errorf("tapochek %s %s: %w", method, u.Path, ue.Err)
		}
		return nil, fmt.Errorf("tapochek %s %s: %w", method, u.Path, err)
	}
	defer resp.Body.Close()
	// Only the path is named in errors: a search or redirect target can
	// carry a query string, which has no business in an error or a log.
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("tapochek %s %s -> %d", method, u.Path, resp.StatusCode)
	}
	// limit+1: io.ReadAll on a bare LimitReader cannot tell a body that ended
	// from one that was cut off, so an oversized .torrent would be truncated
	// and still pass isTorrent's first-byte check on its way to a client.
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxBodyBytes+1))
	if err != nil {
		return nil, fmt.Errorf("tapochek: reading %s: %w", u.Path, err)
	}
	if len(body) > maxBodyBytes {
		return nil, fmt.Errorf("tapochek: response from %s exceeds %d bytes", u.Path, maxBodyBytes)
	}
	return body, nil
}

// safeFileName reduces the scraped .torrent name to a single path segment.
//
// The name comes from the page, so the release uploader controls it, and the
// downloadfolder client turns Payload.FileName into a path. That client
// sanitises too — which is where the guarantee for every plugin lives — but a
// name carrying separators has no business travelling that far, and most
// sibling plugins build their own name rather than passing one through.
// Returning "" lets the caller keep its constant fallback.
func safeFileName(name string) string {
	name = strings.ReplaceAll(strings.TrimSpace(name), `\`, "/")
	name = strings.ReplaceAll(name, "\x00", "")
	base := path.Base(name)
	if base == "." || base == ".." || base == "/" {
		return ""
	}
	return base
}
