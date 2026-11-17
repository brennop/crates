// playlist-remove: remove every occurrence of the given tracks from a
// playlist (the library entries stay), and return the membership read before
// and after the write.
// argv: [playlistID, trackID...].
// Result: {"before": [...], "trackIDs": [...]} (trackIDs is after the write)
function main(argv, music) {
  if (argv.length < 2) fail(-50, 'usage: playlist-remove <playlistID> <trackID>...');
  const p = playlistByID(music, argv[0]);
  const before = playlistTrackIDs(p);
  for (const id of argv.slice(1)) {
    const hits = p.tracks.whose({ persistentID: id });
    for (let i = hits.length - 1; i >= 0; i--) music.delete(hits[i]);
  }
  return { before: before, trackIDs: playlistTrackIDs(p) };
}
