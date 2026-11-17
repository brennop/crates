// delete: remove one library entry by persistent ID (the file stays), then
// count the entries left with that ID so the caller can confirm it is gone.
// argv: [persistentID]. Result: {"deleted": id, "remaining": N}
function main(argv, music) {
  if (argv.length !== 1) fail(-50, 'usage: delete <persistentID>');
  const id = argv[0];
  music.delete(trackByID(music, id));
  return { deleted: id, remaining: music.libraryPlaylists[0].tracks.whose({ persistentID: id }).length };
}
