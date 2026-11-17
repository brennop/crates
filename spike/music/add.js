// add: add one file to the library and read back what Music recorded.
// argv: [posixPath]. Result: track info (id, location, name, ...).
function main(argv, music) {
  if (argv.length !== 1) fail(-50, 'usage: add <path>');
  const t = music.add(Path(argv[0]));
  if (!t) fail(-1, 'add returned nothing for ' + argv[0]);
  return trackInfo(t);
}
