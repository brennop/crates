// playlist-remove: remove every occurrence of the given tracks from a
// playlist (the library entries stay), then return its full membership.
// argv: [playlistID, trackID...]. Result: {"trackIDs": [...]}
function main(argv, music) {
  if (argv.length < 2) fail(-50, 'usage: playlist-remove <playlistID> <trackID>...');
  const p = playlistByID(music, argv[0]);
  for (const id of argv.slice(1)) {
    const hits = p.tracks.whose({ persistentID: id });
    for (let i = hits.length - 1; i >= 0; i--) music.delete(hits[i]);
  }
  return { trackIDs: playlistTrackIDs(p) };
}
