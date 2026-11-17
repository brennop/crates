// playlist-add: append tracks to a playlist, then return its full membership
// so the caller can compare it with what it expected.
// argv: [playlistID, trackID...]. Result: {"trackIDs": [...]}
function main(argv, music) {
  if (argv.length < 2) fail(-50, 'usage: playlist-add <playlistID> <trackID>...');
  const p = playlistByID(music, argv[0]);
  for (const id of argv.slice(1)) music.duplicate(trackByID(music, id), { to: p });
  return { trackIDs: playlistTrackIDs(p) };
}
