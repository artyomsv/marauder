// Package toloka implements a tracker plugin for toloka.to (Гуртом).
//
// Toloka is a Ukrainian phpBB-derived tracker. Everything torrent-shaped is
// behind login: to a guest, a release topic returns a ~7KB stub with an empty
// <title> and no download link, tracker.php echoes the query and returns zero
// rows, forum listings render their chrome with no topic rows, and
// download.php answers with HTML instead of a torrent. Measured 2026-09-03.
// That is why this plugin could never be validated anonymously.
//
// # No infohash on the page
//
// Release pages publish neither an infohash nor a magnet — verified across
// four live releases on 2026-09-03. The plugin previously looked for
// "Info hash: <40 hex>", an English label that does not exist on this
// Ukrainian site, so every check failed with "no infohash found" and no
// Toloka topic could ever work. Check now derives a change token from the
// torrent block the way anidub does for the same reason; see fingerprintInput.
//
// # Session state lives in a cookie, not in the page
//
// The server maintains `toloka_data`, a PHP-serialized blob carrying `userid`:
// -1 for a guest, the real account id once logged in. It is set on responses
// to authenticated and anonymous requests alike, so after any request the jar
// reflects the SERVER's view of the session. That single signal drives both
// Login and Verify, and it is language-independent — unlike the previous
// approach of grepping the body for "помилка"/"error", which the real failure
// page contains neither of ("Такий псевдонім не існує, або не збігається
// пароль"), so a wrong password was reported as a successful login.
//
// **Validation status:** login, session verification, change detection,
// search, and a real .torrent download were exercised against the live site
// with a real account on 2026-09-03. See toloka_live_test.go (build tag
// `live`), which reads credentials from the environment and skips without
// them.
package toloka

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
	"regexp"
	"strconv"
	"strings"

	"github.com/artyomsv/marauder/backend/internal/domain"
	"github.com/artyomsv/marauder/backend/internal/plugins/registry"
	"github.com/artyomsv/marauder/backend/internal/plugins/trackers/forumcommon"
)

const (
	pluginName  = "toloka"
	displayName = "Toloka.to"
	// defaultDomain is the only live host. Probed 2026-09-03: toloka.cc,
	// .pw, .in.ua and .info are dead or misconfigured; toloka.net and
	// toloka.org serve unrelated pages; hurtom.com is the sister community
	// portal, which links to Toloka topics but does not serve the tracker.
	// So the rotation ring has exactly one entry — there is nowhere to fail
	// over to, which is a fact about the tracker rather than a gap here.
	defaultDomain = "toloka.to"
	// sessionCookie carries the server's view of who this jar is.
	sessionCookie = "toloka_data"
	// maxRedirects bounds the redirect chain. Every hop is re-checked
	// against the host allowlist, so this is a cost bound, not a security
	// one. Login relies on following one redirect: a successful POST answers
	// 302 with an empty body.
	maxRedirects = 5
	// maxBodyBytes bounds a single response. A Toloka .torrent for a season
	// pack measured 227KB, so this is generous; the cap exists to stop a
	// hostile or broken response from being read into memory unbounded.
	maxBodyBytes = 8 << 20
	// maxSearchResults matches registry.WithSearch's first-page contract.
	maxSearchResults = 50
	userAgent        = "Marauder/0.4 (+https://marauder.cc)"
)

// urlPattern is host-agnostic; CanParse gates the captured host against the
// known + admin-configured domain allowlist (the SSRF barrier — see
// registry.DomainAllowed).
var urlPattern = regexp.MustCompile(`^https?://(?:www\.)?([^/]+)/t(\d+)`)

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

// effectiveDomain resolves the domain every request is built against:
// a test-injected p.domain wins (httptest servers), then the admin-
// configured active domain, then the compiled default.
func (p *plugin) effectiveDomain() string {
	if p.domain != defaultDomain {
		return p.domain
	}
	if d := registry.ActiveDomain(pluginName); d != "" {
		return d
	}
	return p.domain
}

