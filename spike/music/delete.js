// delete: remove one library entry by persistent ID (the file stays), then
// confirm it is gone.
// argv: [persistentID]. Result: {"deleted": id}
function main(argv, music) {
  if (argv.length !== 1) fail(-50, 'usage: delete <persistentID>');
  const id = argv[0];
  music.delete(trackByID(music, id));
  if (music.libraryPlaylists[0].tracks.whose({ persistentID: id }).length !== 0) {
    fail(-1, 'track ' + id + ' still present after delete');
  }
  return { deleted: id };
}
