package tapochek

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
