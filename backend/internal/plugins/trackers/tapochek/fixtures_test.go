package tapochek

import "strings"

// Fixtures captured from the live site on 2026-09-04 (topic t=289113) and
// trimmed. The MARKUP is real — the previous fixtures were invented, using an
// English "Info hash:" label this site has never served, so the old tests
// proved only that the regexes matched a page nobody had written.
//
// Every account-scoped value is fake. In particular the bb_data `uk` field is
// a persistent-login key: presenting it with a uid signs in WITHOUT the
// password, so a captured one is a credential and must never be committed.
// The tests only need uid > 0.

// fixtureTorrentBlock is the release's torrent table, verbatim apart from the
// download id. Note the traps it carries:
//
//   - "Размер .torrent файла 9 KB" sits in the download cell, ABOVE the
//     release's own "Размер:" row — a loose match on "Размер" finds the size
//     of the .torrent file instead of the release.
//   - the registration <span> carries a title attribute ("10 часов") that a
//     lazy `.*?` would capture in place of the date.
//   - "Скачан" and "Поблагодарили" counts drift on their own and must stay
//     out of the change token.
const fixtureTorrentBlock = `<table class="attach bordered med">
	<tr class="row3">
		<th colspan="3" class="genmed">Lady Death Demonicron [FitGirl Repack] [tapochek.net].torrent</th>
	</tr>
		<tr class="row1">
		<td width="15%">Трекер:</td>
		<td width="70%">
						Зарегистрирован &nbsp;
						[ <span title="10 часов">04-09-2026 00:16</span> ]
		</td>
		<td width="15%" rowspan="7" class="tCenter pad_6">
						<a href="download.php?id=189409" class="genmed">
			<p><span class="download-torrent-btn" title="Скачать торрент-файл">⇩ Скачать ⇩</span></p></a>
			<p class="small torrent-size-info">Размер .torrent файла 9&nbsp;KB</p>
		</td>
	</tr>
	<tr class="row1">
		<td>Скачан:</td>
		<td><span title="Раздача полностью скачана 12 раз">12 раз</span></td>
	</tr>
	<tr class="row1">
		<td>Размер:</td>
		<td>1.39&nbsp;GB</td>
	</tr>
	<tr class="row1">
		<td>Поблагодарили:</td>
		<td><span id="VT189409">7</span></td>
	</tr>
</table>`

// fixtureTopicTitle is entity-encoded exactly as the site serves it: the page
// is windows-1251 but renders Cyrillic titles as HTML numeric references, so
// the raw bytes are pure ASCII. Storing them undecoded would name the topic
// "&#1056;&#1077;&#1087;&#1072;&#1082;".
const fixtureTopicTitle = `Lady Death Demonicron (ENG) [&#1056;&#1077;&#1087;&#1072;&#1082;]`

// fixtureTopicHTML is a signed-in view: title, an opening post carrying the
// cover, and the torrent block.
//
// The cover is the FIRST postImgAligned in the opening post. The plain
// postImg before it is a banner and the one after is a screenshot — taking
// "the first image" would pick the banner.
var fixtureTopicHTML = `<html><head><title>` + fixtureTopicTitle + `</title>
<meta http-equiv="Content-Type" content="text/html; charset=windows-1251" />
</head><body>
<div class="post_body">
<var class="postImg" title="https://img.example/banner.png"></var>
<var class="postImg postImgAligned img-right" title="https://i1.imageban.ru/out/2026/09/03/cover.jpg"></var>
<var class="postImg" title="https://img.example/screenshot-1.jpg"></var>
<div>&#1054;&#1087;&#1080;&#1089;&#1072;&#1085;&#1080;&#1077;</div>
</div><!--/post_body-->
` + fixtureTorrentBlock + `
<div class="post_body">
<var class="postImg postImgAligned img-right" title="https://img.example/reply-image.png"></var>
</div><!--/post_body-->
</body></html>`

// fixtureGuestHTML is what a guest gets for a topic in a public forum: the
// title and description survive, but the download box is replaced with a link
// to the registration page and there is no torrent table at all.
const fixtureGuestHTML = `<html><head><title>` + fixtureTopicTitle + `</title></head><body>
<div class="post_body">
<var class="postImg postImgAligned img-right" title="https://i1.imageban.ru/out/2026/09/03/cover.jpg"></var>
</div><!--/post_body-->
<div><fieldset class="attach"><legend>Download</legend>
<h1 class="attach_link"><a href="profile.php?mode=register">&#1057;&#1082;&#1072;&#1095;&#1072;&#1090;&#1100;</a></h1>
</fieldset></div>
</body></html>`