func (p *plugin) baseURL() string { return "https://" + p.effectiveDomain() }

// canonicalURL rewrites rawURL's host to p.effectiveDomain() when that
// differs from the URL's own host — the nnmclub/rutor canonicalURL
// approach adapted to toloka. Check/Download re-fetch the stored topic
// URL directly, so this is the only place an active-domain override or
// mirror switch actually takes effect for those fetches. The scheme is
// forced to https: urlPattern accepts http, and a stored http:// topic
// would otherwise put the session cookie on the wire in plaintext.
func (p *plugin) canonicalURL(rawURL string) (string, error) {
	u, err := url.Parse(strings.TrimSpace(rawURL))
	if err != nil {
		return "", fmt.Errorf("toloka: invalid URL: %w", err)
	}
	if eff := p.effectiveDomain(); eff != "" && eff != u.Hostname() {
		u.Host = eff
	}
	u.Scheme = "https"
	return u.String(), nil
}

func (p *plugin) CanParse(rawURL string) bool {
	m := urlPattern.FindStringSubmatch(strings.TrimSpace(rawURL))
	return m != nil && registry.DomainAllowed(pluginName, m[1], knownDomains)
}

func (p *plugin) Parse(_ context.Context, rawURL string) (*domain.Topic, error) {
	m := urlPattern.FindStringSubmatch(strings.TrimSpace(rawURL))
	if m == nil {
		return nil, errors.New("not a toloka topic URL")
	}
	id, _ := strconv.Atoi(m[2]) // urlPattern guarantees a digit-only match
	return &domain.Topic{
		TrackerName: pluginName, URL: rawURL,
		DisplayName: fmt.Sprintf("Toloka topic %d", id),
		Extra:       map[string]any{"topic_id": id},
	}, nil
}

// --- session ------------------------------------------------------------

// cookieUserIDRe pulls userid out of the PHP-serialized toloka_data value:
//
//	a:2:{s:11:"autologinid";s:0:"";s:6:"userid";i:-1;}
//
// A guest is -1; a logged-in account is its own positive id.
var cookieUserIDRe = regexp.MustCompile(`"userid";i:(-?\d+);`)

