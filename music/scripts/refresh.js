// refresh: make Music re-read a file track's tags and duration.
// argv: [persistentID]. Result: track info after the refresh.
function main(argv, music) {
  if (argv.length !== 1) fail(-50, 'usage: refresh <persistentID>');
  const t = trackByID(music, argv[0]);
  music.refresh(t);
  return trackInfo(t);
}
