// make-playlist: create a user playlist (spike only; the app never creates
// playlists). argv: [name]. Result: {"id","name"}
function main(argv, music) {
  const p = music.UserPlaylist({ name: argv[0] }).make();
  return { id: p.persistentID(), name: p.name() };
}