// sessionUserID reports the account id the SERVER has assigned this jar, and
// whether the jar is authenticated at all. The cookie value arrives
// percent-encoded, and net/http/cookiejar stores it verbatim, so it is
// unescaped before matching — falling back to the raw value rather than
// failing, since an unescape error must not be reported as "logged out".
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
	if s, uerr := url.QueryUnescape(raw); uerr == nil {
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

// wrongCredentialsRe matches the message the live site returns on a bad
// login — "Такий псевдонім не існує, або не збігається пароль". It is only
// used to tell a rejected password apart from a broken request for the
// error message; the authoritative success signal is the session cookie,
// so a wording change downgrades the message rather than breaking login.
var wrongCredentialsRe = regexp.MustCompile(`(?i)не існує.{0,40}пароль|не збігається пароль`)

func (p *plugin) Login(ctx context.Context, creds *domain.TrackerCredential) error {
	if creds == nil || creds.Username == "" {
		return errors.New("toloka credentials are required")
	}
	// Validate on an unstored jar: posting a password onto an already
	// authenticated session proves nothing about the password, and a shared
	// jar must never be published in an anonymous state (see
	// SessionStore.Invalidate's doc).
	sess := p.newSession()
	form := url.Values{
		"username": {creds.Username},
		"password": {string(creds.SecretEnc)},
		// The submit button's real value. phpBB checks for the field's
		// presence, but sending what the form sends costs nothing.
		"login": {"Вхід"},
		// autologin is deliberately NOT sent. It asks the tracker to mint a
		// durable "remember me" key, which signs in without the password and
		// stays valid server-side until someone logs out. Marauder holds the
		// jar in memory for at most forumcommon.sessionTTL and never persists
		// it, so the persistence buys nothing — while the scheduler logs in
		// before every check, so each tick would leave another live key
		// behind. userid is set in toloka_data either way.
		"redirect": {""},
	}
	body, err := p.post(ctx, sess, p.baseURL()+"/login.php", form)
	if err != nil {
		return fmt.Errorf("toloka login: %w", err)
	}
	// The server's own view of the jar, not a phrase in the page: a
	// successful login answers 302 with an EMPTY body, so any body-matching
	// check sees nothing and would report success for a wrong password too.
	if _, ok := sessionUserID(sess, p.baseURL()); !ok {
		if wrongCredentialsRe.Match(body) {
			return errors.New("toloka login failed: username or password rejected")
		}
		return errors.New("toloka login failed: no session was established")
	}
	sess.LoggedIn = true
	p.sessions.Put(forumcommon.SessionKey(pluginName, creds.UserID.String()), sess)
	return nil
}

// Verify reports whether the stored session is still live. It makes a real
// request and then reads the cookie the server set on that response, so an
// expired session is caught: the server re-issues toloka_data with userid -1
// for a jar it no longer recognises.
func (p *plugin) Verify(ctx context.Context, creds *domain.TrackerCredential) (bool, error) {
	sess := p.session(creds)
	if _, err := p.get(ctx, sess, p.baseURL()+"/index.php"); err != nil {
		return false, fmt.Errorf("toloka verify: %w", err)
	}
	_, ok := sessionUserID(sess, p.baseURL())
	return ok, nil
}

// --- topic parsing ------------------------------------------------------

var (
	titleRe = regexp.MustCompile(`(?s)<title>([^<]*)</title>`)

	// ogImageRe reads the poster from the <head>. Toloka serves it through
	// thumb.hurtom.com and never renders it as an <img> in the post body, so
	// this is the only place it appears. Attribute order is not guaranteed,
	// hence the two alternatives.
	ogImageRe = regexp.MustCompile(`<meta[^>]+(?:property="og:image"[^>]+content="([^"]+)"|content="([^"]+)"[^>]+property="og:image")`)

	// torrentBlockOpenRe opens the release's torrent table. Everything Check
	// and Download need lives inside it, and anchoring there keeps quoted
	// posts elsewhere in the thread from contributing to the change token.
	torrentBlockOpenRe = regexp.MustCompile(`<table[^>]+class="btTbl"[^>]*>`)

	dlHrefRe   = regexp.MustCompile(`href="(download\.php\?id=(\d+))"`)
	fileNameRe = regexp.MustCompile(`<b>(?:&nbsp;|\s)*([^<]+?\.torrent)(?:&nbsp;|\s)*</b>`)
	// Both rows are "<td>label</td><td>value</td>"; the value cell may wrap
	// the text in a <span title="…"> (size does, registration date does not).
	// Both step straight from the label cell into the value cell. A lazy
	// `.*?` there would wander into the next tag's ATTRIBUTES instead: the
	// size value sits in <span title="Розмір частини: 2&nbsp;MB">, so the
	// first size-shaped text after the cell opens is the piece size, and the
	// change token would track that rather than the release.
	// `[^<]*` rather than a lazy `.*?`: lazy is not anchored, so if a value
	// cell ever stops matching (label kept, value reworded), the engine
	// backtracks past its </td> and attaches ANOTHER row's date or size to
	// the token — silently, and in the wrong direction. `[^<]*` cannot cross
	// a tag boundary, so a reworded cell drops the field instead of
	// borrowing a neighbour's.
	regDateRe = regexp.MustCompile(`Зареєстрований:[^<]*</td>\s*<td[^>]*>(?:&nbsp;|\s)*([0-9]{4}-[0-9]{2}-[0-9]{2}[^<]*?)(?:&nbsp;|\s)*</td>`)
	sizeRe    = regexp.MustCompile(`Розмір:[^<]*</td>\s*<td[^>]*>\s*(?:<span[^>]*>)?(?:&nbsp;|\s)*([0-9][0-9.,]*(?:&nbsp;|\s)*[KMGT]?B)`)

	// siteTitleSuffixRe strips the forum section Toloka appends to every
	// page title ("Release name — HD українською"). Anchored to the end and
	// requiring the spaced em dash the template emits, so a release name
	// containing a dash survives.
	siteTitleSuffixRe = regexp.MustCompile(`\s+—\s+[^—]+$`)
)

// cleanTitle turns "Release name — HD українською" into the release name.
func cleanTitle(raw string) string {
	s := strings.TrimSpace(html.UnescapeString(forumcommon.DecodeWindows1251(raw)))
	if trimmed := strings.TrimSpace(siteTitleSuffixRe.ReplaceAllString(s, "")); trimmed != "" {
		return trimmed
	}
	return s
}

// torrentBlock returns the inner HTML of the release's torrent table, or
// ("", false) when the page has none — which is what a guest gets, and what
// a discussion thread looks like.
//
// Taking only the FIRST match is deliberate and measured. A Toloka release
// page carries two `class="btTbl"` tables: the torrent metadata (filename,
// size, registration date, download link) and, below it, the torrent's file
// listing — "Папка: <name>" followed by every file and its own size. All four
// live releases inspected on 2026-09-03 had exactly that shape and exactly
// one `download.php?id=` link, i.e. one torrent per topic, unlike anidub
// where a page carries one block per quality variant. Anchoring here is what
// keeps the per-file sizes in the second table out of the change token.
func torrentBlock(body []byte) (string, bool) {
	return forumcommon.TagBlockInner(string(body), torrentBlockOpenRe, "table")
}

// fingerprintInput builds the human-readable string the change token
// digests. Split out from pageFingerprint so a test can assert something a
// reviewer can read instead of only a golden digest.
//
// Seeder and leecher counts are deliberately excluded: they change on their
// own and would make every check look like a new release. The registration
// timestamp is included because it is the field Toloka updates when an
// uploader replaces the torrent, which is exactly the event being watched.
// The NUL separator cannot occur in HTML text, so no value can forge a field
// boundary.
func fingerprintInput(block string) string {
	var parts []string
	m := dlHrefRe.FindStringSubmatch(block)
	if m == nil {
		// The download id is the one structurally stable field — every other
		// one hangs off a Ukrainian label that a template change could
		// rename. Requiring it means such a change surfaces as a failed
		// check rather than as a silently moved token, which would re-deliver
		// every Toloka topic at once.
		return ""
	}
	parts = append(parts, "id="+m[2])
	if m := fileNameRe.FindStringSubmatch(block); m != nil {
		parts = append(parts, "name="+normalizeCell(m[1]))
	}
	if m := sizeRe.FindStringSubmatch(block); m != nil {
		parts = append(parts, "size="+normalizeCell(m[1]))
	}
	// Required, like the id. This is the field Toloka moves when an uploader
	// replaces a torrent, so if its label ever changes a same-name, same-size
	// replacement would keep the old token and silently never download.
	// Failing the check instead makes the template drift visible.
	d := regDateRe.FindStringSubmatch(block)
	if d == nil {
		return ""
	}
	parts = append(parts, "registered="+normalizeCell(d[1]))
	if len(parts) == 0 {
		return ""
	}
	return strings.Join(parts, "\x00")
}

// normalizeCell decodes entities and collapses whitespace so the token
// depends on the CONTENT, not on the encoding. A token keyed to "&nbsp;"
// would change for every Toloka topic at once — re-downloading all of them —
// the day the template emits a plain space instead.
func normalizeCell(s string) string {
	return strings.Join(strings.Fields(html.UnescapeString(s)), " ")
}

// pageFingerprint derives the change token for a topic.
//
// domain.Check.Hash is a change token, not an infohash: the scheduler only
// compares it to the previous value to decide whether something was
// published. The real infohash used for delivery tracking is computed
// downstream from the .torrent by the infohash package, so nothing needs one
// here — which is just as well, because Toloka publishes none.
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
	body, err := p.fetch(ctx, target, creds)
	if err != nil {
		return nil, err
	}
	check := &domain.Check{}
	if m := titleRe.FindSubmatch(body); m != nil {
		check.DisplayName = cleanTitle(string(m[1]))
	}
	block, ok := torrentBlock(body)
	if !ok {
		return nil, p.gateError(creds, errors.New("toloka: no torrent block on the topic page"))
	}
	fp := pageFingerprint(block)
	if fp == "" {
		return nil, errors.New("toloka: torrent block carried no id, name, size or date")
	}
	check.Hash = fp
	return check, nil
}