// wrongPasswordHTML is the live failure page's message. A successful login
// answers 302 with an EMPTY body, so there is nothing to match on success —
// which is why the session cookie, not this text, is the authority.
const wrongPasswordHTML = `<html><body>
<h4 class="warnColor1 tCenter mrg_16">Вы ввели неверное имя пользователя или неверный пароль.</h4>
</body></html>`

// bb_data cookie values with the real SHAPE and invented contents. `uk` is
// the persistent-login key — a real one is a password-equivalent credential,
// so it is empty here; `uid` is the only field the plugin reads.
const (
	// guestCookie is never actually issued by the site — a guest gets no
	// bb_data at all — but a uid of 0 must also read as "not signed in", so
	// the plugin is held to both.
	guestCookie = `a%3A3%3A%7Bs%3A2%3A%22uk%22%3BN%3Bs%3A3%3A%22uid%22%3Bi%3A0%3Bs%3A3%3A%22sid%22%3Bs%3A20%3A%2200000000000000000000%22%3B%7D`
	userCookie  = `a%3A3%3A%7Bs%3A2%3A%22uk%22%3BN%3Bs%3A3%3A%22uid%22%3Bi%3A42%3Bs%3A3%3A%22sid%22%3Bs%3A20%3A%2211111111111111111111%22%3B%7D`
)

// fixtureTorrentBytes is a minimal bencoded dictionary. The real file's
// announce URL embeds a per-account passkey, so a captured .torrent is a
// credential and is never committed; the plugin only checks the first byte.
var fixtureTorrentBytes = []byte("d8:announce32:https://bt.example.test/announce4:infod4:name4:teste")

// fixtureSeriesTopicHTML is the OTHER page template Tapochek serves, captured
// from the live site on 2026-09-21 (topics t=288010, t=288620 and t=288645 —
// the three in issue #186) and trimmed.
//
// TV-series topics carry no postImgAligned <var> at all. Their cover is a
// plain `<img class="poster">`, and the only <var> tags on the page are the
// Kinopoisk/IMDb rating badges and the per-track language flags — none of
// them aligned, so the <var> selector alone finds nothing and every series
// topic was stored with no image.
//
// The screenshots that follow the cover are the trap: they are <img> tags
// too, and only the "poster" class separates them from the artwork.
var fixtureSeriesTopicHTML = `<html><head><title>` + fixtureTopicTitle + `</title>
<meta http-equiv="Content-Type" content="text/html; charset=windows-1251" />
</head><body>
<div class="post_body">
<var class="postImg" title="https://rating.kinopoisk.ru/5310825.gif"></var>
<var class="postImg" title="https://imdb.desol.one/tt27497393.png"></var>
<img src="https://i128.fastpic.org/big/2026/0726/30/cover.jpg" class="poster" />
<var class="postImg" title="https://tapochek.net/images/flags/mini/rus.jpg"></var>
<img src="https://img.example/screenshot-1.png" border="0" style="max-width:150px;max-height:100px;" alt="https://img.example/screenshot-1.png" />
<img src="https://img.example/screenshot-2.png" border="0" style="max-width:150px;max-height:100px;" alt="https://img.example/screenshot-2.png" />
</div><!--/post_body-->
` + fixtureTorrentBlock + `
</body></html>`

// fixtureGatedTorrentBlock is a MODELLED variant, not a capture: the real
// block with its download anchor pointed elsewhere. No gated account was
// available to capture from, so it stands on the site's documented behaviour
// — Tapochek gates downloading on ratio, rank and a daily cap and answers a
// gated account with the table intact and only the download.php link replaced
// — rather than on an observation.
//
// What rests on that assumption is narrow: only the id-only branch of
// blockFieldsError. If a real gated page also drops a labelled cell, the
// generic "missing: id, size" wording is what the user sees instead, which is
// still honest. Replace this fixture with a capture if a gated account is
// ever to hand.
var fixtureGatedTorrentBlock = strings.Replace(
	fixtureTorrentBlock,
	`<a href="download.php?id=189409" class="genmed">`,
	`<a href="profile.php?mode=viewprofile" class="genmed">`,
	1,
)

