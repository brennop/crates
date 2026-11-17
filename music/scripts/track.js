// track: read one track by persistent ID.
// argv: [persistentID]. Result: track info.
function main(argv, music) {
  if (argv.length !== 1) fail(-50, 'usage: track <persistentID>');
  return trackInfo(trackByID(music, argv[0]));
}