func (p *plugin) Download(ctx context.Context, topic *domain.Topic, _ *domain.Check, creds *domain.TrackerCredential) (*domain.Payload, error) {
	target, err := p.canonicalURL(topic.URL)
	if err != nil {
		return nil, err
	}
	body, err := p.fetch(ctx, target, creds)
	if err != nil {
		return nil, err
	}
	block, ok := torrentBlock(body)
	if !ok {
		return nil, p.gateError(creds, errors.New("toloka: no torrent block on the topic page"))
	}
	m := dlHrefRe.FindStringSubmatch(block)
	if m == nil {
		return nil, errors.New("toloka: no download link in the torrent block")
	}
	torrent, err := p.fetch(ctx, p.baseURL()+"/"+m[1], creds)
	if err != nil {
		return nil, err
	}
	// download.php answers 200 with an HTML page to a session it does not
	// accept, so a status check alone would hand that page to a torrent
	// client as if it were a file.
	if !isTorrent(torrent) {
		return nil, p.gateError(creds, errors.New("toloka: download did not return a .torrent"))
	}
	name := "toloka.torrent"
	if fm := fileNameRe.FindStringSubmatch(block); fm != nil {
		name = normalizeCell(fm[1])
	}
	return &domain.Payload{TorrentFile: torrent, FileName: name}, nil
}

