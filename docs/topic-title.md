# Topic titles

A topic's **display name** is what Marauder shows in the topics list, in
notifications, and as the label of what it delivered. It is display-only: it
never names the torrent or folder in your download client (that is the
[folder name](folder-name.md)), and nothing matches on it — everything runs on
infohashes.

## Where the name comes from

When you add a topic, the name is taken from the first of these that answers:

1. the name you typed in the form (always authoritative);
2. the tracker's metadata for the page (a real release title and poster,
   resolved best-effort at add time);
3. the name the tracker plugin parses out of the URL.

A name that came from (3) is a **placeholder** (like `RuTracker topic 6635490`).
Marauder replaces it with the real title as soon as a successful check reports
a usable, different one — a failed check, or a tracker that resolves no title,
leaves the placeholder in place — and then **locks** the name while following
is off: later checks never overwrite a resolved title, because some trackers
report a noisier title on check than at add time. Repair a mis-named topic by
renaming it by hand.

## Following the tracker's title

Trackers do rename topics — a repack, a quality bump, a corrected release
name. The per-topic **Follow the tracker's title** option opts back into
keeping the name in sync: on every check, when the tracker's title differs
from the stored one, Marauder updates it. It is the choice the name lock
deliberately leaves to you.

### Enabling it

Per topic, in the **Add topic** or **Edit topic** form, tick **Follow the
tracker's title** under the display name.

Administrators can set what new topics start with in **Settings → Topic
defaults**. That default is copied onto each topic when it is created, so
changing it later never touches topics that already exist — and the per-topic
toggle always wins over it.

### Behaviour

- **Off by default.** Existing topics keep the locked behaviour.
- **Typing a name turns it off.** If you rename a topic yourself, the option
  is cleared and the name is yours: Marauder will not overwrite it again. The
  form unticks the box as you type, and the same rule is enforced when the
  change is saved, so an API rename behaves identically.
- **It survives the updates it causes.** The scheduler updating the name is
  not a rename; the option stays on until you rename the topic yourself or
  turn the option off.
- **A tracker that resolves no title never wipes yours.** An empty title on
  check is ignored.
- **Silent.** A renamed topic is not notified about — you see the new name in
  the list and in the next notification.
- **Past notifications keep the old name.** A notification carries the name
  the topic had when it was sent.

### Caveat

The title comes from what the tracker plugin reads on **check**, and its
quality varies per tracker. Most report the release name; some report a page
title instead of it. Turning the option
on means trusting this tracker's titles — if the result is worse, rename the
topic by hand, which turns the option off again.