// fixtureSeedingTorrentBlock is a SECOND release's torrent table (t=288010,
// not the t=289113 of fixtureTorrentBlock), captured on 2026-09-21 from an
// account that already seeds it — the reporter's own paste on issue #186, ad
// markup trimmed.
//
// It is here for what only a real capture carries: the gold release-type
// banner, the Статус row with its inline <script>, and `class="seedmed"` on
// both the filename <th> and the download <a>. The site colours that cell by
// the VIEWER's relation to the torrent — the same page uses
// `seedmed`/`leechmed` for its seeder and leecher counts — so the class is
// per-user, per-topic state and cannot anchor a selector. That is why the bug
// was unreproducible for five days: the reporter seeds the release, we do not.
//
// It is the WRONG fixture for proving the viewer class does not move the
// change token, because the ids, names, sizes and dates differ from
// fixtureTorrentBlock — two unrelated blocks cannot be compared. The
// same-release differential that CAN prove it is fixtureSeedmedTorrentBlock
// below.
const fixtureSeedingTorrentBlock = `<table class="attach bordered med">
	<tr class="row3">
		<th colspan="3" class="seedmed">Стюарт Блум не смог спасти вселенную Stuart Fails to Save the Universe Сезон 1 Серии 1-8 из 10 [WEB-DL 1080p] [tapochek.net].torrent</th>
	</tr>
		<tr class="row4">
	    <th colspan="3" class="row7 gold-header torrent-type-header torrent-type-header--gold"><img src="images/tor_gold.gif" width="16" height="15" title="Золото" />&nbsp;ЗОЛОТАЯ РАЗДАЧА! СКАЧАННОЕ НЕ ЗАСЧИТЫВАЕТСЯ!&nbsp;<img src="images/tor_gold.gif" width="16" height="15" title="Золото" />&nbsp;</th>
	</tr>
			<tr class="row1">
		<td width="15%">Трекер:</td>
		<td width="70%">
						Зарегистрирован &nbsp;
						[ <span title="4 дня">17-09-2026 00:21</span> ]
		</td>
		<td width="15%" rowspan="7" class="tCenter pad_6">
						<a href="download.php?id=188304" class="seedmed">
			<p><span class="download-torrent-btn" title="Скачать торрент-файл">&#8681; Скачать &#8681;</span></p></a>
			<p class="small torrent-size-info">Размер .torrent файла 131&nbsp;KB</p>
						<br /><p class="small"><input type="button" value="Список файлов" id="tor-filelist-btn" onclick="show_filelist();return false;"></p>
        </td>
	</tr>
<tr class="row1">
<td>Статус:</td>
<td style="padding: 6px 4px;">
<script type="text/javascript">$('#tor-288010').html( $('#tor-status-txt').html() );</script>
<b>
<span  id="tor-288010-icon" class="tor-icon tor_status_txt">
<span style="color: green;">&radic;</span></span> <span id="tor-288010-text"> проверено</span>
</b>
</td>
</tr>
	<tr class="row1">
		<td>Скачан:</td>
		<td><span title="Раздача полностью скачана 38 раз">38 раз</span></td>
	</tr>
	<tr class="row1">
		<td>Размер:</td>
		<td>13.03&nbsp;GB</td>
	</tr>
	<tr class="row1">
		<td>Поблагодарили:</td>
		<td><span id="VT188304">25</span></td>
	</tr>
</table>`

// fixtureSeedmedTorrentBlock is fixtureTorrentBlock with the viewer-scoped
// class swapped, so the two differ in NOTHING else — not the download id, not
// the filename, not the size, not the registration date.
//
// That is the whole point. Comparing two captures of DIFFERENT releases proves
// only that each one parses; comparing these two proves the thing issue #186
// actually turned on, which is that the reader's relation to a release must
// not move its change token. Derived rather than captured because obtaining
// the genuine article means one account seeding a release while another does
// not, and the substitution is exactly what the server does.
var fixtureSeedmedTorrentBlock = strings.ReplaceAll(fixtureTorrentBlock, "genmed", "seedmed")

// fixtureNbspNameBlock is fixtureTorrentBlock with a trailing `&nbsp;` inside
// the filename cell — the shape that broke the first fix for this bug.
//
// Not invented: the gold banner in fixtureSeedingTorrentBlock ends
// `&nbsp;</th>` and the size cell one row down reads `1.39&nbsp;GB`, so this
// template emits them freely. A pattern ending `\s*</th>` does not match one
// (Go's `\s` is ASCII-only), so had the filename cell ever gained one, every
// Tapochek check would have failed at once.
var fixtureNbspNameBlock = strings.Replace(fixtureTorrentBlock,
	`[tapochek.net].torrent</th>`, `[tapochek.net].torrent&nbsp;</th>`, 1)