// isTorrent reports whether body looks like a bencoded dictionary. Cheap
// enough to run on every download and the only thing separating a real file
// from the login gate's HTML.
func isTorrent(body []byte) bool { return len(body) > 0 && body[0] == 'd' }

// gateError turns "the content is not there" into the typed sentinel the
// scheduler acts on when the reason is actually a lost session. Everything on
// Toloka is login-gated, so a missing torrent block is far more often an
// expired session than a changed page — but only Verify can tell, so it is
// asked before making the claim.
// It answers from the jar rather than making a request. The fetch that just
// produced fallback already refreshed toloka_data — the server sets it on
// every response, authenticated or not (measured 2026-09-03 on both) — so a
// Verify call here would spend a round-trip on an answer already in hand.
// That matters because an expired session is a persistent state: every topic
// would then make two requests per check against a tracker that 429s at six
// requests in three seconds.
func (p *plugin) gateError(creds *domain.TrackerCredential, fallback error) error {
	if creds == nil {
		return fallback
	}
	if _, ok := sessionUserID(p.session(creds), p.baseURL()); ok {
		return fallback
	}
	return fmt.Errorf("%w: toloka session is no longer signed in", registry.ErrSessionExpired)
}

// --- WithMetadata -------------------------------------------------------

var _ registry.WithMetadata = (*plugin)(nil)

// ResolveMetadata returns the release name and poster for the AddTopic
// preview and for the topic's stored display name, so a new Toloka topic
// shows a real title instead of "Toloka topic 33571" until its first check.
//
// The poster comes from the <head>'s og:image, not from the post body. That
// is worth stating because searching the body for an <img> finds nothing —
// every image there is site chrome, an avatar or a smiley — and concluding
// "this tracker has no posters" from that is wrong: all five releases
// re-checked on 2026-09-03 carry an og:image, served through
// thumb.hurtom.com.
//
// creds are required, unlike every other WithMetadata tracker: a guest gets
// a stub with an empty <title>, so resolving anonymously would silently
// return nothing at all rather than fail.
func (p *plugin) ResolveMetadata(ctx context.Context, rawURL string, creds *domain.TrackerCredential) (*registry.Metadata, error) {
	target, err := p.canonicalURL(rawURL)
	if err != nil {
		return nil, fmt.Errorf("resolve metadata: %w", err)
	}
	body, err := p.fetch(ctx, target, creds)
	if err != nil {
		return nil, fmt.Errorf("resolve metadata: %w", err)
	}
	m := titleRe.FindSubmatch(body)
	if m == nil {
		return nil, errors.New("toloka: no title on the page")
	}
	title := cleanTitle(string(m[1]))
	if title == "" {
		// The guest stub renders <title></title>. Saying so beats handing
		// back an empty name that looks like a parsing failure.
		return nil, p.gateError(creds, errors.New("toloka: the page carried no title"))
	}
	return &registry.Metadata{Title: title, ImageURL: ogImage(body)}, nil
}

