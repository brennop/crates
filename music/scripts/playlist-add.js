// playlist-add: append tracks to a playlist, skipping ones already in it
// (Music would add a second copy), and return the membership read before and
// after the write so the caller can compare the whole playlist.
// argv: [playlistID, trackID...].
// Result: {"before": [...], "trackIDs": [...]} (trackIDs is after the write)
function main(argv, music) {
  if (argv.length < 2) fail(-50, 'usage: playlist-add <playlistID> <trackID>...');
  const p = playlistByID(music, argv[0]);
  const before = playlistTrackIDs(p);
  const have = {};
  for (const id of before) have[id] = true;
  for (const id of argv.slice(1)) {
    if (have[id]) continue;
    have[id] = true;
    music.duplicate(trackByID(music, id), { to: p });
  }
  return { before: before, trackIDs: playlistTrackIDs(p) };
}
