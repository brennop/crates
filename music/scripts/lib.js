// Shared prelude for every Music script. The runner concatenates it in front
// of the script source; each script defines main(argv, music) and returns a
// plain object. run() wraps that in JSON so Go only ever parses one shape:
//   {"ok": true,  "result": ...}
//   {"ok": false, "error": {"number": N, "message": "..."}}
// Nothing is interpolated into the source: every input arrives in argv.

const MUSIC_BUNDLE = 'com.apple.Music';

function run(argv) {
  try {
    const music = Application(MUSIC_BUNDLE);
    // Application(...).running() does not launch the app. Every script except
    // launch.js refuses to run against a Music that is not already running.
    if (typeof NEEDS_RUNNING === 'undefined' || NEEDS_RUNNING) {
      if (!music.running()) {
        return JSON.stringify({ ok: false, error: { number: -600, message: 'Music is not running' } });
      }
    }
    return JSON.stringify({ ok: true, result: main(argv, music) });
  } catch (e) {
    return JSON.stringify({
      ok: false,
      error: { number: e.errorNumber === undefined ? null : e.errorNumber, message: String(e.message || e) },
    });
  }
}

function fail(number, message) {
  const e = new Error(message);
  e.errorNumber = number;
  throw e;
}

// A track by persistent ID, searched in the main library playlist.
function trackByID(music, id) {
  const hits = music.libraryPlaylists[0].tracks.whose({ persistentID: id });
  if (hits.length === 0) fail(-1728, 'no track with persistent ID ' + id);
  return hits[0];
}

function playlistByID(music, id) {
  const hits = music.playlists.whose({ persistentID: id });
  if (hits.length === 0) fail(-1728, 'no playlist with persistent ID ' + id);
  return hits[0];
}

function posixOrNull(loc) {
  if (loc === null || loc === undefined) return null;
  try { return loc.toString(); } catch (e) { return null; }
}

function trackInfo(t) {
  let loc = null;
  try { loc = posixOrNull(t.location()); } catch (e) { loc = null; }
  return {
    id: t.persistentID(),
    location: loc,
    name: t.name(),
    artist: t.artist(),
    album: t.album(),
    duration: t.duration(),
  };
}

function playlistTrackIDs(p) {
  return p.tracks.persistentID();
}