// ogImage returns the absolute poster URL from the page head, or "" when the
// release has none. The two capture groups are the two attribute orders; only
// one of them is populated per match.
func ogImage(body []byte) string {
	m := ogImageRe.FindSubmatch(body)
	if m == nil {
		return ""
	}
	raw := string(m[1])
	if raw == "" {
		raw = string(m[2])
	}
	src := html.UnescapeString(strings.TrimSpace(raw))
	switch {
	case src == "":
		return ""
	case strings.HasPrefix(src, "//"):
		return "https:" + src
	case strings.HasPrefix(src, "https://"), strings.HasPrefix(src, "http://"):
		return src
	default:
		// Anything else is not a URL a browser should be handed.
		return ""
	}
}

// --- WithSearch ---------------------------------------------------------

var _ registry.WithSearch = (*plugin)(nil)

var (
	searchRowRe = regexp.MustCompile(`(?s)<tr[^>]*class="[^"]*prow[^"]*"[^>]*>(.*?)</tr>`)
	// The topic anchor wraps the title in <b>, and the row's next </a>
	// belongs to the uploader's profile link — so the title must be taken
	// from the bold element, not from "everything up to </a>".
	searchLinkRe = regexp.MustCompile(`(?s)<a[^>]+href="/?t(\d+)"[^>]*>\s*<b>(.*?)</b>`)
	// The size cell's TEXT is the size; its title attribute holds the
	// download speed ("title=\" 7&nbsp;MB/s \""), which an unanchored size
	// pattern matches first and reports as a 7 MB release.
	searchSizeRe = regexp.MustCompile(`<td[^>]*class="gensmall"[^>]*>\s*([0-9][0-9.,]*(?:&nbsp;|\s)*[KMGT]?B)\s*</td>`)
	// Seeder and leecher counts have dedicated cell classes; counting
	// positionally would drift with the column layout.
	searchSeedsRe = regexp.MustCompile(`(?s)class="seedmed"[^>]*>\s*<b>\s*(\d+)\s*</b>`)
)

// Search implements registry.WithSearch. tracker.php is login-gated: to a
// guest it renders the form, echoes the query back into the highlighter, and
// returns zero rows — with no error message at all. Reporting "no results"
// for that would be a lie, so a search without a live session returns the
// sentinel that tells the user to add an account.
func (p *plugin) Search(ctx context.Context, query string, creds *domain.TrackerCredential) ([]registry.SearchResult, error) {
	q := strings.TrimSpace(query)
	if q == "" {
		return nil, nil
	}
	if creds == nil {
		return nil, registry.ErrSearchRequiresCredentials
	}
	sess := p.session(creds)
	// Toloka serves UTF-8 (Content-Type on every response, measured
	// 2026-09-03), unlike rutracker's cp1251 — so ordinary escaping is right
	// and forumcommon.EncodeWindows1251Query would corrupt Cyrillic here.
	target := p.baseURL() + "/tracker.php?nm=" + url.QueryEscape(q)
	body, err := p.get(ctx, sess, target)
	if err != nil {
		return nil, fmt.Errorf("toloka search: %w", err)
	}
	if _, ok := sessionUserID(sess, p.baseURL()); !ok {
		return nil, registry.ErrSearchRequiresCredentials
	}
	var out []registry.SearchResult
	for _, row := range searchRowRe.FindAllSubmatch(body, -1) {
		cell := row[1]
		link := searchLinkRe.FindSubmatch(cell)
		if link == nil {
			continue
		}
		r := registry.SearchResult{
			Title:   forumcommon.HTMLToText(string(link[2])),
			URL:     fmt.Sprintf("%s/t%s", p.baseURL(), link[1]),
			Seeders: -1,
		}
		if m := searchSizeRe.FindSubmatch(cell); m != nil {
			r.Size = normalizeCell(string(m[1]))
		}
		if m := searchSeedsRe.FindSubmatch(cell); m != nil {
			if n, cerr := strconv.Atoi(string(m[1])); cerr == nil {
				r.Seeders = n
			}
		}
		out = append(out, r)
		if len(out) == maxSearchResults {
			break
		}
	}
	return out, nil
}

