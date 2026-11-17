// playlist: read one playlist's flags and full membership.
// argv: [playlistID]. Result: {"id","name","smart","special","trackIDs"}
function main(argv, music) {
  if (argv.length !== 1) fail(-50, 'usage: playlist <playlistID>');
  const p = playlistByID(music, argv[0]);
  return { id: p.persistentID(), name: p.name(), smart: p.smart(), special: p.specialKind(), trackIDs: playlistTrackIDs(p) };
}