// --- transport ----------------------------------------------------------

// checkTarget is the guard applied to every URL before it is dialed — the
// initial request and each redirect hop alike. The plugin previously had no
// host guard on fetch at all, unlike rutor/rutracker/nnmclub.
// https only, not "https or http": every URL this plugin builds is already
// https (baseURL, canonicalURL), so the sole way a plain-http request could
// be dialled is a redirect hop — and Go's cookie jar attaches a non-Secure
// cookie to it, putting the session on the wire in plaintext. That is exactly
// what canonicalURL's https forcing exists to prevent, so the guard must not
// hand it back at the next hop.
func checkTarget(u *url.URL) error {
	if u.Scheme != "https" {
		return fmt.Errorf("toloka: refusing non-https URL scheme %q", u.Scheme)
	}
	if !registry.DomainAllowed(pluginName, u.Hostname(), knownDomains) {
		return fmt.Errorf("toloka: refusing to fetch off-site host %q", u.Hostname())
	}
	return nil
}

// checkRedirect re-runs the host guard on every hop. Login depends on
// following one redirect, so the chain cannot simply be refused.
func (p *plugin) checkRedirect(req *http.Request, via []*http.Request) error {
	if len(via) >= maxRedirects {
		return fmt.Errorf("toloka: stopped after %d redirects", maxRedirects)
	}
	return checkTarget(req.URL)
}

func (p *plugin) fetch(ctx context.Context, target string, creds *domain.TrackerCredential) ([]byte, error) {
	return p.get(ctx, p.session(creds), target)
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
		return nil, fmt.Errorf("toloka: invalid URL: %w", err)
	}
	if err := checkTarget(u); err != nil {
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
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusTooManyRequests {
		// Toloka throttles aggressively — six requests inside three seconds
		// was enough to trigger it (measured 2026-09-03). Saying so plainly
		// matters because the bare status reads like the tracker is broken,
		// and the fix is to back off rather than to investigate. The
		// "-> 429" marker is kept because the scheduler's classifyError
		// reads the status out of the message text; without it a Toloka 429
		// would classify as "unknown" while every other tracker's is
		// "unreachable". Only the path is named: a search target carries the
		// user's own query string, which has no business in an error or a log.
		return nil, fmt.Errorf("toloka %s %s -> %d: rate limited by the tracker; back off and retry later",
			method, u.Path, resp.StatusCode)
	}
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("toloka %s %s -> %d", method, u.Path, resp.StatusCode)
	}
	// limit+1: io.ReadAll on a bare LimitReader cannot tell a body that ended
	// from one that was cut off, so an oversized .torrent would be truncated
	// and still pass isTorrent's first-byte check on its way to a client.
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxBodyBytes+1))
	if err != nil {
		return nil, fmt.Errorf("toloka: reading %s: %w", u.Path, err)
	}
	if len(body) > maxBodyBytes {
		return nil, fmt.Errorf("toloka %s %s: response exceeds %d bytes", method, u.Path, maxBodyBytes)
	}
	return body, nil
}
